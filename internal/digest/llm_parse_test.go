package digest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// corruptedDigestReply is the reply shape that broke prod digest generation:
// the model emitted the bare word "unrelated" where separators belong, which
// json.Unmarshal rejects with
// "invalid character 'u' looking for beginning of object key string".
const corruptedDigestReply = "```json\n{\n  \"title\": \"Pacing the Frontier\",\n" +
	"  \"summary\": \"overall summary\",\n" +
	"  \"highlights\": [\n    {\n      \"title\": \"Build profiler\", unrelated\n" +
	"      \"summary\": \"Lalit Kumar developed 'buildprof'.\" unrelated\n    }\n  ]\n}\n```"

const fencedDigestReply = "```json\n{\"title\":\"Reading Rainbow\",\"summary\":\"overall\"," +
	"\"highlights\":[{\"title\":\"A\",\"summary\":\"hl A summary\"}]}\n```"

// scriptedLLM replays a fixed sequence of replies and records the prompts, so a
// test can tell a retry from the first attempt.
type scriptedLLM struct {
	replies []string
	prompts []string
}

func (s *scriptedLLM) Chat(_ context.Context, _, userPrompt string) (string, error) {
	s.prompts = append(s.prompts, userPrompt)
	if len(s.replies) == 0 {
		return "", errors.New("scriptedLLM: no replies left")
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return reply, nil
}

func digestTestHighlights() []digestItem {
	return []digestItem{{
		Item: store.Item{ID: 1, FeedID: 7, Title: "A", Url: "https://a.example.com", Points: 100, CommentsCount: 3},
	}}
}

func TestDecodeDigestOutputShapes(t *testing.T) {
	valid := `{"title":"T","summary":"S","highlights":[{"title":"A","summary":"hl"}]}`
	tests := []struct {
		name        string
		reply       string
		wantTitle   string
		wantSummary string
		wantErr     bool
	}{
		{name: "bare object", reply: valid},
		{name: "fenced", reply: "```json\n" + valid + "\n```"},
		{name: "fence with CRLF", reply: "```json\r\n" + valid + "\r\n```"},
		{name: "prose around object", reply: "Here is the digest:\n" + valid + "\nHope that helps!"},
		{name: "double encoded", reply: `"{\"title\":\"T\",\"summary\":\"S\",\"highlights\":[{\"title\":\"A\",\"summary\":\"hl\"}]}"`},
		{
			// Prose forces the scan to run, and the braces inside the title
			// string are what a non-string-aware scan would stop on.
			name:      "braces inside strings",
			reply:     "Sure:\n" + `{"title":"T { not } a stop","summary":"S","highlights":[{"title":"A","summary":"hl"}]}` + "\ndone",
			wantTitle: "T { not } a stop",
		},
		{name: "empty reply", reply: "", wantErr: true},
		{name: "prose only", reply: "I could not summarize these articles.", wantErr: true},
		{name: "corrupted separators", reply: corruptedDigestReply, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantTitle, wantSummary := tt.wantTitle, tt.wantSummary
			if wantTitle == "" {
				wantTitle = "T"
			}
			if wantSummary == "" {
				wantSummary = "S"
			}
			output, err := decodeDigestOutput(tt.reply)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("decodeDigestOutput(%q) = %+v, want error", tt.reply, output)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeDigestOutput(%q) error = %v", tt.reply, err)
			}
			if output.Title != wantTitle || output.Summary != wantSummary {
				t.Errorf("title/summary = %q/%q, want %q/%q", output.Title, output.Summary, wantTitle, wantSummary)
			}
			if len(output.Highlights) != 1 || output.Highlights[0].Summary != "hl" {
				t.Errorf("highlights = %+v, want one highlight with summary %q", output.Highlights, "hl")
			}
		})
	}
}

