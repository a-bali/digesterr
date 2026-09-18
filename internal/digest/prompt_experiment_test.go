package digest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// promptExperiment drives candidate prompts against the live gateway and
// reports malformed / incomplete / misaligned rates. Manual experiment
// runner, not a CI gate: skipped unless DIGESTERR_PROMPT_EXP=1 plus the
// usual LLM_ENDPOINT / LLM_API_KEY.
//
// Run with:
//
//	DIGESTERR_PROMPT_EXP=1 LLM_ENDPOINT=... LLM_API_KEY=... LLM_MODEL=virtual-free \
//	  go test ./internal/digest -run TestPromptExperiment -v
//
// Results are historical record, not setup instructions:
//
//	2026-09-18, virtual-free, 10 articles with ~4KB bodies each, 3 trials
//	per variant: baseline (title-only) 2 ok / 1 incomplete; url-key 2 ok /
//	1 malformed ("User Safety: safe" style guard refusal); article-number
//	1 ok / 2 malformed ({"article":"1"} string numbers — since tolerated by
//	llmHighlight.UnmarshalJSON); url-plus-number 2 ok / 1 malformed (guard
//	refusal). Conclusion: requesting article number + exact URL echo costs
//	no JSON-shape penalty vs baseline; the residual failure mode is the
//	free-tier guard refusing the long prompt, unrelated to the join keys.
func TestPromptExperiment(t *testing.T) {
	if os.Getenv("DIGESTERR_PROMPT_EXP") == "" {
		t.Skip("set DIGESTERR_PROMPT_EXP=1 to run the live prompt experiment")
	}
	endpoint := os.Getenv("LLM_ENDPOINT")
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL")
	if endpoint == "" || apiKey == "" {
		t.Skip("LLM env vars not set")
	}
	if model == "" {
		model = "virtual-free"
	}

	client := NewLLMClient(endpoint, apiKey, model)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	variants := []struct {
		name   string
		prompt func(articles []digestItem) (system, user string)
	}{
		{"baseline-title", baselinePrompt},
		{"url-key", urlKeyPrompt},
		{"article-number", articleNumberPrompt},
		{"url-plus-number", urlPlusNumberPrompt},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			trials := 3
			stats := map[string]int{"ok": 0, "malformed": 0, "incomplete": 0, "misaligned": 0}
			for i := 0; i < trials; i++ {
				articles := summaryArticles()
				for j := range articles {
					// Realistic body: ~4000 chars like a real digest prompt
					// (maxContentForLLM), so truncation/omission pressure
					// matches production.
					articles[j].Content = "Body text for " + articles[j].Title + ". " +
						strings.Repeat("This is filler detail sentence about the topic at hand. ", 70)
					slug := strings.ToLower(strings.ReplaceAll(articles[j].Title, " ", "-"))
					slug = strings.ReplaceAll(slug, "/", "-")
					if len(slug) > 20 {
						slug = slug[:20]
					}
					articles[j].Url = fmt.Sprintf("https://example.com/articles/%d-%s", 100+j, slug)
				}
				system, user := v.prompt(articles)
				t.Logf("trial %d: system=%d bytes user=%d bytes", i, len(system), len(user))
				resp, err := client.Chat(ctx, system, user)
				if err != nil {
					t.Logf("trial %d: chat error: %v", i, err)
					stats["malformed"]++
					continue
				}
				saveReply(t, v.name, i, resp)
				output, err := decodeDigestOutput(resp)
				if err != nil {
					t.Logf("trial %d: MALFORMED: %v\nreply excerpt: %.500s", i, err, resp)
					stats["malformed"]++
					continue
				}
				if !digestReplyComplete(articles, output.Highlights) {
					_, missing := alignHighlights(articles, output.Highlights)
					t.Logf("trial %d: INCOMPLETE: articles=%d entries=%d missing=%d", i, len(articles), len(output.Highlights), missing)
					stats["incomplete"]++
					continue
				}
				mis := checkMisaligned(t, articles, output.Highlights)
				if mis > 0 {
					stats["misaligned"]++
					continue
				}
				stats["ok"]++
			}
			t.Logf("VARIANT %s over %d trials: %+v", v.name, trials, stats)
		})
	}
}

