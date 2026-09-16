package digest

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// summaryArticles returns articles whose titles mirror the prod digest that
// exposed the misalignment (2026-09-16, config 1 "Daily Tech").
func summaryArticles() []digestItem {
	titles := []string{
		"Jev: New frontier model 40-400x cheaper and 20-200x faster",
		"An Update on Wayback Machine Access",
		"Gemini 3.8 Live and 3.8 Live Extended Thinking",
		"Building a Linux GPU Driver for the M4 Mac Mini in One Month",
		"America's Driver's License Breach Is a National Security Disaster",
		"GEFS on OpenBSD: A very early preview",
		"A/I Shuts Down",
		"Small Programming Tricks",
		"Converting a $20 4G wireless hotspot into a texting device",
		"Ubuntu 26.10 completes transition to Rust-based coreutils",
	}
	items := make([]digestItem, len(titles))
	for i, t := range titles {
		items[i] = digestItem{Item: store.Item{
			ID:     int64(100 + i),
			FeedID: 7,
			Title:  t,
			Url:    "https://example.com/" + t,
		}}
	}
	return items
}

// entry titles and summaries as the model actually phrased them for the prod
// run, where entry 7 carried Ubuntu's summary and entries 8-9 were absent.
func prodShapedEntries() []llmHighlight {
	return []llmHighlight{
		{Title: "Jev: Frontier Model Cheaper and Faster", Summary: "TypeSafe released Jev, a structured decision model."},
		{Title: "Wayback Machine Tightens Bot Protection", Summary: "The Internet Archive added stricter rate limiting."},
		{Title: "Gemini 3.8 Live & Extended Thinking", Summary: "Google added real-time voice and reasoning models."},
		{Title: "One-Month Linux GPU Driver for M4 Mac Mini", Summary: "A clean-room OpenGL driver for Apple's M4 chip."},
		{Title: "Driver's License Leak Becomes National-Security Threat", Summary: "A breach exposed 153 million licenses."},
		{Title: "GEFS on OpenBSD: Early Preview", Summary: "A crash-safe copy-on-write filesystem reaches OpenBSD."},
		{Title: "A/I Collective Shuts Down", Summary: "The privacy collective ceased operations."},
		// The prod reply put Ubuntu's summary here, in the slot for
		// "Small Programming Tricks", and omitted the last two articles.
		{Title: "Ubuntu 26.10 completes Rust Coreutils Migration", Summary: "cp, mv and rm were replaced by memory-safe Rust."},
	}
}

// TestAlignHighlightsProdShiftRegression is the guard for the prod incident: a
// reply whose entries are one short puts Ubuntu's summary in the position of
// "Small Programming Tricks" when pairing is positional. Alignment must instead
// leave "Small Programming Tricks" without a summary and never misattribute it.
func TestAlignHighlightsProdShiftRegression(t *testing.T) {
	articles := summaryArticles()
	assigned, missing := alignHighlights(articles, prodShapedEntries())

	assignedSummary := make(map[string]string)
	for articleIdx, entryIdx := range assigned {
		if entryIdx == unassigned {
			continue
		}
		assignedSummary[articles[articleIdx].Title] = prodShapedEntries()[entryIdx].Summary
	}

	ubuntu := "Ubuntu 26.10 completes transition to Rust-based coreutils"
	if got := assignedSummary[ubuntu]; !strings.Contains(got, "Rust") {
		t.Errorf("Ubuntu article summary = %q, want Ubuntu's own summary", got)
	}
	if got, ok := assignedSummary["Small Programming Tricks"]; ok {
		t.Errorf("Small Programming Tricks got summary %q, want none (the prod bug assigned Ubuntu's)", got)
	}
	if missing != 2 {
		t.Errorf("missing = %d, want 2 (Small Programming Tricks and the 4G hotspot article)", missing)
	}
}

