package digest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// llmHighlight is one entry of the model's "highlights" array.
//
// Article is the 1-based article number and URL is the article's exact URL,
// both echoed back by the model. The shipped prompt requests both: the
// article number is a compact deterministic key, the URL is immune to title
// paraphrase. Either one alone can misfire (a model may emit a bare 1..N
// counter while skipping articles, or mistype a URL), so alignment trusts
// each only when it is unambiguous and falls back to title matching
// otherwise. Alignment never depends on reply position.
//
// Article accepts a JSON number or a number-as-string: virtual-free was
// measured emitting {"article":"1"} (string), which encoding/json otherwise
// rejects with "cannot unmarshal string into int" and costs the whole run.
type llmHighlight struct {
	Article int    `json:"article"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	URL     string `json:"url"`
}

// UnmarshalJSON decodes an highlight entry, tolerating the article number
// arriving as a JSON string ("1") instead of a number (1).
func (h *llmHighlight) UnmarshalJSON(data []byte) error {
	var aux struct {
		Article json.RawMessage `json:"article"`
		Title   string          `json:"title"`
		Summary string          `json:"summary"`
		URL     string          `json:"url"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	h.Title = aux.Title
	h.Summary = aux.Summary
	h.URL = aux.URL
	if len(aux.Article) == 0 {
		return nil
	}
	if err := json.Unmarshal(aux.Article, &h.Article); err == nil {
		return nil
	}
	var s string
	if err := json.Unmarshal(aux.Article, &s); err != nil {
		return fmt.Errorf("article: %w", err)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("article %q is not a number", s)
	}
	h.Article = n
	return nil
}

// unassigned marks an article that no reply entry was matched to. Such an
// article renders without a summary.
const unassigned = -1

// alignmentMinTokenLen drops noise tokens ("a", "to", "of") from title
// matching. Two titles sharing only short words are not evidence of anything.
const alignmentMinTokenLen = 3

// weightTokens lowercases s and returns each token long enough to be
// meaningful, mapped to its rune length. Longer tokens weigh more because they
// carry more of the title's identity ("coreutils" over "rust").
func weightTokens(s string) map[string]int {
	weights := make(map[string]int)
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, f := range fields {
		n := len([]rune(f))
		if n < alignmentMinTokenLen {
			continue
		}
		if n > weights[f] {
			weights[f] = n
		}
	}
	return weights
}

// tokenCoverage reports how much of the query's token weight appears in the
// document: shared weight / total query weight.
func tokenCoverage(query, document map[string]int) float64 {
	total, shared := 0, 0
	for token, weight := range query {
		total += weight
		if _, ok := document[token]; ok {
			shared += weight
		}
	}
	if total == 0 {
		return 0
	}
	return float64(shared) / float64(total)
}