// saveReply persists a live reply for offline fixture analysis. Replies go
// to /tmp (never the repo: captured model output is measurement data, and
// testdata/replies/ holds hand-picked regression cases, not bulk captures).
func saveReply(t *testing.T, variant string, trial int, resp string) {
	t.Helper()
	path := fmt.Sprintf("/tmp/digesterr-exp-%s-t%d.txt", variant, trial)
	if err := os.WriteFile(path, []byte(resp), 0o644); err != nil {
		t.Logf("save reply: %v", err)
		return
	}
	t.Logf("trial %d: saved %d bytes to %s", trial, len(resp), path)
}

// checkMisaligned verifies each aligned summary belongs to its article
// (subject-keyword check, same shape as the live gateway test).
func checkMisaligned(t *testing.T, articles []digestItem, entries []llmHighlight) int {
	t.Helper()
	subject := []string{"Jev", "Wayback", "Gemini", "GPU", "License", "GEFS", "A/I", "Tricks", "Hotspot", "Ubuntu"}
	lower := func(s string) string { return strings.ToLower(s) }
	assigned, _ := alignHighlights(articles, entries)
	mis := 0
	for i, entryIdx := range assigned {
		if entryIdx == unassigned {
			continue
		}
		ownText := lower(entries[entryIdx].Title + " " + entries[entryIdx].Summary)
		ownHits := strings.Count(ownText, lower(subject[i]))
		for j, other := range subject {
			if j == i {
				continue
			}
			if strings.Count(ownText, lower(other)) >= 1 && ownHits == 0 {
				t.Logf("article %d %q carries summary about %q: %q", i, articles[i].Title, articles[j].Title, entries[entryIdx].Summary)
				mis++
				break
			}
		}
	}
	return mis
}

// buildExperimentArticles mirrors the generator's article rendering: the
// prompt under test gets the same per-article fields the shipped prompt
// sends (title, url, points, comments, content, top comments).
func buildExperimentArticles(articles []digestItem, header func(i int) string) string {
	var sb strings.Builder
	sb.WriteString("Here are the articles to summarize:\n\n")
	for i, h := range articles {
		sb.WriteString(header(i))
		fmt.Fprintf(&sb, "Title: %s\n", h.Title)
		fmt.Fprintf(&sb, "URL: %s\n", h.Url)
		content := h.Content
		if content == "" {
			content = "Body text for " + h.Title
		}
		fmt.Fprintf(&sb, "Content: %s\n", CleanContentForLLM(content))
		sb.WriteString("\n")
	}
	return sb.String()
}

var experimentSystem = "For each article, provide a 2-3 sentence highlight with the key insight and why it matters. Also give the whole digest a short title of at most 8-10 words, catchy, optionally with a light pun."

func baselinePrompt(articles []digestItem) (string, string) {
	user := buildExperimentArticles(articles, func(i int) string {
		return fmt.Sprintf("Article %d (HIGHLIGHT):\n", i+1)
	})
	user += "For each HIGHLIGHT article, provide a 2-3 sentence highlight capturing the key insight and why it matters. " +
		"Format your response as JSON with keys \"title\" (string, a short title for the whole digest, at most 8-10 words, catchy and derived from the digest contents), " +
		"\"summary\" (string of overall digest summary) and \"highlights\" (array of {title, summary}). " + jsonOutputRule
	return experimentSystem, user
}

func urlKeyPrompt(articles []digestItem) (string, string) {
	user := buildExperimentArticles(articles, func(i int) string {
		return fmt.Sprintf("Article %d (HIGHLIGHT):\n", i+1)
	})
	user += "For each HIGHLIGHT article, provide a 2-3 sentence highlight capturing the key insight and why it matters. " +
		"Format your response as JSON with keys \"title\" (string, a short title for the whole digest, at most 8-10 words, catchy and derived from the digest contents), " +
		"\"summary\" (string of overall digest summary) and \"highlights\" (array of {url, title, summary}). " +
		"Copy the \"url\" of each article EXACTLY as given above into its entry — the url is how entries are matched to articles. " +
		"Reply with exactly one entry per article. " + jsonOutputRule
	return experimentSystem, user
}