// TestAlignHighlightsShuffledReply checks that a reply containing the right
// entries in the wrong order still lands every summary on its own article.
func TestAlignHighlightsShuffledReply(t *testing.T) {
	articles := summaryArticles()
	// One entry per article, each with a title that only matches its own
	// article. Kept in article order here, then reversed below.
	byArticle := []llmHighlight{
		{Title: "Jev: Frontier Model Cheaper and Faster", Summary: "about Jev"},
		{Title: "Wayback Machine Tightens Bot Protection", Summary: "about Wayback"},
		{Title: "Gemini 3.8 Live & Extended Thinking", Summary: "about Gemini"},
		{Title: "One-Month Linux GPU Driver for M4 Mac Mini", Summary: "about the GPU driver"},
		{Title: "Driver's License Leak Becomes National-Security Threat", Summary: "about the license leak"},
		{Title: "GEFS on OpenBSD: Early Preview", Summary: "about GEFS"},
		{Title: "A/I Collective Shuts Down", Summary: "about A/I"},
		{Title: "Small Programming Tricks Matter", Summary: "about small programming tricks"},
		{Title: "Turning a $20 4G Hotspot into a Texting Device", Summary: "about the hotspot"},
		{Title: "Ubuntu 26.10 completes Rust Coreutils Migration", Summary: "about Ubuntu"},
	}

	entries := make([]llmHighlight, len(byArticle))
	copy(entries, byArticle)
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	assigned, missing := alignHighlights(articles, entries)
	if missing != 0 {
		t.Fatalf("missing = %d, want 0: a complete reply must cover every article regardless of order", missing)
	}
	for articleIdx, entryIdx := range assigned {
		if entryIdx == unassigned {
			t.Errorf("article %q unassigned", articles[articleIdx].Title)
			continue
		}
		want := byArticle[articleIdx].Summary
		if got := entries[entryIdx].Summary; got != want {
			t.Errorf("article %q got summary %q, want %q",
				articles[articleIdx].Title, got, want)
		}
	}
}

// TestAlignHighlightsUsesArticleNumber checks the primary signal: a complete
// numbered reply is trusted outright, even when the titles are no help.
func TestAlignHighlightsUsesArticleNumber(t *testing.T) {
	articles := []digestItem{
		{Item: store.Item{ID: 1, Title: "First story"}},
		{Item: store.Item{ID: 2, Title: "Second story"}},
		{Item: store.Item{ID: 3, Title: "Third story"}},
	}
	entries := []llmHighlight{
		{Article: 3, Title: "ccc", Summary: "third"},
		{Article: 1, Title: "aaa", Summary: "first"},
		{Article: 2, Title: "bbb", Summary: "second"},
	}
	assigned, missing := alignHighlights(articles, entries)
	if missing != 0 {
		t.Fatalf("missing = %d, want 0", missing)
	}
	want := []string{"first", "second", "third"}
	for i, entryIdx := range assigned {
		if got := entries[entryIdx].Summary; got != want[i] {
			t.Errorf("article %d summary = %q, want %q", i, got, want[i])
		}
	}
}

