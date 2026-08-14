package digest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"digesterr/internal/feed"
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

type digestConfigJSON struct {
	Categories        []digestCategorySelection `json:"categories"`
	Feeds             []digestFeedSelection     `json:"feeds"`
	DefaultHighlights int                       `json:"default_highlights"`
	DefaultComments   int                       `json:"default_comments"`
	MaxItems          int                       `json:"max_items"`
	Prompt            string                    `json:"prompt"`
}

type Generator struct {
	queries         *store.Queries
	llm             llmChatter
	enrichers       map[string]feed.Enricher
	commentFetchers map[string]feed.CommentFetcher
	extractor       *feed.Poller
}

func NewGenerator(queries *store.Queries, llm llmChatter, poller *feed.Poller) *Generator {
	return &Generator{
		queries:         queries,
		llm:             llm,
		enrichers:       poller.Enrichers(),
		commentFetchers: poller.CommentFetchers(),
		extractor:       poller,
	}
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

func (g *Generator) Generate(ctx context.Context, cfg store.DigestConfig) (*store.DigestResult, error) {
	start := time.Now()
	genLogger := slog.With("config_id", cfg.ID, "config_name", cfg.Name)
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
	perFeed := maxItems / len(feedLimits)
	if perFeed < 1 {
		perFeed = 1
	}

	var allItems []store.Item
	for feedID := range feedLimits {
		feedItems, err := g.queries.ListUnreadDigestItemsByFeed(ctx, store.ListUnreadDigestItemsByFeedParams{
			FeedID: feedID,
			Limit:  int64(perFeed),
		})
		if err != nil {
			return nil, fmt.Errorf("listing unread items for feed %d: %w", feedID, err)
		}
		allItems = append(allItems, feedItems...)
	}

	filtered := make([]digestItem, 0, len(allItems))
	for _, item := range allItems {
		if lim, ok := feedLimits[item.FeedID]; ok {
			filtered = append(filtered, digestItem{Item: item, Highlights: lim.Highlights, Comments: lim.Comments})
		}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no unread items found for digest config %d", cfg.ID)
	}

	g.enrichAndExtractItems(ctx, filtered, genLogger)

	highlights, rest := g.selectHighlights(filtered)

	g.fetchCommentsForHighlights(ctx, highlights, genLogger)

	content, err := g.buildContent(ctx, dcfg, highlights, rest, genLogger)
	if err != nil {
		return nil, fmt.Errorf("building digest content: %w", err)
	}

	configSnapshot := cfg.Config
	contentJSON, _ := json.Marshal(content)

	result, err := g.queries.CreateDigestResult(ctx, store.CreateDigestResultParams{
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
		g.queries.CreateDigestResultItem(ctx, store.CreateDigestResultItemParams{
			ResultID:    result.ID,
			ItemID:      h.ID,
			IsHighlight: 1,
		})
		g.queries.UpdateItemStatus(ctx, store.UpdateItemStatusParams{
			ID:     h.ID,
			Status: "digested",
		})
	}
	for _, item := range rest {
		g.queries.CreateDigestResultItem(ctx, store.CreateDigestResultItemParams{
			ResultID:    result.ID,
			ItemID:      item.ID,
			IsHighlight: 0,
		})
		g.queries.UpdateItemStatus(ctx, store.UpdateItemStatusParams{
			ID:     item.ID,
			Status: "digested",
		})
	}

	slog.Info("digest generated", "result_id", result.ID, "config_id", cfg.ID, "config_name", cfg.Name, "highlights", len(highlights), "items", len(rest), "duration", time.Since(start))
	return &result, nil
}

type feedLimit struct {
	Highlights int
	Comments   int
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
	for i := range items {
		feedObj, err := g.queries.GetFeedByID(ctx, items[i].FeedID)
		if err != nil || feedObj.ID == 0 {
			continue
		}
		ft := feedObj.Type
		byFeedType[ft] = append(byFeedType[ft], &items[i].Item)
	}

	for feedType, feedItems := range byFeedType {
		enricher, ok := g.enrichers[feedType]
		if !ok {
			continue
		}
		if b, ok := enricher.(feed.BulkEnricher); ok {
			if err := b.BulkEnrich(ctx, feedItems); err != nil {
				genLogger.Debug("bulk enrich failed for digest", "type", feedType, "error", err)
			}
		} else {
			for _, item := range feedItems {
				if err := enricher.Enrich(ctx, item); err != nil {
					genLogger.Debug("enrich failed for digest item", "item_id", item.ID, "error", err)
				}
			}
		}
		for _, item := range feedItems {
			g.queries.UpdateItemPointsAndComments(ctx, store.UpdateItemPointsAndCommentsParams{
				ID:            item.ID,
				Points:        item.Points,
				CommentsCount: item.CommentsCount,
				CommentUrl:    item.CommentUrl,
			})
		}
		genLogger.Debug("digest enriched items", "type", feedType, "count", len(feedItems))
	}

	for i := range items {
		item := &items[i]
		if item.ExtractStatus == "done" || item.ExtractContent != "" {
			continue
		}
		feedObj, err := g.queries.GetFeedByID(ctx, item.FeedID)
		if err != nil || feedObj.ID == 0 || feedObj.ExtractContent == 0 {
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
		comments, err := cf.FetchComments(ctx, item.Item, item.Comments)
		if err != nil {
			genLogger.Debug("failed to fetch comments for highlight", "item_id", item.ID, "error", err)
			continue
		}
		sort.Slice(comments, func(a, b int) bool { return comments[a].Points > comments[b].Points })
		item.FetchedComments = comments
		genLogger.Info("fetched comments for highlight", "item_id", item.ID, "url", item.Url, "count", len(comments))
	}
}

func (g *Generator) buildContent(ctx context.Context, dcfg digestConfigJSON, highlights, rest []digestItem, genLogger *slog.Logger) (*digestResultContent, error) {
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
		if h.ExtractContent != "" && h.ExtractContent != "pending" && h.ExtractContent != "failed" {
			userPrompt += fmt.Sprintf("Content: %s\n", h.ExtractContent)
		} else if h.Content != "" {
			userPrompt += fmt.Sprintf("Content: %s\n", h.Content)
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

	if len(rest) > 0 {
		userPrompt += "Additional articles (not highlights, just for context):\n"
		for i, item := range rest {
			userPrompt += fmt.Sprintf("%d. %s (%s) - Points: %d\n", i+1, item.Title, item.Url, item.Points)
		}
		userPrompt += "\n"
	}

	userPrompt += "For each HIGHLIGHT article, provide a 2-3 sentence highlight capturing the key insight and why it matters — skip introductory phrases like \"The article discusses\" or \"The author explains\". Format your response as JSON with keys \"title\" (string, a short title for the whole digest, at most 8-10 words, catchy and derived from the digest contents; a light pun is welcome but not required), \"summary\" (string of overall digest summary) and \"highlights\" (array of {title, summary})."

	llmStart := time.Now()
	genLogger.Info("calling LLM for digest", "input_bytes", len(systemPrompt)+len(userPrompt))
	llmResponse, err := g.llm.Chat(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("LLM chat failed: %w", err)
	}
	genLogger.Info("LLM digest response received", "output_bytes", len(llmResponse), "duration", time.Since(llmStart))

	type llmOutput struct {
		Title      string `json:"title"`
		Summary    string `json:"summary"`
		Highlights []struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		} `json:"highlights"`
	}
	cleaned := strings.TrimSpace(llmResponse)
	cleaned = strings.TrimPrefix(cleaned, "```json\n")
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```\n")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "\n```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var output llmOutput
	if err := json.Unmarshal([]byte(cleaned), &output); err != nil {
		genLogger.Error("failed to parse LLM response", "error", err, "output_bytes", len(llmResponse))
		return nil, fmt.Errorf("parsing LLM response: %w", err)
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
	for i, h := range output.Highlights {
		if i < len(content.Highlights) {
			content.Highlights[i].LLMSummary = h.Summary
		}
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
	if item.ExtractContent != "" && item.ExtractStatus == "done" {
		return item.ExtractContent
	}
	return item.Content
}

func formatNullTime(nt sql.NullTime) string {
	if nt.Valid {
		return nt.Time.Format(time.RFC3339)
	}
	return ""
}
