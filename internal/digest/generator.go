package digest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

type digestConfigJSON struct {
	Categories []struct {
		ID         int `json:"id"`
		Highlights int `json:"highlights"`
		Comments   int `json:"comments"`
	} `json:"categories"`
	Feeds []struct {
		ID         int `json:"id"`
		Highlights int `json:"highlights"`
		Comments   int `json:"comments"`
	} `json:"feeds"`
	DefaultHighlights int    `json:"default_highlights"`
	DefaultComments   int    `json:"default_comments"`
	Prompt            string `json:"prompt"`
}

type Generator struct {
	queries   *store.Queries
	llm       *LLMClient
	enrichers map[string]feed.Enricher
	commentFetchers map[string]feed.CommentFetcher
	extractor *feed.Poller
}

func NewGenerator(queries *store.Queries, llm *LLMClient, poller *feed.Poller) *Generator {
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
	Summary    string          `json:"summary"`
	Highlights []digestItemRef `json:"highlights"`
	Items      []digestItemRef `json:"items"`
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
	var dcfg digestConfigJSON
	if err := json.Unmarshal([]byte(cfg.Config), &dcfg); err != nil {
		return nil, fmt.Errorf("parsing digest config: %w", err)
	}

	feedLimits := g.buildFeedLimits(ctx, cfg.UserID, dcfg)

	limit := int64(100)
	items, err := g.queries.ListUnreadDigestItems(ctx, store.ListUnreadDigestItemsParams{
		UserID: cfg.UserID,
		Limit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing unread digest items: %w", err)
	}

	filtered := make([]digestItem, 0, len(items))
	for _, item := range items {
		if lim, ok := feedLimits[item.FeedID]; ok {
			filtered = append(filtered, digestItem{Item: item, Highlights: lim.Highlights, Comments: lim.Comments})
		}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no unread items found for digest config %d", cfg.ID)
	}

	g.enrichAndExtractItems(ctx, filtered)

	highlights, rest := g.selectHighlights(filtered)

	g.fetchCommentsForHighlights(ctx, highlights)

	content, err := g.buildContent(ctx, dcfg, highlights, rest)
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

	slog.Info("digest generated", "result_id", result.ID, "config_id", cfg.ID, "highlights", len(highlights), "items", len(rest))
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
		catFeeds, err := g.queries.ListFeedsByCategory(ctx, store.ListFeedsByCategoryParams{
			CategoryID: sql.NullInt64{Int64: int64(cat.ID), Valid: true},
			UserID:     userID,
		})
		if err != nil {
			slog.Debug("failed to list category feeds", "category_id", cat.ID, "error", err)
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

func (g *Generator) enrichAndExtractItems(ctx context.Context, items []digestItem) {
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
				slog.Debug("bulk enrich failed for digest", "type", feedType, "error", err)
			}
		} else {
			for _, item := range feedItems {
				if err := enricher.Enrich(ctx, item); err != nil {
					slog.Debug("enrich failed for digest item", "item_id", item.ID, "error", err)
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
		slog.Debug("digest enriched items", "type", feedType, "count", len(feedItems))
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
		slog.Info("extracting content for digest item", "item_id", item.ID)
		if err := g.extractor.ExtractItem(ctx, item.ID); err != nil {
			slog.Debug("extraction failed for digest item", "item_id", item.ID, "error", err)
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

func (g *Generator) fetchCommentsForHighlights(ctx context.Context, highlights []digestItem) {
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
			slog.Debug("failed to fetch comments for highlight", "item_id", item.ID, "error", err)
			continue
		}
		sort.Slice(comments, func(a, b int) bool { return comments[a].Points > comments[b].Points })
		item.FetchedComments = comments
		slog.Info("fetched comments for highlight", "item_id", item.ID, "count", len(comments))
	}
}

func (g *Generator) buildContent(ctx context.Context, dcfg digestConfigJSON, highlights, rest []digestItem) (*digestResultContent, error) {
	content := &digestResultContent{}

	feedNames, _ := g.buildFeedNameMap(ctx)

	prompt := dcfg.Prompt
	if prompt == "" {
		prompt = "For each article, provide a 2-3 sentence highlight with the key insight and why it matters. Format response as JSON: {\"highlights\": [{\"title\": ..., \"summary\": ...}]}"
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

	userPrompt += "For each HIGHLIGHT article, provide a 2-3 sentence highlight capturing the key insight and why it matters — skip introductory phrases like \"The article discusses\" or \"The author explains\". Format your response as JSON with keys \"summary\" (string of overall digest summary) and \"highlights\" (array of {title, summary})."

	llmResponse, err := g.llm.Chat(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("LLM chat failed: %w", err)
	}

	type llmOutput struct {
		Summary    string `json:"summary"`
		Highlights []struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		} `json:"highlights"`
	}
	var output llmOutput
	if err := json.Unmarshal([]byte(llmResponse), &output); err != nil {
		content.Summary = llmResponse
	} else {
		content.Summary = output.Summary
	}

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
			FeedName:         feedNames[h.FeedID],
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
			FeedName:      feedNames[item.FeedID],
			Title:         item.Title,
			URL:           item.Url,
			CommentURL:    item.CommentUrl,
			Points:        item.Points,
			CommentsCount: item.CommentsCount,
			PublishedAt:   formatNullTime(item.PublishedAt),
		})
	}

	if output.Summary == "" && len(highlights) == 0 {
		return content, nil
	}

	for i, h := range output.Highlights {
		if i < len(content.Highlights) {
			content.Highlights[i].LLMSummary = h.Summary
		}
	}

	return content, nil
}

func (g *Generator) buildFeedNameMap(ctx context.Context) (map[int64]string, error) {
	allUserFeeds, err := g.queries.ListAllFeeds(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(allUserFeeds))
	for _, f := range allUserFeeds {
		m[f.ID] = f.Name
	}
	return m, nil
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