// TestAlignHighlightsIgnoresCounterNumbering covers the measured failure that
// makes the numbering untrustworthy on its own: the model labels its entries
// 1..N like a counter while skipping articles. Honouring those numbers would
// shift every summary after the gap onto the wrong article.
func TestAlignHighlightsIgnoresCounterNumbering(t *testing.T) {
	articles := summaryArticles()
	// Real reply shape: 8 entries numbered 1..8 that actually cover articles
	// 1-5, 8, 9 and 10 (the model skipped "GEFS on OpenBSD" and "A/I Shuts
	// Down", and numbered the remainder sequentially).
	entries := []llmHighlight{
		{Article: 1, Title: "TypeSafe Launches Jev System One Model", Summary: "about Jev"},
		{Article: 2, Title: "Wayback Machine Faces Traffic Spikes", Summary: "about Wayback"},
		{Article: 3, Title: "Google Debuts Gemini 3.8 Live", Summary: "about Gemini"},
		{Article: 4, Title: "M4 Linux GPU Driver in a Month", Summary: "about the GPU driver"},
		{Article: 5, Title: "Critical IDScan Data Breach", Summary: "about the license breach"},
		{Article: 6, Title: "Small Programming Tricks Drive Productivity", Summary: "about small programming tricks"},
		{Article: 7, Title: "DIY Texting Device from Hotspot and Keyboard", Summary: "about the hotspot"},
		{Article: 8, Title: "Ubuntu 26.10 Completes Shift to Rust-Based Coreutils", Summary: "about Ubuntu"},
	}
	assigned, missing := alignHighlights(articles, entries)

	bySummary := make(map[string]string)
	for articleIdx, entryIdx := range assigned {
		if entryIdx != unassigned {
			bySummary[articles[articleIdx].Title] = entries[entryIdx].Summary
		}
	}
	want := map[string]string{
		"Jev: New frontier model 40-400x cheaper and 20-200x faster":        "about Jev",
		"An Update on Wayback Machine Access":                               "about Wayback",
		"Gemini 3.8 Live and 3.8 Live Extended Thinking":                    "about Gemini",
		"Building a Linux GPU Driver for the M4 Mac Mini in One Month":      "about the GPU driver",
		"America's Driver's License Breach Is a National Security Disaster": "about the license breach",
		"Small Programming Tricks":                                          "about small programming tricks",
		"Converting a $20 4G wireless hotspot into a texting device":        "about the hotspot",
		"Ubuntu 26.10 completes transition to Rust-based coreutils":         "about Ubuntu",
	}
	for title, wantSummary := range want {
		if got := bySummary[title]; got != wantSummary {
			t.Errorf("article %q summary = %q, want %q", title, got, wantSummary)
		}
	}
	if missing != 2 {
		t.Errorf("missing = %d, want 2 (GEFS and A/I, which the reply skipped)", missing)
	}
	// The counter numbering must not have been believed: article 6 is "GEFS on
	// OpenBSD", and claiming entry 6 (about Small Programming Tricks) for it
	// would be the shift this guards against.
	if _, ok := bySummary["GEFS on OpenBSD: A very early preview"]; ok {
		t.Error("GEFS article was assigned a summary from the counter-numbered reply")
	}
	if _, ok := bySummary["A/I Shuts Down"]; ok {
		t.Error("A/I article was assigned a summary from the counter-numbered reply")
	}
}

// TestAlignHighlightsPartialNumbering checks a reply that both numbers its
// entries and skips one. The incomplete numbering is not trusted, so the
// entries are placed by title and the skipped article stays unassigned.
func TestAlignHighlightsPartialNumbering(t *testing.T) {
	articles := []digestItem{
		{Item: store.Item{ID: 1, Title: "First story"}},
		{Item: store.Item{ID: 2, Title: "Second story"}},
		{Item: store.Item{ID: 3, Title: "Third story"}},
	}
	entries := []llmHighlight{
		{Article: 1, Title: "First story", Summary: "first"},
		// "Second story" is missing from the reply, so the numbering skips.
		{Article: 3, Title: "Third story", Summary: "third"},
	}
	assigned, missing := alignHighlights(articles, entries)
	if missing != 1 {
		t.Fatalf("missing = %d, want 1", missing)
	}
	if assigned[1] != unassigned {
		t.Errorf("article 1 (the skipped one) got entry %d, want unassigned", assigned[1])
	}
	if entries[assigned[0]].Summary != "first" {
		t.Errorf("article 0 summary = %q, want %q", entries[assigned[0]].Summary, "first")
	}
	if entries[assigned[2]].Summary != "third" {
		t.Errorf("article 2 summary = %q, want %q", entries[assigned[2]].Summary, "third")
	}
}

// TestAlignHighlightsSingleArticle covers the shape every existing fixture uses:
// one article, one entry, no article number.
func TestAlignHighlightsSingleArticle(t *testing.T) {
	articles := []digestItem{{Item: store.Item{ID: 1, Title: "A"}}}
	entries := []llmHighlight{{Title: "A", Summary: "hl A summary"}}
	assigned, missing := alignHighlights(articles, entries)
	if missing != 0 || assigned[0] != 0 {
		t.Errorf("assigned = %v missing = %d, want article 0 matched", assigned, missing)
	}
}

// TestAlignHighlightsNoGuessing checks that an entry matching nothing is dropped
// rather than attached to an arbitrary article.
func TestAlignHighlightsNoGuessing(t *testing.T) {
	articles := []digestItem{
		{Item: store.Item{ID: 1, Title: "First story"}},
		{Item: store.Item{ID: 2, Title: "Second story"}},
	}
	entries := []llmHighlight{
		{Title: "Completely unrelated topic about whales", Summary: "unrelated"},
		{Title: "Another unrelated item about pottery", Summary: "unrelated too"},
	}
	assigned, missing := alignHighlights(articles, entries)
	if missing != 2 {
		t.Errorf("missing = %d, want 2: unrelated entries must not be forced onto articles", missing)
	}
	for i, entryIdx := range assigned {
		if entryIdx != unassigned {
			t.Errorf("article %d matched unrelated entry %d (%q)", i, entryIdx, entries[entryIdx].Title)
		}
	}
}

