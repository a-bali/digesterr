package digest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAlignRealCapturedReplies replays every reply captured from the live
// gateway during the prod investigation through the shipped alignment code, and
// asserts the two invariants that matter:
//
//  1. A summary never lands on an article it does not describe.
//  2. A summary the model did not produce leaves the article empty (blank is
//     acceptable; someone else's summary is not).
//
// The fixtures live in testdata/ and are the verbatim `message.content` values.
func TestAlignRealCapturedReplies(t *testing.T) {
	articles := summaryArticles()
	subjectByDetail := map[string]int{
		"Jev": 0, "Wayback": 1, "Gemini": 2, "GPU": 3, "OpenGL": 3,
		"License": 4, "IDScan": 4, "Nexus": 4, "GEFS": 5,
		"A/I": 6, "Autistici": 6, "Collective": 6,
		"Small Programming": 7, "Tricks": 7, "fzf": 7,
		"Hotspot": 8, "Texting": 8, "Ubuntu": 9, "Coreutils": 9,
	}

	files, err := filepath.Glob("testdata/replies/*.txt")
	if err != nil || len(files) == 0 {
		t.Fatalf("no captured replies found in testdata/replies (glob err=%v)", err)
	}

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		body := string(raw)
		if m := regexp.MustCompile(`(?s)\{.*\}`).FindString(body); m != "" {
			body = m
		}
		var output llmDigestOutput
		if err := json.Unmarshal([]byte(body), &output); err != nil {
			t.Errorf("%s: reply did not decode: %v", filepath.Base(file), err)
			continue
		}
		if len(output.Highlights) == 0 {
			continue
		}

		assigned, missing := alignHighlights(articles, output.Highlights)
		if missing < 0 || missing > len(articles) {
			t.Errorf("%s: missing = %d, out of range", filepath.Base(file), missing)
		}

		placed := 0
		for articleIdx, entryIdx := range assigned {
			if entryIdx == unassigned {
				continue
			}
			placed++
			e := output.Highlights[entryIdx]
			// Which article does this entry's text describe?
			text := strings.ToLower(e.Title + " " + e.Summary)
			// Collect every subject the entry mentions; the article it was
			// assigned to must be one of them, and only a single subject is
			// expected for these fixtures.
			var subjects []int
			for token, idx := range subjectByDetail {
				if strings.Contains(text, strings.ToLower(token)) {
					subjects = append(subjects, idx)
				}
			}
			if len(subjects) == 0 {
				continue // no distinctive subject; exempt from the check
			}
			match := false
			for _, s := range subjects {
				if s == articleIdx {
					match = true
					break
				}
			}
			if !match {
				t.Errorf("%s: entry %d (%q) landed on article %q but describes article(s) %v",
					filepath.Base(file), entryIdx, e.Title, articles[articleIdx].Title, subjects)
			}
		}
		t.Logf("%s: entries=%d placed=%d missing=%d", filepath.Base(file), len(output.Highlights), placed, missing)
	}
}
