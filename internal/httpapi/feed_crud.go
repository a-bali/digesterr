package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// errExtractReadabilityExclusive is returned when a feed asks for both content
// sources at once. Both clean the article and both write items.extract_content, so
// whichever ran last would silently decide what the reader sees.
const errExtractReadabilityExclusive = "readability and extract_content are mutually exclusive: both clean the article and store it in the same place"

type createFeedRequest struct {
	Category       string `json:"category"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Config         string `json:"config"`
	ExtractContent int64  `json:"extract_content"`
	Readability    int64  `json:"readability"`
	MinScore       int64  `json:"min_score"`
}

func (s *Server) handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	var req createFeedRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Name == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "name and type are required")
		return
	}
	if req.MinScore < 0 {
		writeError(w, http.StatusBadRequest, "min_score must be zero or a positive integer")
		return
	}
	if req.Config == "" {
		req.Config = "{}"
	}
	// Email feeds carry a plus-address: it is normalised against the CALLER's own
	// inbox and checked for collision before it reaches the database. Addresses
	// are unique across the instance because an address is a routing key and two
	// feeds on it would ingest the same mail twice.
	if req.Type == "email" {
		addr, err := s.normalizeEmailFeedURL(req.URL, s.callerEmailInbox(r.Context(), userID))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.URL = addr
		if taken, err := s.emailAddressTaken(req.URL, 0); err != nil {
			writeServerError(w, "failed to check email address", err)
			return
		} else if taken {
			writeError(w, http.StatusBadRequest, "address already in use")
			return
		}
	}
	if req.ExtractContent != 0 && req.Readability != 0 {
		writeError(w, http.StatusBadRequest, errExtractReadabilityExclusive)
		return
	}
	feed, err := s.queries.CreateFeed(r.Context(), store.CreateFeedParams{
		UserID:         userID,
		Category:       req.Category,
		Type:           req.Type,
		Name:           req.Name,
		Url:            req.URL,
		Config:         req.Config,
		Status:         "active",
		ExtractContent: req.ExtractContent,
		Readability:    req.Readability,
		MinScore:       req.MinScore,
	})
	if err != nil {
		writeServerError(w, "failed to create feed", err)
		return
	}
	writeJSON(w, http.StatusCreated, feed)
}

// discoverFeedRequest asks for the name a feed declares for itself, so the
// add-feed form can prefill the name field from the URL alone.
type discoverFeedRequest struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// discoverTimeout bounds the upstream fetch behind name discovery.
const discoverTimeout = 20 * time.Second

// handleDiscoverFeed returns the name a feed gives itself ({type, url} ->
// {name}). Discovery is a convenience for the add-feed form, so an unreachable
// feed or a type without configured credentials answers with an empty name
// rather than an error — the user simply types the name.
func (s *Server) handleDiscoverFeed(w http.ResponseWriter, r *http.Request) {
	var req discoverFeedRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	req.URL = strings.TrimSpace(req.URL)
	if req.Type == "" || req.URL == "" {
		writeError(w, http.StatusBadRequest, "type and url are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), discoverTimeout)
	defer cancel()
	// An email feed's name comes from its own suffix, with no network call:
	// AgentMail has no "name this address" endpoint, and a lookup that could
	// time out would present as a broken form. Discovery here is a pure
	// convenience, matching the contract that a failed lookup returns "".
	if req.Type == "email" {
		inbox := s.callerEmailInbox(ctx, authedUserID(r))
		if suffix, ok := feed.EmailSplitAddress(req.URL, inbox); ok {
			writeJSON(w, http.StatusOK, map[string]string{"name": feed.EmailSuffixToName(suffix)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": ""})
		return
	}
	name, err := s.poller.DiscoverFeedName(ctx, req.Type, req.URL)
	if err != nil {
		slog.Debug("feed name discovery failed", "type", req.Type, "url", req.URL, "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// normalizeEmailFeedURL validates and canonicalises an email feed's address
// against the CALLER's own AgentMail inbox.
//
// The inbox is per account, so it is passed in rather than read off the Server:
// an empty inbox means this account has not configured the email feed type, and
// that is a 400 rather than a silent acceptance.
//
// The UI sends the full address it composed from the account's prefix and the
// user's suffix, so this is the boundary where a bad address must be caught:
// a wrong suffix is not a harmless typo (there is no catch-all on AgentMail), it
// is a feed that silently never receives anything.
func (s *Server) normalizeEmailFeedURL(raw, inbox string) (string, error) {
	if inbox == "" {
		return "", fmt.Errorf("email feeds are not configured for this account: add an AgentMail inbox and API key in Settings")
	}
	suffix, ok := feed.EmailSplitAddress(raw, inbox)
	if !ok {
		return "", fmt.Errorf("invalid email feed address: it must be %s<suffix>%s with a suffix of letters, digits, dots, dashes or underscores",
			feed.EmailInboxPrefix(inbox), feed.EmailInboxDomain(inbox))
	}
	return feed.EmailBuildAddress(inbox, suffix), nil
}

// emailAddressTaken reports whether another feed already claims this address.
//
// The address namespace is instance-wide: an address is a routing key, so two
// feeds on it would both read the same mail and every newsletter would be
// ingested twice. Email feeds are namespaced per account (only one account can
// hold a given inbox), so in practice a collision is either two feeds of the
// same owner or two feeds whose accounts share a mailbox. The caller must not
// reveal WHICH feed holds the address — it may belong to someone else — so the
// response is a bare "already in use".
//
// excludeID skips a feed's own row so re-saving an unchanged address is not a
// conflict with itself.
func (s *Server) emailAddressTaken(url string, excludeID int64) (bool, error) {
	feeds, err := s.queries.ListEmailFeedURLs(context.Background())
	if err != nil {
		return false, err
	}
	for _, f := range feeds {
		if f.Url == url && f.ID != excludeID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) handleListFeeds(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	feeds, err := s.queries.ListFeeds(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list feeds", err)
		return
	}
	if feeds == nil {
		feeds = []store.Feed{}
	}
	writeJSON(w, http.StatusOK, feeds)
}

func (s *Server) handleGetFeed(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	feed, err := s.queries.GetFeed(r.Context(), store.GetFeedParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

type updateFeedRequest struct {
	Name           *string `json:"name"`
	URL            *string `json:"url"`
	Config         *string `json:"config"`
	Category       *string `json:"category"`
	Status         *string `json:"status"`
	ExtractContent *int64  `json:"extract_content"`
	Readability    *int64  `json:"readability"`
	MinScore       *int64  `json:"min_score"`
}

func (s *Server) handleUpdateFeed(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := s.queries.GetFeed(r.Context(), store.GetFeedParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}
	var req updateFeedRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name := existing.Name
	if req.Name != nil {
		name = *req.Name
	}
	url := existing.Url
	if req.URL != nil {
		url = *req.URL
	}
	config := existing.Config
	if req.Config != nil {
		config = *req.Config
	}
	// Re-normalise an email feed's address on update, and re-check uniqueness
	// when it changed. The check ignores this feed's own row so saving without
	// touching the address is not a conflict with itself.
	if existing.Type == "email" {
		addr, err := s.normalizeEmailFeedURL(url, s.callerEmailInbox(r.Context(), userID))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		url = addr
		if taken, err := s.emailAddressTaken(url, existing.ID); err != nil {
			writeServerError(w, "failed to check email address", err)
			return
		} else if taken {
			writeError(w, http.StatusBadRequest, "address already in use")
			return
		}
	}
	category := existing.Category
	if req.Category != nil {
		category = *req.Category
	}
	status := existing.Status
	if req.Status != nil {
		status = *req.Status
	}
	extractContent := existing.ExtractContent
	if req.ExtractContent != nil {
		extractContent = *req.ExtractContent
	}
	readability := existing.Readability
	if req.Readability != nil {
		readability = *req.Readability
	}
	if extractContent != 0 && readability != 0 {
		writeError(w, http.StatusBadRequest, errExtractReadabilityExclusive)
		return
	}
	minScore := existing.MinScore
	if req.MinScore != nil {
		if *req.MinScore < 0 {
			writeError(w, http.StatusBadRequest, "min_score must be zero or a positive integer")
			return
		}
		minScore = *req.MinScore
	}
	feed, err := s.queries.UpdateFeed(r.Context(), store.UpdateFeedParams{
		ID:             id,
		UserID:         userID,
		Name:           name,
		Url:            url,
		Config:         config,
		Category:       category,
		Status:         status,
		ExtractContent: extractContent,
		Readability:    readability,
		MinScore:       minScore,
	})
	if err != nil {
		writeServerError(w, "failed to update feed", err)
		return
	}

	// If the feed left its category (moved or cleared), digest configs selecting
	// that category should stop counting this feed against it. The category
	// selection itself is dropped only when this was its LAST feed — it names the
	// category, so it stays valid while other feeds remain in it.
	if existing.Category != "" && category != existing.Category {
		if removed, err := s.pruneDigestConfigSources(r.Context(), userID, nil, existing.Category,
			s.feedsLeftInCategory(r.Context(), userID, existing.Category)); err != nil {
			slog.Warn("failed to prune digest config sources after category change",
				"feed_id", id, "category", existing.Category, "error", err)
		} else if removed > 0 {
			slog.Info("pruned digest config sources after category change",
				"feed_id", id, "category", existing.Category, "configs_updated", removed)
		}
	}

	writeJSON(w, http.StatusOK, feed)
}