// TestBuildContentShiftsNoSummariesOnShortReply drives the whole buildContent
// path with a reply that skips an article, and asserts no summary lands on the
// wrong one. The reply deliberately covers articles 0, 1 and 3 — leaving a hole
// at 2 — because a reply that merely stops early is still positionally correct
// for the entries it does carry, and would not catch the bug.
func TestBuildContentShiftsNoSummariesOnShortReply(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	articles := summaryArticles()[:4]
	reply, err := json.Marshal(map[string]any{
		"title":   "Prod Shape",
		"summary": "overall",
		"highlights": []llmHighlight{
			{Article: 1, Title: "Jev: Frontier Model Cheaper and Faster", Summary: "about Jev"},
			{Article: 2, Title: "Wayback Machine Tightens Bot Protection", Summary: "about Wayback"},
			// Article 3 ("Gemini 3.8 Live…") is missing from the reply.
			{Article: 4, Title: "One-Month Linux GPU Driver for M4 Mac Mini", Summary: "about the GPU driver"},
		},
	})
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}

	g := &Generator{queries: queries, llm: fakeLLM{resp: string(reply)}}
	content, err := g.buildContent(ctx, digestConfigJSON{}, articles, nil, slog.Default())
	if err != nil {
		t.Fatalf("buildContent: %v", err)
	}

	byTitle := make(map[string]string)
	for _, h := range content.Highlights {
		byTitle[h.Title] = h.LLMSummary
	}
	want := map[string]string{
		"Jev: New frontier model 40-400x cheaper and 20-200x faster":   "about Jev",
		"An Update on Wayback Machine Access":                          "about Wayback",
		"Building a Linux GPU Driver for the M4 Mac Mini in One Month": "about the GPU driver",
		"Gemini 3.8 Live and 3.8 Live Extended Thinking":               "",
	}
	for title, wantSummary := range want {
		if got := byTitle[title]; got != wantSummary {
			t.Errorf("article %q summary = %q, want %q", title, got, wantSummary)
		}
	}
	// Sections must carry the same alignment as the flat array.
	var sectioned int
	for _, sec := range content.Sections {
		for _, h := range sec.Highlights {
			sectioned++
			if h.LLMSummary != byTitle[h.Title] {
				t.Errorf("section summary for %q = %q, flat array has %q", h.Title, h.LLMSummary, byTitle[h.Title])
			}
		}
	}
	if sectioned != len(articles) {
		t.Errorf("sections carry %d highlights, want %d", sectioned, len(articles))
	}
}

// TestBuildContentRetriesIncompleteReply checks that a parsed but incomplete
// reply triggers exactly one corrective retry and that the retry's summaries are
// used when they cover more articles. A count mismatch needs no parse error to
// be worth retrying: it silently blanks highlights otherwise.
// The replies deliberately omit the "article" field, which is what the shipped
// prompt asks for (the numbering instruction was measured to make replies
// malformed more often, so it is not used).
func TestBuildContentRetriesIncompleteReply(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	articles := summaryArticles()[:3]
	short, _ := json.Marshal(map[string]any{
		"title": "Short", "summary": "overall",
		"highlights": []llmHighlight{{Title: "Jev", Summary: "only one"}},
	})
	full, _ := json.Marshal(map[string]any{
		"title": "Full", "summary": "overall",
		"highlights": []llmHighlight{
			{Title: "Jev: New frontier model 40-400x cheaper and 20-200x faster", Summary: "s1"},
			{Title: "An Update on Wayback Machine Access", Summary: "s2"},
			{Title: "Gemini 3.8 Live and 3.8 Live Extended Thinking", Summary: "s3"},
		},
	})

	llm := &scriptedLLM{replies: []string{string(short), string(full)}}
	g := &Generator{queries: queries, llm: llm}
	content, err := g.buildContent(ctx, digestConfigJSON{}, articles, nil, slog.Default())
	if err != nil {
		t.Fatalf("buildContent() error = %v, want the retry to recover", err)
	}
	if len(llm.prompts) != 2 {
		t.Fatalf("LLM called %d times, want 2 (attempt + retry)", len(llm.prompts))
	}
	if !strings.Contains(llm.prompts[1], "1 highlight entries") || !strings.Contains(llm.prompts[1], "3 articles") {
		t.Errorf("retry prompt did not name the coverage gap:\n%s", llm.prompts[1])
	}
	if content.Title != "Full" {
		t.Errorf("title = %q, want the retry's title", content.Title)
	}
	for i, want := range []string{"s1", "s2", "s3"} {
		if got := content.Highlights[i].LLMSummary; got != want {
			t.Errorf("highlight %d summary = %q, want %q", i, got, want)
		}
	}
}