func articleNumberPrompt(articles []digestItem) (string, string) {
	user := buildExperimentArticles(articles, func(i int) string {
		return fmt.Sprintf("Article %d (HIGHLIGHT):\n", i+1)
	})
	user += "For each HIGHLIGHT article, provide a 2-3 sentence highlight capturing the key insight and why it matters. " +
		"Format your response as JSON with keys \"title\" (string, a short title for the whole digest, at most 8-10 words, catchy and derived from the digest contents), " +
		"\"summary\" (string of overall digest summary) and \"highlights\" (array of {article, title, summary}). " +
		"Set \"article\" to the article's number (1.." + fmt.Sprintf("%d", len(articles)) + ") exactly as shown above. " +
		"Reply with exactly one entry per article. " + jsonOutputRule
	return experimentSystem, user
}

func urlPlusNumberPrompt(articles []digestItem) (string, string) {
	user := buildExperimentArticles(articles, func(i int) string {
		return fmt.Sprintf("Article %d (HIGHLIGHT):\n", i+1)
	})
	user += "For each HIGHLIGHT article, provide a 2-3 sentence highlight capturing the key insight and why it matters. " +
		"Format your response as JSON with keys \"title\" (string, a short title for the whole digest, at most 8-10 words, catchy and derived from the digest contents), " +
		"\"summary\" (string of overall digest summary) and \"highlights\" (array of {article, url, title, summary}). " +
		"Set \"article\" to the article's number (1.." + fmt.Sprintf("%d", len(articles)) + ") and copy its \"url\" EXACTLY as given above. " +
		"Reply with exactly one entry per article. " + jsonOutputRule
	return experimentSystem, user
}

// TestPromptExperimentFixtures is the offline companion: it feeds canned
// paraphrase-heavy replies (the shape virtual-free actually returns) through
// decode + the candidate alignment keys, without touching the network.
func TestPromptExperimentFixtures(t *testing.T) {
	articles := summaryArticles()
	replies := []struct {
		name  string
		reply string
	}{
		{"paraphrased-titles", `{"title":"T","summary":"s","highlights":[
			{"title":"Jev frontier model goes cheap and fast","summary":"about Jev"},
			{"title":"Wayback Machine tightens bot protection","summary":"about Wayback"},
			{"title":"Gemini 3.8 live and extended thinking","summary":"about Gemini"},
			{"title":"One-month Linux GPU driver for M4 Mac Mini","summary":"about the GPU driver"},
			{"title":"Driver license leak becomes national security threat","summary":"about the license leak"},
			{"title":"GEFS on OpenBSD early preview","summary":"about GEFS"},
			{"title":"A/I collective shuts down","summary":"about A/I"},
			{"title":"Small programming tricks matter","summary":"about small programming tricks"},
			{"title":"Turning a $20 4G hotspot into a texting device","summary":"about the hotspot"},
			{"title":"Ubuntu 26.10 completes Rust coreutils migration","summary":"about Ubuntu"}]}`},
		{"one-missing", `{"title":"T","summary":"s","highlights":[
			{"title":"Jev frontier model","summary":"about Jev"},
			{"title":"Wayback bot protection","summary":"about Wayback"},
			{"title":"Gemini 3.8 live","summary":"about Gemini"},
			{"title":"GPU driver for M4","summary":"about the GPU driver"},
			{"title":"License leak","summary":"about the license leak"},
			{"title":"GEFS preview","summary":"about GEFS"},
			{"title":"A/I shuts down","summary":"about A/I"},
			{"title":"Small programming tricks","summary":"about tricks"},
			{"title":"Ubuntu coreutils","summary":"about Ubuntu"}]}`},
	}
	for _, r := range replies {
		output, err := decodeDigestOutput(r.reply)
		if err != nil {
			t.Fatalf("%s: decode: %v", r.name, err)
		}
		_, missing := alignHighlights(articles, output.Highlights)
		t.Logf("%s: entries=%d missing=%d", r.name, len(output.Highlights), missing)
	}
}
