package feed

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustPayload(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tweetResult(id, screen, fullText string, urls [][2]string, quoted any) map[string]any {
	urlEnts := make([]map[string]string, 0, len(urls))
	for _, u := range urls {
		urlEnts = append(urlEnts, map[string]string{"url": u[0], "expanded_url": u[1]})
	}
	res := map[string]any{
		"__typename": "Tweet",
		"rest_id":    id,
		"core": map[string]any{
			"user_results": map[string]any{
				"result": map[string]any{
					"legacy": map[string]any{"screen_name": screen},
				},
			},
		},
		"legacy": map[string]any{
			"full_text": fullText,
			"entities":  map[string]any{"urls": urlEnts},
		},
	}
	if quoted != nil {
		res["quoted_status_result"] = quoted
	}
	return res
}

func entry(id string, res map[string]any) map[string]any {
	return map[string]any{
		"entryId": "tweet-" + id,
		"content": map[string]any{
			"entryType":   "TimelineTimelineItem",
			"itemContent": map[string]any{"tweet_results": map[string]any{"result": res}},
		},
	}
}

// Quote tweets whose only URL entity points at the quoted status must not be
// folded away as link-only thread continuations — the quote text is the
// content (regression: OpenRouter's Ox Alpha community QT was dropped).
func TestQuoteTweetWithStatusLinkNotFolded(t *testing.T) {
	qt := tweetResult("100", "openrouter",
		"Ox Alpha discussion is booming, see what people say",
		[][2]string{{"https://t.co/def", "https://x.com/jjacky/status/999?s=20"}},
		map[string]any{"result": tweetResult("999", "jjacky", "ox alpha has been live for 5 days", nil, nil)})
	plain := tweetResult("555", "openrouter", "a normal tweet with https://t.co/link",
		[][2]string{{"https://t.co/link", "https://openrouter.ai"}}, nil)

	body := mustPayload(t, map[string]any{
		"data": map[string]any{"user": map[string]any{"result": map[string]any{
			"core": map[string]any{"user_results": map[string]any{"result": map[string]any{
				"legacy": map[string]any{"screen_name": "openrouter"}}}},
			"timeline_v2": map[string]any{"timeline": map[string]any{"instructions": []any{
				map[string]any{"type": "TimelineAddEntries", "entries": []any{entry("100", qt), entry("555", plain)}},
			}}},
		}}},
	})
	tweets, err := parseUserTweets(body)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tw := range tweets {
		if tw.RestID == "100" {
			found = true
			if !strings.Contains(tw.displayText(false), "Ox Alpha discussion") {
				t.Errorf("quote text lost: %q", tw.displayText(false))
			}
		}
	}
	if !found {
		t.Errorf("quote tweet 100 dropped; got %d tweets", len(tweets))
	}
}

// A genuine thread continuation (no quote) keeps folding into its target.
func TestLinkOnlyThreadContinuationStillFolds(t *testing.T) {
	cont := tweetResult("333", "openrouter", "more detail below",
		[][2]string{{"https://t.co/x", "https://x.com/openrouter/status/444"}}, nil)
	target := tweetResult("444", "openrouter", "the main announcement tweet", nil, nil)

	body := mustPayload(t, map[string]any{
		"data": map[string]any{"user": map[string]any{"result": map[string]any{
			"core": map[string]any{"user_results": map[string]any{"result": map[string]any{
				"legacy": map[string]any{"screen_name": "openrouter"}}}},
			"timeline_v2": map[string]any{"timeline": map[string]any{"instructions": []any{
				map[string]any{"type": "TimelineAddEntries", "entries": []any{entry("333", cont), entry("444", target)}},
			}}},
		}}},
	})
	tweets, err := parseUserTweets(body)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]bool{}
	for _, tw := range tweets {
		counts[tw.RestID] = true
	}
	if counts["333"] {
		t.Error("link-only continuation should fold into 444")
	}
	if !counts["444"] {
		t.Error("target 444 missing")
	}
}