// TestBuildContentIncompleteAfterRetryStillAligns checks the best-effort path: a
// retry that is also incomplete must not misattribute, and must not error out.
func TestBuildContentIncompleteAfterRetryStillAligns(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	articles := summaryArticles()[:4]
	// First reply covers two articles, retry covers three — still one short.
	first, _ := json.Marshal(map[string]any{
		"title": "First", "summary": "overall",
		"highlights": []llmHighlight{
			{Title: "Jev: New frontier model cheaper", Summary: "s1"},
			{Title: "Wayback Machine Access Update", Summary: "s2"},
		},
	})
	second, _ := json.Marshal(map[string]any{
		"title": "Second", "summary": "overall",
		"highlights": []llmHighlight{
			{Title: "Jev: New frontier model cheaper", Summary: "s1"},
			{Title: "Wayback Machine Access Update", Summary: "s2"},
			{Title: "Gemini 3.8 Live and Extended Thinking", Summary: "s3"},
		},
	})

	llm := &scriptedLLM{replies: []string{string(first), string(second)}}
	g := &Generator{queries: queries, llm: llm}
	content, err := g.buildContent(ctx, digestConfigJSON{}, articles, nil, slog.Default())
	if err != nil {
		t.Fatalf("buildContent() error = %v, want best-effort success", err)
	}
	if len(llm.prompts) != 2 {
		t.Errorf("LLM called %d times, want exactly 2 (a single retry, not a loop)", len(llm.prompts))
	}
	// The retry covered more, so its reply is kept.
	if content.Title != "Second" {
		t.Errorf("title = %q, want the more complete retry's title", content.Title)
	}
	want := []string{"s1", "s2", "s3", ""}
	for i, w := range want {
		if got := content.Highlights[i].LLMSummary; got != w {
			t.Errorf("highlight %d (%q) summary = %q, want %q",
				i, content.Highlights[i].Title, got, w)
		}
	}
}

// TestBuildContentKeepsFirstReplyWhenRetryUnparseable checks the mixed case: the
// first reply parsed but was short, and the retry is garbage. The usable first
// reply must survive instead of the whole digest failing.
func TestBuildContentKeepsFirstReplyWhenRetryUnparseable(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	articles := summaryArticles()[:2]
	first, _ := json.Marshal(map[string]any{
		"title": "Usable", "summary": "overall",
		"highlights": []llmHighlight{{Title: "Jev: New frontier model cheaper", Summary: "s1"}},
	})

	llm := &scriptedLLM{replies: []string{string(first), corruptedDigestReply}}
	g := &Generator{queries: queries, llm: llm}
	content, err := g.buildContent(ctx, digestConfigJSON{}, articles, nil, slog.Default())
	if err != nil {
		t.Fatalf("buildContent() error = %v, want the usable first reply to be kept", err)
	}
	if len(llm.prompts) != 2 {
		t.Errorf("LLM called %d times, want 2", len(llm.prompts))
	}
	if content.Title != "Usable" {
		t.Errorf("title = %q, want the first reply's title", content.Title)
	}
	if got := content.Highlights[0].LLMSummary; got != "s1" {
		t.Errorf("highlight 0 summary = %q, want %q", got, "s1")
	}
	if got := content.Highlights[1].LLMSummary; got != "" {
		t.Errorf("highlight 1 summary = %q, want empty", got)
	}
}