// alignHighlights maps each reply entry to the highlight article it describes.
// The returned slice is indexed by article and holds the matching entry index,
// or unassigned. The second result is how many articles were left without a
// summary.
//
// Positional pairing is NOT used. A reply that omits or reorders entries shifts
// every later summary onto the wrong article (prod 2026-09-16: "Small
// Programming Tricks" rendered with the summary of "Ubuntu 26.10 completes
// transition to Rust-based coreutils", while the last two articles rendered
// blank). Matching is evidence-based and anything ambiguous stays unassigned, so
// an article renders with no summary rather than with someone else's.
//
// Keys are applied strongest-first:
//
//  1. Article number, when it forms a complete permutation of the articles.
//     A bare 1..N counter emitted while skipping articles is the known
//     misfire (a measured reply numbered 1..8 while actually covering
//     articles 1-5, 8, 9 and 10), so only collision-free numbering is trusted.
//  2. Exact URL echo: the prompt shows each article's URL and asks for it back
//     verbatim. URLs are immune to title paraphrase, but only an exact match
//     against a known article URL counts — a mistyped URL is dropped, never
//     guessed at.
//  3. Title matching (mutual-best): an entry is anchored to an article only when
//     they are each other's strongest remaining match. That needs no tuned
//     similarity threshold — a threshold must be low enough to accept a paraphrase
//     ("A/I Shuts Down" vs the model's "Autistici/Inventati (A/I) Announces
//     Shutdown") yet high enough to reject the runner-up, and measured against real
//     replies those two requirements conflict.
func alignHighlights(articles []digestItem, entries []llmHighlight) ([]int, int) {
	assigned := make([]int, len(articles))
	for i := range assigned {
		assigned[i] = unassigned
	}
	entryUsed := make([]bool, len(entries))
	take := func(articleIdx, entryIdx int) {
		assigned[articleIdx] = entryIdx
		entryUsed[entryIdx] = true
	}

	// Pass 1: the entry's own article number — but only when the numbering is a
	// complete permutation of the articles. Models often emit a plain 1..N
	// counter while skipping articles: a measured reply numbered 1..8 while
	// actually covering articles 1-5, 8, 9 and 10, so honouring those numbers
	// would land three summaries on the wrong articles — precisely the bug this
	// function exists to prevent. A complete permutation cannot collide, so it
	// is the only numbering worth trusting.
	if len(entries) == len(articles) {
		seen := make([]bool, len(articles)+1)
		complete := true
		for _, e := range entries {
			if e.Article < 1 || e.Article > len(articles) || seen[e.Article] {
				complete = false
				break
			}
			seen[e.Article] = true
		}
		if complete {
			for entryIdx, e := range entries {
				take(e.Article-1, entryIdx)
			}
		}
	}

	articleTokens := make([]map[string]int, len(articles))
	for i, a := range articles {
		articleTokens[i] = weightTokens(a.Title)
	}
	coverage := func(entryIdx, articleIdx int) float64 {
		return tokenCoverage(weightTokens(entries[entryIdx].Title), articleTokens[articleIdx])
	}

	// Pass 2: exact URL echo. The prompt shows each article's URL and asks for
	// it back verbatim; URLs are immune to title paraphrase. Only an exact
	// match against a known article URL anchors — a mistyped URL is left for
	// title matching, never guessed at. Duplicate claims on one URL resolve
	// to nothing: the first claimant in reply order wins only when no other
	// entry echoes the same URL.
	byURL := make(map[string]int, len(articles))
	for articleIdx, a := range articles {
		if a.Url == "" {
			continue
		}
		if _, dup := byURL[a.Url]; !dup {
			byURL[a.Url] = articleIdx
		} else {
			// Two articles sharing one URL cannot be told apart by echo.
			byURL[a.Url] = unassigned
		}
	}
	claimed := make(map[string]int)
	for entryIdx, e := range entries {
		if entryUsed[entryIdx] || e.URL == "" {
			continue
		}
		articleIdx, ok := byURL[e.URL]
		if !ok || articleIdx == unassigned || assigned[articleIdx] != unassigned {
			continue
		}
		if first, dup := claimed[e.URL]; dup && first != entryIdx {
			continue
		}
		claimed[e.URL] = entryIdx
		take(articleIdx, entryIdx)
	}

	// Pass 3: mutual-best matching over the entries the numbering did not place.
	// Repeat until a pass anchors nothing new, so an entry that lost a contested
	// article can re-anchor to its next-best candidate.
	for {
		anchored := false
		for entryIdx := range entries {
			if entryUsed[entryIdx] {
				continue
			}
			bestArticle, bestScore := unassigned, 0.0
			for articleIdx := range articles {
				if assigned[articleIdx] != unassigned {
					continue
				}
				// Strict > favours the earlier article on a tie, matching
				// reply order.
				if score := coverage(entryIdx, articleIdx); score > bestScore {
					bestArticle, bestScore = articleIdx, score
				}
			}
			if bestArticle == unassigned || bestScore == 0 {
				continue
			}
			// The article must rate this entry its best remaining candidate
			// too, or the match is one-sided and not trustworthy.
			bestEntry, bestEntryScore := unassigned, -1.0
			for other := range entries {
				if entryUsed[other] {
					continue
				}
				if score := coverage(other, bestArticle); score > bestEntryScore {
					bestEntry, bestEntryScore = other, score
				}
			}
			if bestEntry == entryIdx {
				take(bestArticle, entryIdx)
				anchored = true
			}
		}
		if !anchored {
			break
		}
	}

	// Pass 4: a single leftover article with a single leftover entry is forced —
	// there is nothing to confuse, so no guessing is involved. This keeps the
	// single-article case, and short fixture titles like "A", working when the
	// model answers without the article number.
	var freeArticles, freeEntries []int
	for i := range assigned {
		if assigned[i] == unassigned {
			freeArticles = append(freeArticles, i)
		}
	}
	for i := range entries {
		if !entryUsed[i] {
			freeEntries = append(freeEntries, i)
		}
	}
	if len(freeArticles) == 1 && len(freeEntries) == 1 {
		take(freeArticles[0], freeEntries[0])
	}

	missing := 0
	for _, entryIdx := range assigned {
		if entryIdx == unassigned {
			missing++
		}
	}
	return assigned, missing
}
