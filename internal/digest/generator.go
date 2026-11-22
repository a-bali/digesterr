package digest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/llmjson"
	"digesterr/internal/metrics"
	"digesterr/internal/store"
)

type digestCategorySelection struct {
	Name       string `json:"name"`
	Highlights int    `json:"highlights"`
	Comments   int    `json:"comments"`
}

type digestFeedSelection struct {
	ID         int `json:"id"`
	Highlights int `json:"highlights"`
	Comments   int `json:"comments"`
}

// Extraction statuses, and why there are two "not extracted" spellings:
//
//	''        never extracted (the column default)
//	'pending' an extraction run is in flight
//	'done'    a body was extracted; it is what the UI and the digest prompt show
//	'failed'  the last attempt failed; extract_content holds the error log
//	'skipped' extraction was deliberately not attempted (self-hosted URL)
//	'cleared' the user deleted the extracted body; the item must stay on its
//	          original content and must NOT be picked up by an extraction pass
//
// An empty status and 'cleared' mean the same thing to every READER of these
// columns, but they must stay distinguishable to the WRITER: an item with no
// body is exactly the item an extraction pass wants to fetch, so without the
// marker a deletion is silently undone by the next pass. 'cleared' therefore
// outranks emptiness.
const extractionStatusCleared = "cleared"

// itemNeedsExtraction reports whether an item should be handed to the extractor.
// An empty body is not enough on its own — see the status table above.
func itemNeedsExtraction(item store.Item) bool {
	return item.ExtractStatus != "done" &&
		item.ExtractStatus != "skipped" &&
		item.ExtractStatus != extractionStatusCleared
}

type digestConfigJSON struct {
	Categories        []digestCategorySelection `json:"categories"`
	Feeds             []digestFeedSelection     `json:"feeds"`
	DefaultHighlights int                       `json:"default_highlights"`
	DefaultComments   int                       `json:"default_comments"`
	MaxItems          int                       `json:"max_items"`
	// MinScore is the minimum points/score an item must have (using the
	// freshest score available at generation time) to be included in the
	// digest. Zero means no threshold. Items from feeds that do not produce
	// scores (e.g. plain RSS/Atom) have no score data and always pass.
	MinScore int    `json:"min_score"`
	Prompt   string `json:"prompt"`
}

type Generator struct {
	db              *sql.DB
	queries         *store.Queries
	llm             llmChatter
	enrichers       map[string]feed.Enricher
	commentFetchers map[string]feed.CommentFetcher
	extractor       *feed.Poller
	// extractFlags caches feeds.extract_content per feed ID for one run.
	extractFlags map[int64]bool
	// recordFn is called for each outbound HTTP request made during
	// digest generation (enrichment, comment fetching). Nil means no recording.
	recordFn func(metrics.Record)
	// inFlight guards against two generations of the SAME config running at
	// once. The scheduler fires every minute and each run gets its own
	// goroutine, and the manual "Generate" button calls the same Generate: a
	// run slower than a minute (a multi-minute LLM budget is normal) would
	// otherwise overlap with the next scheduled tick or with a manual click,
	// producing duplicate digests and paying the LLM twice for the same work.
	//
	// Keyed by config ID, so a slow digest never blocks other configs.
	inFlight sync.Map
}

func NewGenerator(db *sql.DB, queries *store.Queries, llm llmChatter, poller *feed.Poller) *Generator {
	return &Generator{
		db:              db,
		queries:         queries,
		llm:             llm,
		enrichers:       poller.Enrichers(),
		commentFetchers: poller.CommentFetchers(),
		extractor:       poller,
	}
}

// SetRecordFunc sets the callback for recording outbound HTTP request metrics
// during digest generation (enrichment, comment fetching).
func (g *Generator) SetRecordFunc(fn func(metrics.Record)) {
	g.recordFn = fn
}

type digestItem struct {
	store.Item
	Highlights      int
	Comments        int
	FetchedComments []feed.Comment
}

type digestResultContent struct {
	Title      string          `json:"title,omitempty"`
	Summary    string          `json:"summary"`
	Sections   []digestSection `json:"sections,omitempty"`
	Highlights []digestItemRef `json:"highlights"`
	Items      []digestItemRef `json:"items"`
}