// TestGenerateRecoversFromMalformedLLMReply runs the whole generation path
// against a real HTTP LLM endpoint that returns the prod-corrupted reply once,
// then a usable one. A single glitched token must not lose the digest.
func TestGenerateRecoversFromMalformedLLMReply(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "digest-retry@example.com",
		PasswordHash: "hash",
		ApiKey:       "key-digest-retry",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Retry Feed",
		Url:    "https://example.com/retry.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	for i, title := range []string{"Go 1.25 Released", "Rust Gets Faster"} {
		if _, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID:        feedObj.ID,
			Hash:          "retry-h" + strconv.Itoa(i),
			Title:         title,
			Url:           "https://example.com/" + strconv.Itoa(i),
			Content:       "Content for " + title,
			Points:        int64(100 - i*10),
			CommentsCount: 5,
			Status:        "unread",
		}); err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
	}

	replies := []string{corruptedDigestReply, fencedDigestReply}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[0]
		if calls < len(replies) {
			reply = replies[calls]
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": reply}}},
		})
	}))
	defer srv.Close()

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Retry Digest",
		Schedule: "0 7 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	gen := NewGenerator(db, queries, NewLLMClient(srv.URL, "test-key", "test-model"), feed.NewPoller(queries))
	result, err := gen.Generate(ctx, cfg)
	if err != nil {
		t.Fatalf("Generate() error = %v, want the retry to recover", err)
	}
	if calls != 2 {
		t.Errorf("LLM endpoint received %d calls, want 2 (attempt + retry)", calls)
	}

	var stored struct {
		Title      string `json:"title"`
		Highlights []struct {
			LLMSummary string `json:"llm_summary"`
		} `json:"highlights"`
	}
	if err := json.Unmarshal([]byte(result.Content), &stored); err != nil {
		t.Fatalf("parsing stored digest content: %v", err)
	}
	if stored.Title != "Reading Rainbow" {
		t.Errorf("stored digest title = %q, want %q", stored.Title, "Reading Rainbow")
	}
	if len(stored.Highlights) != 1 || stored.Highlights[0].LLMSummary != "hl A summary" {
		t.Errorf("stored highlights = %+v, want the retry's summary", stored.Highlights)
	}
}

// TestBuildContentRetriesMalformedLLMJSON guards the prod incident: one stray
// token in the reply must not cost the whole digest. The corrective retry has
// to quote the parse error back to the model, and a good second reply must be
// used.
func TestBuildContentRetriesMalformedLLMJSON(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	llm := &scriptedLLM{replies: []string{corruptedDigestReply, fencedDigestReply}}
	g := &Generator{queries: queries, llm: llm}

	content, err := g.buildContent(ctx, digestConfigJSON{}, digestTestHighlights(), nil, slog.Default())
	if err != nil {
		t.Fatalf("buildContent() error = %v, want the retry to recover", err)
	}
	if content.Title != "Reading Rainbow" {
		t.Errorf("title = %q, want %q", content.Title, "Reading Rainbow")
	}
	if len(content.Highlights) != 1 || content.Highlights[0].LLMSummary != "hl A summary" {
		t.Errorf("highlights = %+v, want the retry's summary", content.Highlights)
	}
	if len(llm.prompts) != 2 {
		t.Fatalf("LLM called %d times, want 2 (one attempt + one retry)", len(llm.prompts))
	}
	if !strings.Contains(llm.prompts[1], "could not be parsed as JSON") {
		t.Errorf("retry prompt did not quote the parse failure:\n%s", llm.prompts[1])
	}
	if !strings.Contains(llm.prompts[1], "invalid character 'u'") {
		t.Errorf("retry prompt did not carry the parse error:\n%s", llm.prompts[1])
	}
}

// TestBuildContentFailsLoudlyWithReplyExcerpt checks the final failure path: one
// retry, then an error that carries the offending reply so the failure is
// diagnosable without going to the gateway logs.
func TestBuildContentFailsLoudlyWithReplyExcerpt(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	llm := &scriptedLLM{replies: []string{corruptedDigestReply, corruptedDigestReply}}
	g := &Generator{queries: queries, llm: llm}

	_, err := g.buildContent(ctx, digestConfigJSON{}, digestTestHighlights(), nil, slog.Default())
	if err == nil {
		t.Fatal("buildContent() error = nil, want a parse failure")
	}
	if len(llm.prompts) != 2 {
		t.Errorf("LLM called %d times, want exactly 2 (a single retry, not a loop)", len(llm.prompts))
	}
	if !strings.Contains(err.Error(), "parsing LLM response") {
		t.Errorf("error = %q, want it to mention parsing the LLM response", err)
	}
	if !strings.Contains(err.Error(), "invalid character 'u'") {
		t.Errorf("error = %q, want the underlying parse error", err)
	}
	if !strings.Contains(err.Error(), "unrelated") {
		t.Errorf("error = %q, want an excerpt of the reply that failed", err)
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("error = %q, want the excerpt collapsed onto one line", err)
	}
}