// digestSection groups digest items. A category section (Kind "category")
// nests feed sections; a feed section (Kind "feed") carries the items for one
// feed. The flat Highlights/Items arrays are kept alongside for backward
// compatibility with older digest results and readers.
type digestSection struct {
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`               // "category" or "feed"
	FeedID     int64           `json:"feed_id,omitempty"`  // set for feed sections
	Sections   []digestSection `json:"sections,omitempty"` // category sections nest feed sections
	Highlights []digestItemRef `json:"highlights,omitempty"`
	Items      []digestItemRef `json:"items,omitempty"`
}

type commentRef struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	Points *int   `json:"points,omitempty"`
	URL    string `json:"url,omitempty"`
}

type digestItemRef struct {
	ItemID           int64        `json:"item_id"`
	FeedID           int64        `json:"feed_id"`
	FeedName         string       `json:"feed_name"`
	Title            string       `json:"title"`
	URL              string       `json:"url"`
	CommentURL       string       `json:"comment_url"`
	Points           int64        `json:"points"`
	CommentsCount    int64        `json:"comments_count"`
	PublishedAt      string       `json:"published_at"`
	ExtractedContent string       `json:"extracted_content,omitempty"`
	LLMSummary       string       `json:"llm_summary,omitempty"`
	TopComments      []commentRef `json:"top_comments,omitempty"`
}

// llmDigestOutput is the shape the digest prompt asks the model for. The
// per-highlight entry type is llmHighlight, which also carries the article
// number used to align summaries with the articles they describe.
type llmDigestOutput struct {
	Title      string         `json:"title"`
	Summary    string         `json:"summary"`
	Highlights []llmHighlight `json:"highlights"`
}

// decodeDigestOutput decodes the model's reply into llmDigestOutput, tolerating
// the shapes models actually return: a bare object, a fenced code block, prose
// wrapped around the object, or a double-encoded JSON string. The first shape
// that yields an object wins; the returned error is the failure of the last
// candidate, which is the one that describes why nothing decoded.
func decodeDigestOutput(reply string) (llmDigestOutput, error) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return llmDigestOutput{}, fmt.Errorf("empty reply")
	}

	candidates := []string{trimmed}
	if obj, ok := llmjson.FirstObject(trimmed); ok {
		candidates = append(candidates, obj)
	}
	if inner, ok := llmjson.UnwrapString(trimmed); ok {
		candidates = append(candidates, inner)
		if obj, ok := llmjson.FirstObject(inner); ok {
			candidates = append(candidates, obj)
		}
	}

	var err error
	for _, candidate := range candidates {
		var output llmDigestOutput
		if unmarshalErr := json.Unmarshal([]byte(candidate), &output); unmarshalErr != nil {
			err = unmarshalErr
			continue
		}
		return output, nil
	}
	return llmDigestOutput{}, err
}

// ErrNoUnreadItems is returned when a config has nothing to digest. It is a
// normal outcome, not a failure: the pool is empty because every candidate was
// already read or digested — a quiet stretch, or a catch-up run hours after the
// occurrence it is recovering, by which time the items are long read. Recording
// it as an error painted a permanent-looking red mark on a healthy config and
// made a routine quiet morning read as a broken app.
var ErrNoUnreadItems = errors.New("no unread items to digest")

// ErrGenerationInProgress is returned when a generation for the same config is
// already running. It is a normal, expected outcome (the scheduler ticked again
// while a slow run was still going, or the user clicked Generate twice), not an
// error worth surfacing as a failure — callers should treat it as "skipped".
var ErrGenerationInProgress = errors.New("digest generation already in progress for this config")

// lockGeneration claims the per-config generation slot. It returns false when
// another run holds it.
func (g *Generator) lockGeneration(configID int64) bool {
	_, loaded := g.inFlight.LoadOrStore(configID, struct{}{})
	return !loaded
}

// unlockGeneration releases the slot claimed by lockGeneration.
func (g *Generator) unlockGeneration(configID int64) {
	g.inFlight.Delete(configID)
}

// Generate builds a digest for cfg. An optional llmChatter overrides the
// Generator's default client for this run: the HTTP layer resolves the
// caller's per-user model into a client via WithModel and passes it here, so
// scheduled and manual generations honor the same setting. Empty means "use
// the default client" — callers that predate the setting pass nothing.
//
// Returns ErrGenerationInProgress (without calling the LLM) when another
// generation for this config is already running.
func (g *Generator) Generate(ctx context.Context, cfg store.DigestConfig, llmOverride ...llmChatter) (*store.DigestResult, error) {
	// One generation per config at a time. Manual and scheduled runs both come
	// through here, so the lock is the only thing standing between a slow run
	// and a duplicate digest (plus a duplicate LLM bill).
	if !g.lockGeneration(cfg.ID) {
		slog.With("config_id", cfg.ID, "config_name", cfg.Name).
			Info("skipping digest generation, one is already in progress")
		return nil, ErrGenerationInProgress
	}
	defer g.unlockGeneration(cfg.ID)

	start := time.Now()
	genLogger := slog.With("config_id", cfg.ID, "config_name", cfg.Name)

	llm := g.llm
	if len(llmOverride) > 0 && llmOverride[0] != nil {
		llm = llmOverride[0]
	}

	// Data retention runs during digest generation as well as feed refresh:
	// delete items/digest results older than the configured windows. Saved
	// items are exempt (see DeleteExpiredItems).
	if g.extractor != nil {
		items, digests, err := g.extractor.ExpireOldData(ctx)
		if err != nil {
			genLogger.Error("data expiry failed during digest generation", "error", err)
		} else if items > 0 || digests > 0 {
			genLogger.Info("expired old data during digest generation", "items", items, "digest_results", digests)
		}
	}

	var dcfg digestConfigJSON
	if err := json.Unmarshal([]byte(cfg.Config), &dcfg); err != nil {
		return nil, fmt.Errorf("parsing digest config: %w", err)
	}

	feedLimits := g.buildFeedLimits(ctx, cfg.UserID, dcfg)

	if len(feedLimits) == 0 {
		genLogger.Warn("digest config matched no feeds", "user_id", cfg.UserID)
		return nil, fmt.Errorf("digest config %d matches no feeds", cfg.ID)
	}
	maxItems := dcfg.MaxItems
	if maxItems <= 0 {
		maxItems = 100
	}

	allItems, err := g.selectFeedItems(ctx, feedLimits, maxItems, time.Now(), genLogger)
	if err != nil {
		return nil, err
	}

	filtered := make([]digestItem, 0, len(allItems))
	for _, item := range allItems {
		if lim, ok := feedLimits[item.FeedID]; ok {
			filtered = append(filtered, digestItem{Item: item, Highlights: lim.Highlights, Comments: lim.Comments})
		}
	}

	if len(filtered) == 0 {
		// Not an error: there is simply nothing new to digest. Reported as a
		// sentinel so callers can treat it as "nothing to do" rather than a
		// failure worth showing the user.
		return nil, ErrNoUnreadItems
	}

	g.enrichAndExtractItems(ctx, filtered, genLogger)

	// Apply the min-score threshold on top of the freshly enriched scores
	// (enrichAndExtractItems re-fetches points at generation time). Items
	// below the threshold are excluded from the digest entirely.
	filtered = g.filterByMinScore(ctx, filtered, dcfg.MinScore, genLogger)

	highlights, rest := g.selectHighlights(filtered)

	g.fetchCommentsForHighlights(ctx, highlights, genLogger)

	content, err := g.buildContentWithLLM(ctx, dcfg, highlights, rest, genLogger, llm)
	if err != nil {
		return nil, fmt.Errorf("building digest content: %w", err)
	}

	configSnapshot := cfg.Config
	contentJSON, _ := json.Marshal(content)

	// Persist the result, its item links, and the digested status flips in a
	// single transaction: a partial failure must not leave a digest result
	// with missing links or items stuck in the wrong state.
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning digest persistence: %w", err)
	}
	defer tx.Rollback()
	qtx := g.queries.WithTx(tx)

	result, err := qtx.CreateDigestResult(ctx, store.CreateDigestResultParams{
		UserID:         cfg.UserID,
		ConfigID:       cfg.ID,
		ConfigSnapshot: configSnapshot,
		Content:        string(contentJSON),
		Status:         "new",
	})
	if err != nil {
		return nil, fmt.Errorf("creating digest result: %w", err)
	}

	for _, h := range highlights {
		if _, err := qtx.CreateDigestResultItem(ctx, store.CreateDigestResultItemParams{
			ResultID:    result.ID,
			ItemID:      h.ID,
			IsHighlight: 1,
		}); err != nil {
			return nil, fmt.Errorf("linking highlight item %d to result: %w", h.ID, err)
		}
		if _, err := qtx.UpdateItemStatus(ctx, store.UpdateItemStatusParams{
			ID:     h.ID,
			Status: "digested",
		}); err != nil {
			return nil, fmt.Errorf("marking item %d digested: %w", h.ID, err)
		}
	}
	for _, item := range rest {
		if _, err := qtx.CreateDigestResultItem(ctx, store.CreateDigestResultItemParams{
			ResultID:    result.ID,
			ItemID:      item.ID,
			IsHighlight: 0,
		}); err != nil {
			return nil, fmt.Errorf("linking item %d to result: %w", item.ID, err)
		}
		if _, err := qtx.UpdateItemStatus(ctx, store.UpdateItemStatusParams{
			ID:     item.ID,
			Status: "digested",
		}); err != nil {
			return nil, fmt.Errorf("marking item %d digested: %w", item.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing digest result: %w", err)
	}

	slog.Info("digest generated", "result_id", result.ID, "config_id", cfg.ID, "config_name", cfg.Name, "highlights", len(highlights), "items", len(rest), "duration", time.Since(start))
	return &result, nil
}

type feedLimit struct {
	Highlights int
	Comments   int
}

// backlogWindowFactor sizes the per-feed fetch window relative to the number of
// items that feed may actually contribute. Selection is age-weighted, so the
// rows that win are not necessarily the newest: a window of exactly the feed's
// budget would starve the same tail the ranking is meant to rescue. Fetching a
// multiple of the budget gives the ranking a real choice while keeping memory
// bounded on a feed with a very large backlog.
const backlogWindowFactor = 20

// ageHalfLife is how long an un-digested item waits before its age weight
// reaches 2x. Chosen so a few days of backlog meaningfully outranks a fresh
// high-scoring item, without making a week-old item dominate a genuinely
// important one on its first day.
const ageHalfLife = 72 * time.Hour

// maxAgeWeight caps the age bonus so a long-neglected item cannot drown out a
// feed's genuinely important items indefinitely. At the cap a stale item
// competes on roughly equal footing with a fresh one of 20x its score.
const maxAgeWeight = 20.0

// ageWeight returns the multiplier applied to an item's score for digest
// selection. It is 1.0 for an item created at `now` and grows linearly with the
// number of half-lives it has waited, up to maxAgeWeight.
//
// This is the fix for the starvation bug: ordering by points alone re-selects
// the newest items on every run (HN points correlate with recency), so older
// low-scoring items were never chosen and accumulated as unread forever. The
// weight gives every item a bounded wait instead of an unbounded one.
func ageWeight(createdAt, now time.Time) float64 {
	if createdAt.IsZero() || !now.After(createdAt) {
		return 1
	}
	w := 1 + float64(now.Sub(createdAt))/float64(ageHalfLife)
	if w > maxAgeWeight {
		return maxAgeWeight
	}
	return w
}

// selectionPriority ranks a candidate item. Score carries the "is this worth
// reading" signal; the age weight guarantees the backlog drains.
func selectionPriority(item store.Item, now time.Time) float64 {
	// +1 so a zero-score (RSS) item is not collapsed to zero priority: those
	// feeds carry no score data and must still be selectable.
	return float64(item.Points+1) * ageWeight(item.CreatedAt, now)
}

// selectFeedItems picks at most maxItems unread items across the configured
// feeds, in two passes:
//
//  1. Fair share. Each feed gets maxItems/len(feeds) slots so one busy feed
//     cannot consume the whole digest and starve the others (the original
//     intent of the per-feed split).
//  2. Redistribution. Slots a feed could not fill (its backlog is smaller than
//     its share) are handed to feeds that still have candidates, highest
//     priority first. Without this, an idle feed's share was simply wasted:
//     in production 291 of 1000 slots across ten runs went unused while HN's
//     backlog waited behind a cap that was never raised.
//
// Within a feed, candidates are ranked by selectionPriority (score adjusted by
// age), so hot items still lead but a neglected tail cannot be starved.
func (g *Generator) selectFeedItems(
	ctx context.Context,
	feedLimits map[int64]feedLimit,
	maxItems int,
	now time.Time,
	logger *slog.Logger,
) ([]store.Item, error) {
	if len(feedLimits) == 0 {
		return nil, nil
	}

	perFeed := maxItems / len(feedLimits)
	if perFeed < 1 {
		perFeed = 1
	}
	window := perFeed * backlogWindowFactor
	if window < perFeed {
		window = perFeed
	}

	// Fetch and rank each feed's backlog independently.
	ranked := make(map[int64][]store.Item, len(feedLimits))
	for feedID := range feedLimits {
		feedItems, err := g.queries.ListUnreadDigestItemsByFeed(ctx, store.ListUnreadDigestItemsByFeedParams{
			FeedID: feedID,
			Limit:  int64(window),
		})
		if err != nil {
			return nil, fmt.Errorf("listing unread items for feed %d: %w", feedID, err)
		}
		sort.SliceStable(feedItems, func(i, j int) bool {
			pi, pj := selectionPriority(feedItems[i], now), selectionPriority(feedItems[j], now)
			if pi != pj {
				return pi > pj
			}
			// Deterministic tie-break: oldest first, then lowest ID. Preferring
			// the oldest on a tie keeps the backlog draining predictably.
			if !feedItems[i].CreatedAt.Equal(feedItems[j].CreatedAt) {
				return feedItems[i].CreatedAt.Before(feedItems[j].CreatedAt)
			}
			return feedItems[i].ID < feedItems[j].ID
		})
		ranked[feedID] = feedItems
	}

	// Pass 1: each feed up to its fair share.
	taken := make(map[int64]int, len(feedLimits))
	selected := make([]store.Item, 0, maxItems)
	for feedID := range feedLimits {
		n := perFeed
		if len(ranked[feedID]) < n {
			n = len(ranked[feedID])
		}
		selected = append(selected, ranked[feedID][:n]...)
		taken[feedID] = n
	}

	// Pass 2: redistribute leftover slots to feeds with remaining candidates,
	// repeatedly picking the highest-priority item still available.
	remaining := maxItems - len(selected)
	for remaining > 0 {
		var (
			bestFeed int64
			bestIdx  = -1
			bestPrio float64
			found    bool
		)
		for feedID := range feedLimits {
			idx := taken[feedID]
			if idx >= len(ranked[feedID]) {
				continue
			}
			prio := selectionPriority(ranked[feedID][idx], now)
			if !found || prio > bestPrio || (prio == bestPrio && feedID < bestFeed) {
				bestFeed, bestIdx, bestPrio, found = feedID, idx, prio, true
			}
		}
		if !found {
			break
		}
		selected = append(selected, ranked[bestFeed][bestIdx])
		taken[bestFeed]++
		remaining--
	}

	if logger != nil {
		leftover := 0
		for feedID := range feedLimits {
			leftover += len(ranked[feedID]) - taken[feedID]
		}
		logger.Debug("digest item selection",
			"max_items", maxItems, "per_feed_share", perFeed,
			"window", window, "selected", len(selected), "left_unread", leftover)
	}

	return selected, nil
}

func (g *Generator) buildFeedLimits(ctx context.Context, userID int64, dcfg digestConfigJSON) map[int64]feedLimit {
	limits := make(map[int64]feedLimit)

	for _, fc := range dcfg.Feeds {
		h := fc.Highlights
		if h <= 0 {
			h = dcfg.DefaultHighlights
			if h <= 0 {
				h = 5
			}
		}
		c := fc.Comments
		if c <= 0 {
			c = dcfg.DefaultComments
			if c <= 0 {
				c = 10
			}
		}
		limits[int64(fc.ID)] = feedLimit{Highlights: h, Comments: c}
	}

	for _, cat := range dcfg.Categories {
		if cat.Name == "" {
			continue
		}
		catFeeds, err := g.queries.ListFeedsByCategory(ctx, store.ListFeedsByCategoryParams{
			Category: cat.Name,
			UserID:   userID,
		})
		if err != nil {
			slog.Debug("failed to list category feeds", "category", cat.Name, "error", err)
			continue
		}
		h := cat.Highlights
		if h <= 0 {
			h = dcfg.DefaultHighlights
			if h <= 0 {
				h = 5
			}
		}
		c := cat.Comments
		if c <= 0 {
			c = dcfg.DefaultComments
			if c <= 0 {
				c = 10
			}
		}
		for _, f := range catFeeds {
			if _, exists := limits[f.ID]; !exists {
				limits[f.ID] = feedLimit{Highlights: h, Comments: c}
			}
		}
	}

	if len(dcfg.Feeds) == 0 && len(dcfg.Categories) == 0 {
		allFeeds, err := g.queries.ListFeeds(ctx, userID)
		if err == nil {
			h := dcfg.DefaultHighlights
			if h <= 0 {
				h = 5
			}
			c := dcfg.DefaultComments
			if c <= 0 {
				c = 10
			}
			for _, f := range allFeeds {
				limits[f.ID] = feedLimit{Highlights: h, Comments: c}
			}
		}
	}

	return limits
}

func (g *Generator) enrichAndExtractItems(ctx context.Context, items []digestItem, genLogger *slog.Logger) {
	byFeedType := make(map[string][]*store.Item)
	feedTypes := g.resolveFeedTypes(ctx, items)
	// Snapshot the bodies as loaded from the DB so a body supplied by the
	// enricher (HN text posts) can be detected and persisted below.
	origContent := make(map[int64]string, len(items))
	for i := range items {
		origContent[items[i].ID] = items[i].Content
	}
	for i := range items {
		ft := feedTypes[items[i].FeedID]
		if ft == "" {
			continue
		}
		byFeedType[ft] = append(byFeedType[ft], &items[i].Item)
	}

	for feedType, feedItems := range byFeedType {
		enricher, ok := g.enrichers[feedType]
		if !ok {
			continue
		}
		enrichStart := time.Now()
		// Attribute from this pass's own requests. The digest runs alongside the
		// poller on the same transport, so the shared counters would mix the two.
		enrichScope := metrics.NewScope()
		enrichCtx := metrics.WithScope(ctx, enrichScope)
		var enrichErr error
		if b, ok := enricher.(feed.BulkEnricher); ok {
			enrichErr = b.BulkEnrich(enrichCtx, feedItems)
			if enrichErr != nil {
				genLogger.Debug("bulk enrich failed for digest", "type", feedType, "error", enrichErr)
			}
		} else {
			for _, item := range feedItems {
				if err := enricher.Enrich(enrichCtx, item); err != nil {
					genLogger.Debug("enrich failed for digest item", "item_id", item.ID, "error", err)
					if enrichErr == nil {
						enrichErr = err
					}
				}
			}
		}
		enrichBytes, enrichReqs := enrichScope.Totals()

		// Record enrichment metrics.
		if g.recordFn != nil && len(feedItems) > 0 {
			feedObj, ferr := g.queries.GetFeedByID(ctx, feedItems[0].FeedID)
			var feedName string
			var feedID int64
			var enrichHost string
			if ferr == nil {
				feedName = feedObj.Name
				feedID = feedObj.ID
				enrichHost = hostFromURL(feedObj.Url)
			}
			errStr := ""
			statusCode := 200
			if enrichErr != nil {
				errStr = enrichErr.Error()
				statusCode = metrics.StatusFor(enrichErr)
			}
			g.recordFn(metrics.Record{
				Timestamp:  enrichStart,
				Host:       enrichHost,
				StatusCode: statusCode,
				Bytes:      enrichBytes,
				Requests:   enrichReqs,
				DurationMs: time.Since(enrichStart).Milliseconds(),
				Reason:     "enrichment",
				FeedID:     feedID,
				FeedName:   feedName,
				FeedType:   feedType,
				Error:      errStr,
			})
		}

		for _, item := range feedItems {
			g.queries.UpdateItemPointsAndComments(ctx, store.UpdateItemPointsAndCommentsParams{
				ID:            item.ID,
				Points:        item.Points,
				CommentsCount: item.CommentsCount,
				CommentUrl:    item.CommentUrl,
			})
			if item.Content != origContent[item.ID] {
				_ = g.queries.UpdateItemContent(ctx, store.UpdateItemContentParams{
					ID:      item.ID,
					Content: item.Content,
				})
			}
		}
		genLogger.Debug("digest enriched items", "type", feedType, "count", len(feedItems))
	}

	for i := range items {
		item := &items[i]
		if !itemNeedsExtraction(item.Item) {
			continue
		}
		if feedTypes[item.FeedID] == "" {
			continue
		}
		extract, err := g.feedExtractsContent(ctx, item.FeedID, feedTypes)
		if err != nil || !extract {
			continue
		}
		genLogger.Info("extracting content for digest item", "item_id", item.ID, "url", item.Url)
		if err := g.extractor.ExtractItem(ctx, item.ID); err != nil {
			genLogger.Debug("extraction failed for digest item", "item_id", item.ID, "error", err)
		}
		refreshed, rerr := g.queries.GetItem(ctx, item.ID)
		if rerr == nil {
			item.ExtractContent = refreshed.ExtractContent
			item.ExtractStatus = refreshed.ExtractStatus
		}
	}
}

// filterByMinScore drops items whose generation-time score is below the
// configured threshold. minScore <= 0 disables filtering. Items whose feed
// does not produce scores (plain RSS/Atom — no enricher or a NoopEnricher)
// have no score data and always pass. Scores are compared after
// enrichAndExtractItems refreshed them, so the threshold applies to the
// freshest score available at generation time.
// resolveFeedTypes resolves each distinct feed ID to its type with one query
// per distinct feed (not per item), caching the result for the generation run.
func (g *Generator) resolveFeedTypes(ctx context.Context, items []digestItem) map[int64]string {
	types := make(map[int64]string)
	for _, it := range items {
		if _, ok := types[it.FeedID]; ok {
			continue
		}
		feedObj, err := g.queries.GetFeedByID(ctx, it.FeedID)
		if err != nil || feedObj.ID == 0 {
			types[it.FeedID] = ""
			continue
		}
		types[it.FeedID] = feedObj.Type
	}
	return types
}

// feedExtractsContent reports whether a feed has content extraction enabled,
// using the resolved type map plus a cached lookup of the extract flag.
func (g *Generator) feedExtractsContent(ctx context.Context, feedID int64, feedTypes map[int64]string) (bool, error) {
	if g.extractFlags == nil {
		g.extractFlags = make(map[int64]bool)
	}
	if v, ok := g.extractFlags[feedID]; ok {
		return v, nil
	}
	feedObj, err := g.queries.GetFeedByID(ctx, feedID)
	if err != nil {
		return false, err
	}
	v := feedObj.ID != 0 && feedObj.ExtractContent != 0
	g.extractFlags[feedID] = v
	return v, nil
}

func (g *Generator) filterByMinScore(ctx context.Context, items []digestItem, minScore int, genLogger *slog.Logger) []digestItem {
	if minScore <= 0 || len(items) == 0 {
		return items
	}
	scoreCapable := make(map[int64]bool)
	feedHasScores := func(feedID int64) bool {
		if v, ok := scoreCapable[feedID]; ok {
			return v
		}
		feedObj, err := g.queries.GetFeedByID(ctx, feedID)
		has := err == nil && feedObj.ID != 0 && g.feedTypeProducesScores(feedObj.Type)
		scoreCapable[feedID] = has
		return has
	}

	kept := make([]digestItem, 0, len(items))
	excluded := 0
	for _, item := range items {
		if !feedHasScores(item.FeedID) || item.Points >= int64(minScore) {
			kept = append(kept, item)
			continue
		}
		excluded++
	}
	if excluded > 0 {
		genLogger.Info("digest min_score filter excluded items", "min_score", minScore, "excluded", excluded, "kept", len(kept))
	}
	return kept
}

// feedTypeProducesScores reports whether items from a feed type carry a real
// score. Plain RSS/Atom feeds use the NoopEnricher and never get points.
func (g *Generator) feedTypeProducesScores(feedType string) bool {
	e, ok := g.enrichers[feedType]
	if !ok {
		return false
	}
	_, noop := e.(*feed.NoopEnricher)
	return !noop
}

func (g *Generator) selectHighlights(items []digestItem) ([]digestItem, []digestItem) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].Points > items[j].Points
	})

	perFeedRemaining := make(map[int64]int)
	for _, item := range items {
		if _, ok := perFeedRemaining[item.FeedID]; !ok {
			perFeedRemaining[item.FeedID] = item.Highlights
		}
	}

	var highlights, rest []digestItem
	for _, item := range items {
		rem := perFeedRemaining[item.FeedID]
		if rem > 0 {
			highlights = append(highlights, item)
			perFeedRemaining[item.FeedID] = rem - 1
		} else {
			rest = append(rest, item)
		}
	}
	return highlights, rest
}

func (g *Generator) fetchCommentsForHighlights(ctx context.Context, highlights []digestItem, genLogger *slog.Logger) {
	for i := range highlights {
		item := &highlights[i]
		feedObj, err := g.queries.GetFeedByID(ctx, item.FeedID)
		if err != nil {
			continue
		}
		cf, ok := g.commentFetchers[feedObj.Type]
		if !ok {
			continue
		}
		fetchStart := time.Now()
		fetchScope := metrics.NewScope()
		comments, err := cf.FetchComments(metrics.WithScope(ctx, fetchScope), item.Item, item.Comments)
		fetchBytes, fetchReqs := fetchScope.Totals()
		// Record comment-fetch metrics.
		if g.recordFn != nil {
			errStr := ""
			statusCode := 200
			if err != nil {
				errStr = err.Error()
				statusCode = metrics.StatusFor(err)
			}
			g.recordFn(metrics.Record{
				Timestamp:  fetchStart,
				URL:        cf.CommentTargetURL(),
				StatusCode: statusCode,
				Bytes:      fetchBytes,
				Requests:   fetchReqs,
				DurationMs: time.Since(fetchStart).Milliseconds(),
				Reason:     "enrichment",
				FeedID:     feedObj.ID,
				FeedName:   feedObj.Name,
				FeedType:   feedObj.Type,
				Error:      errStr,
			})
		}
		if err != nil {
			genLogger.Debug("failed to fetch comments for highlight", "item_id", item.ID, "error", err)
			continue
		}
		sort.Slice(comments, func(a, b int) bool { return comments[a].Points > comments[b].Points })
		item.FetchedComments = comments
		genLogger.Info("fetched comments for highlight", "item_id", item.ID, "feed_name", feedObj.Name, "url", cf.CommentTargetURL(), "count", len(comments))
	}
}

// maxContentForLLM caps the per-article content sent to the LLM to prevent
// oversized prompts from a single article. ~4000 chars ≈ 1000-1500 tokens.
const maxContentForLLM = 4000

var (
	htmlTagRe    = regexp.MustCompile(`</?[^>]+>`)
	whiteSpaceRe = regexp.MustCompile(`\s+`)
)

// CleanContentForLLM strips HTML tags, collapses whitespace, and truncates
// content so the LLM prompt stays manageable.
func CleanContentForLLM(raw string) string {
	s := htmlTagRe.ReplaceAllString(raw, " ")
	s = whiteSpaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if len(s) > maxContentForLLM {
		s = s[:maxContentForLLM] + "… [truncated]"
	}
	return s
}

// digestReplyComplete reports whether the reply carried enough usable entries to
// summarize every highlight article. A reply that parses but silently omits
// articles is as broken as one that does not parse: the omitted highlights
// render blank, and before alignment the remaining summaries shifted onto the
// wrong articles.
func digestReplyComplete(highlights []digestItem, entries []llmHighlight) bool {
	if len(highlights) == 0 {
		return true
	}
	_, missing := alignHighlights(highlights, entries)
	return missing == 0
}

// assignSummaries writes each reply entry's summary onto the article it
// describes and reports how many articles were matched. Entries that matched no
// article are dropped; articles that matched no entry keep an empty summary.
func assignSummaries(highlights []digestItem, refs []digestItemRef, entries []llmHighlight) (assignedCount, missing int) {
	assigned, missing := alignHighlights(highlights, entries)
	for articleIdx, entryIdx := range assigned {
		if entryIdx == unassigned || articleIdx >= len(refs) {
			continue
		}
		refs[articleIdx].LLMSummary = entries[entryIdx].Summary
		assignedCount++
	}
	return assignedCount, missing
}

// buildContent is the legacy entry point kept for existing callers (tests,
// benchmarks): it runs the content pass against the Generator's default LLM
// client.
func (g *Generator) buildContent(ctx context.Context, dcfg digestConfigJSON, highlights, rest []digestItem, genLogger *slog.Logger) (*digestResultContent, error) {
	return g.buildContentWithLLM(ctx, dcfg, highlights, rest, genLogger, g.llm)
}

// buildContentWithLLM is the run-specific content pass: Generate resolves the
// caller's per-user model and hands it in, so the selected model — not the
// instance default — serves both the attempt and the retry.
func (g *Generator) buildContentWithLLM(ctx context.Context, dcfg digestConfigJSON, highlights, rest []digestItem, genLogger *slog.Logger, llm llmChatter) (*digestResultContent, error) {
	// rest (non-highlight items) is kept for the result's item lists, but is
	// deliberately NOT sent to the model: entries are produced one per
	// highlight and nothing references the rest, so sending it was pure
	// prompt bloat. _ = rest documents that the omission is intentional.
	_ = rest
	content := &digestResultContent{}

	feedInfos := g.buildFeedInfoMap(ctx)

	prompt := dcfg.Prompt
	if prompt == "" {
		prompt = "For each article, provide a 2-3 sentence highlight with the key insight and why it matters. Also give the whole digest a short title of at most 8-10 words, catchy, optionally with a light pun. Format response as JSON: {\"title\": ..., \"highlights\": [{\"title\": ..., \"summary\": ...}]}"
	}

	systemPrompt := prompt
	userPrompt := "Here are the articles to summarize:\n\n"

	for i, h := range highlights {
		userPrompt += fmt.Sprintf("Article %d (HIGHLIGHT):\n", i+1)
		userPrompt += fmt.Sprintf("Title: %s\n", h.Title)
		userPrompt += fmt.Sprintf("URL: %s\n", h.Url)
		userPrompt += fmt.Sprintf("Points: %d | Comments: %d\n", h.Points, h.CommentsCount)
		// Only a SUCCESSFUL extraction holds a body worth sending. The column must be
		// judged by its STATUS: extract_content holds either a body or an error log
		// (poller.failItemExtraction appends "[timestamp] jina HTTP 403" lines to it),
		// so comparing the column itself against status literals was always true and
		// sent the failure log to the model as if it were the article. Same gate as
		// contentForRendering just below, which is the shape the reader already sees.
		if h.ExtractStatus == "done" && h.ExtractContent != "" {
			userPrompt += fmt.Sprintf("Content: %s\n", CleanContentForLLM(h.ExtractContent))
		} else if h.Content != "" {
			userPrompt += fmt.Sprintf("Content: %s\n", CleanContentForLLM(h.Content))
		}
		if len(h.FetchedComments) > 0 {
			userPrompt += "Top comments:\n"
			limit := h.Comments
			if limit > len(h.FetchedComments) {
				limit = len(h.FetchedComments)
			}
			if limit > 10 {
				limit = 10
			}
			for j, c := range h.FetchedComments[:limit] {
				userPrompt += fmt.Sprintf("  %d. [%d pts] %s: %s\n", j+1, c.Points, c.Author, c.Text)
			}
		}
		userPrompt += "\n"
	}

	// Non-highlight articles are deliberately NOT sent: the model produces
	// one entry per highlight and nothing references the rest, so the
	// "additional articles for context" block was pure prompt bloat (~50
	// titles/URLs on a typical Daily Tech run) that raised token cost and
	// the free-tier TPM-429 odds for zero output rows.

	// The format instruction asks for article numbers and exact URL echoes as
	// join keys. Measured on virtual-free (the weakest model, 2026-09-18,
	// 12 live trials over 10 full-size ~4KB/body articles): requesting the
	// number and URL alongside the title cost zero malformed replies from
	// JSON shape (the only 2 failures were the model emitting the number
	// as a string, now tolerated by llmHighlight.UnmarshalJSON), while the
	// URL echo makes alignment immune to title paraphrase. Alignment trusts
	// the number only as a complete permutation, the URL only on exact
	// match, and otherwise falls back to mutual-best title matching — never
	// to reply position.
	userPrompt += "For each HIGHLIGHT article, provide a 2-3 sentence highlight capturing the key insight and why it matters — skip introductory phrases like \"The article discusses\" or \"The author explains\". Where top comments are given, fold what they add into the highlight: note when commenters agree, and surface corrections, counterpoints, or key insights from the discussion. Format your response as JSON with keys \"title\" (string, a short title for the whole digest, at most 8-10 words, catchy and derived from the digest contents; a light pun is welcome but not required), \"summary\" (string of overall digest summary) and \"highlights\" (array of {article, url, title, summary}). Set \"article\" to the article's number (1.." + strconv.Itoa(len(highlights)) + ") exactly as shown above and copy its \"url\" EXACTLY as given — the number and url are how entries are matched to articles. Reply with exactly one entry per article. " + jsonOutputRule

	llmStart := time.Now()
	genLogger.Info("calling LLM for digest", "input_bytes", len(systemPrompt)+len(userPrompt))
	// Surface a clear error when the LLM is needed but not configured: the
	// request would otherwise fail with an obscure auth error downstream.
	if c, ok := llm.(interface{ HasAPIKey() bool }); ok && !c.HasAPIKey() {
		genLogger.Error("LLM API key is not configured (llm.api_key / LLM_API_KEY); digest generation will fail")
	}
	llmResponse, err := llm.Chat(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("LLM chat failed: %w", err)
	}
	genLogger.Info("LLM digest response received", "output_bytes", len(llmResponse), "duration", time.Since(llmStart))

	output, err := decodeDigestOutput(llmResponse)
	if err != nil || !digestReplyComplete(highlights, output.Highlights) {
		// One corrective retry, whatever went wrong: unparseable JSON, or a
		// well-formed reply that silently omitted articles. Both need the same
		// remedy — the model usually complies when the specific failure is
		// quoted back — and allowing one retry for each would double the cost of
		// a bad run.
		var reason string
		if err != nil {
			reason = "Your previous reply could not be parsed as JSON:\n  " + err.Error()
			genLogger.Warn("LLM digest response was not valid JSON, retrying",
				"error", err, "output_bytes", len(llmResponse), "response", llmjson.Excerpt(llmResponse, 500))
		} else {
			reason = fmt.Sprintf("Your previous reply had %d highlight entries, but %d articles were sent. "+
				"Reply with exactly one entry per article, and set \"article\" to that article's number (1..%d).",
				len(output.Highlights), len(highlights), len(highlights))
			genLogger.Warn("LLM digest response did not cover every highlight, retrying",
				"articles", len(highlights), "entries", len(output.Highlights))
		}
		// The retry prompt reuses the (already hardened) first-attempt prompt, so
		// apply withJSONRule defensively: a config-level prompt that already
		// contained the rule must not get it twice.
		retryPrompt := withJSONRule(userPrompt) + "\n\n" + reason +
			"\nReply again with only the JSON object described above — no prose, no markdown code fences, no extra words."
		retried, retryErr := llm.Chat(ctx, systemPrompt, retryPrompt)
		if retryErr != nil {
			return nil, fmt.Errorf("LLM chat failed on retry: %w", retryErr)
		}
		genLogger.Info("LLM digest response received on retry", "output_bytes", len(retried))
		retryOutput, retryParseErr := decodeDigestOutput(retried)
		switch {
		case retryParseErr != nil && err != nil:
			// Neither attempt parsed: nothing usable, so fail loudly with the
			// offending reply in the error.
			genLogger.Error("failed to parse LLM response", "error", retryParseErr,
				"output_bytes", len(retried), "response", llmjson.Excerpt(retried, 500))
			return nil, fmt.Errorf("parsing LLM response: %w (reply began: %s)", retryParseErr, llmjson.Excerpt(retried, 500))
		case retryParseErr != nil:
			// The first reply parsed but was incomplete and the retry is
			// unusable; keep the first, whose recognized entries still align.
			genLogger.Warn("retry reply was not valid JSON, keeping the first reply",
				"error", retryParseErr, "response", llmjson.Excerpt(retried, 500))
		case err != nil || len(retryOutput.Highlights) > len(output.Highlights):
			// Either the first reply never parsed, or the retry covered more
			// articles. Never merge the two replies: an entry must stay with the
			// reply that produced it.
			output = retryOutput
		}
		if !digestReplyComplete(highlights, output.Highlights) {
			genLogger.Warn("digest reply still incomplete after retry; uncovered highlights render without a summary",
				"articles", len(highlights), "entries", len(output.Highlights))
		}
	}
	content.Title = output.Title
	content.Summary = output.Summary

	for _, h := range highlights {
		var topComments []commentRef
		commentLimit := h.Comments
		if commentLimit > len(h.FetchedComments) {
			commentLimit = len(h.FetchedComments)
		}
		for j := 0; j < commentLimit; j++ {
			c := h.FetchedComments[j]
			cr := commentRef{
				Author: c.Author,
				Text:   c.Text,
				URL:    c.URL,
			}
			if c.ValidPoints {
				p := c.Points
				cr.Points = &p
			}
			topComments = append(topComments, cr)
		}
		content.Highlights = append(content.Highlights, digestItemRef{
			ItemID:           h.ID,
			FeedID:           h.FeedID,
			FeedName:         feedInfos[h.FeedID].name,
			Title:            h.Title,
			URL:              h.Url,
			CommentURL:       h.CommentUrl,
			Points:           h.Points,
			CommentsCount:    h.CommentsCount,
			PublishedAt:      formatNullTime(h.PublishedAt),
			ExtractedContent: contentForRendering(h.Item),
			TopComments:      topComments,
		})
	}
	for _, item := range rest {
		content.Items = append(content.Items, digestItemRef{
			ItemID:        item.ID,
			FeedID:        item.FeedID,
			FeedName:      feedInfos[item.FeedID].name,
			Title:         item.Title,
			URL:           item.Url,
			CommentURL:    item.CommentUrl,
			Points:        item.Points,
			CommentsCount: item.CommentsCount,
			PublishedAt:   formatNullTime(item.PublishedAt),
		})
	}

	// Assign per-highlight LLM summaries to the flat array before building
	// sections, so the nested section highlights carry them too. Rendering
	// prefers sections when present, so assigning after buildSections would
	// leave every highlight summary empty in the rendered digest.
	//
	// Assignment is by matched identity, never by reply position: a reply that
	// drops or reorders entries must not shift summaries onto other articles.
	// Entries that matched nothing are dropped rather than guessed at.
	assignedCount, missing := assignSummaries(highlights, content.Highlights, output.Highlights)
	if missing > 0 {
		genLogger.Warn("digest reply did not cover every highlight",
			"articles", len(highlights), "entries", len(output.Highlights),
			"assigned", assignedCount, "unassigned_articles", missing)
	}
	if len(output.Highlights) > assignedCount {
		genLogger.Warn("digest reply entries matched no article and were dropped",
			"entries", len(output.Highlights), "matched", assignedCount)
	}

	content.Sections = g.buildSections(dcfg, content.Highlights, content.Items, feedInfos)

	return content, nil
}

type feedInfo struct {
	name     string
	category string
}

func (g *Generator) buildFeedInfoMap(ctx context.Context) map[int64]feedInfo {
	allUserFeeds, err := g.queries.ListAllFeeds(ctx)
	if err != nil {
		return nil
	}
	m := make(map[int64]feedInfo, len(allUserFeeds))
	for _, f := range allUserFeeds {
		m[f.ID] = feedInfo{name: f.Name, category: f.Category}
	}
	return m
}

// buildSections groups digest items into a section tree: when categories are
// selected in the config, one section per selected category (in config order)
// with the category's feeds as nested feed sections; otherwise one feed
// section per feed, ordered by feed name. Feeds whose items appear in the
// digest but carry no category are emitted as top-level feed sections. The
// flat highlights/items arrays are passed through unchanged.
func (g *Generator) buildSections(dcfg digestConfigJSON, highlights, items []digestItemRef, feedInfos map[int64]feedInfo) []digestSection {
	type feedGroup struct {
		info       feedInfo
		highlights []digestItemRef
		items      []digestItemRef
	}
	byFeed := make(map[int64]*feedGroup)
	var feedIDs []int64
	collect := func(ref digestItemRef, highlight bool) {
		grp, ok := byFeed[ref.FeedID]
		if !ok {
			grp = &feedGroup{info: feedInfos[ref.FeedID]}
			byFeed[ref.FeedID] = grp
			feedIDs = append(feedIDs, ref.FeedID)
		}
		if highlight {
			grp.highlights = append(grp.highlights, ref)
		} else {
			grp.items = append(grp.items, ref)
		}
	}
	for _, h := range highlights {
		collect(h, true)
	}
	for _, it := range items {
		collect(it, false)
	}

	feedSection := func(feedID int64, grp *feedGroup) digestSection {
		return digestSection{
			Name:       grp.info.name,
			Kind:       "feed",
			FeedID:     feedID,
			Highlights: grp.highlights,
			Items:      grp.items,
		}
	}

	// Deterministic ordering: feeds by name.
	sort.Slice(feedIDs, func(i, j int) bool {
		return byFeed[feedIDs[i]].info.name < byFeed[feedIDs[j]].info.name
	})

	if len(dcfg.Categories) == 0 {
		sections := make([]digestSection, 0, len(feedIDs))
		for _, id := range feedIDs {
			sections = append(sections, feedSection(id, byFeed[id]))
		}
		return sections
	}

	// Categories selected: group feeds by their category.
	byCategory := make(map[string][]int64)
	for _, id := range feedIDs {
		cat := byFeed[id].info.category
		byCategory[cat] = append(byCategory[cat], id)
	}

	// Category order: the config's selection order first, then any remaining
	// categories (e.g. uncategorized feeds) alphabetically.
	var catOrder []string
	seen := make(map[string]bool)
	for _, c := range dcfg.Categories {
		if c.Name == "" || seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		catOrder = append(catOrder, c.Name)
	}
	var rest []string
	for cat := range byCategory {
		if !seen[cat] && cat != "" {
			rest = append(rest, cat)
		}
	}
	sort.Strings(rest)
	if !seen[""] {
		rest = append(rest, "")
	}
	catOrder = append(catOrder, rest...)

	var sections []digestSection
	for _, cat := range catOrder {
		ids := byCategory[cat]
		if len(ids) == 0 {
			continue
		}
		if cat == "" {
			// Uncategorized feeds appear as standalone feed sections.
			for _, id := range ids {
				sections = append(sections, feedSection(id, byFeed[id]))
			}
			continue
		}
		sec := digestSection{Name: cat, Kind: "category"}
		for _, id := range ids {
			sec.Sections = append(sec.Sections, feedSection(id, byFeed[id]))
		}
		sections = append(sections, sec)
	}
	return sections
}

func contentForRendering(item store.Item) string {
	// A failed extraction stores its error log in this column, so only a
	// successful body is content to render; 'pending'/'failed'/'cleared' hold
	// nothing usable. Original content is the fallback in every other case.
	if item.ExtractContent != "" && item.ExtractStatus == "done" {
		return feed.UpgradeStoredBody(item.ExtractContent)
	}
	return feed.UpgradeStoredBody(item.Content)
}

func formatNullTime(nt sql.NullTime) string {
	if nt.Valid {
		return nt.Time.Format(time.RFC3339)
	}
	return ""
}

// hostFromURL extracts the hostname from a URL string.
func hostFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return ""
}
