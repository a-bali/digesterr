package feed

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"
)

// ReadabilityResult is the outcome of running readability over an item's body.
//
// `Input` names which body was processed ("extracted" or "original") so the UI
// can say what it acted on, and `Content` is the cleaned markup. `Title`,
// `Byline`, `Excerpt` and `SiteName` come from the page's own metadata when
// readability finds any (often empty for a stored fragment, which carries no
// <head>).
type ReadabilityResult struct {
	Input    string
	Content  string
	Title    string
	Byline   string
	Excerpt  string
	SiteName string
	// Reduced reports whether the cleaned body is smaller than its input. False
	// means readability found nothing to strip, which is the interesting case for
	// a stored fragment: worth surfacing rather than presenting as a win.
	Reduced bool
}

// ApplyReadability runs the content through go-readability and returns the
// cleaned main-article HTML.
//
// This operates on the body we ALREADY HAVE, not on the live page: callers pass
// the extracted body when there is one and the original feed body otherwise, so
// the button costs no network request and works on items whose source is gone.
// The feed package's ExtractContent is the network path and is a different thing.
//
// pageURL gives readability a base for resolving relative links; it may be empty.
func ApplyReadability(content, pageURL string) (ReadabilityResult, error) {
	if strings.TrimSpace(content) == "" {
		return ReadabilityResult{}, fmt.Errorf("no content to process")
	}

	var base *url.URL
	if pageURL != "" {
		if u, err := url.Parse(pageURL); err == nil {
			base = u
		}
	}

	article, err := readability.FromReader(strings.NewReader(content), base)
	if err != nil {
		return ReadabilityResult{}, fmt.Errorf("readability: %w", err)
	}

	var buf bytes.Buffer
	if err := article.RenderHTML(&buf); err != nil {
		return ReadabilityResult{}, fmt.Errorf("rendering readability output: %w", err)
	}

	out := strings.TrimSpace(buf.String())
	if out == "" {
		// Parse() returns a nil Node when it cannot find a main article — it does
		// not error. Present that as a failure rather than an empty body, which
		// would otherwise look like the item had no content.
		return ReadabilityResult{}, fmt.Errorf("readability found no article content")
	}

	return ReadabilityResult{
		Content:  out,
		Title:    article.Title(),
		Byline:   article.Byline(),
		Excerpt:  article.Excerpt(),
		SiteName: article.SiteName(),
		Reduced:  len(out) < len(content),
	}, nil
}

// ReadabilityInput picks the body readability should process, mirroring how the
// pane renders an item: a successful extraction wins, otherwise the original.
// 'pending'/'failed'/'cleared' hold no usable body, and a failed extraction
// stores its error log in extract_content, so those fall back too.
func ReadabilityInput(extractContent, extractStatus, original string) (body string, which string) {
	if extractContent != "" && extractStatus == "done" {
		return extractContent, "extracted"
	}
	return original, "original"
}

// readabilityPageID is the marker go-readability stamps on its own output
// (parser.go sets id="readability-page-1", with class="page", on the wrapper it
// returns). It survives as-is in stored content, which is what makes it usable
// as a fingerprint.
const readabilityPageID = `id="readability-page-1"`

// IsReadabilityOutput reports whether a body is ALREADY go-readability output.
//
// This matters because the extraction path (ExtractContent → extractHTMLContent)
// runs the same library, so most extracted bodies are readability output already
// — measuring the dev database, 4276 of 4794 extracted items. Running the
// processing over those is a no-op, so the UI hides the control rather than
// offering an action that cannot do anything.
//
// The marker is emitted for both shapes the library can return (the single-child
// case and the wrapped case), so a plain substring test is sufficient; no HTML
// parsing is needed for a fingerprint.
func IsReadabilityOutput(body string) bool {
	return strings.Contains(body, readabilityPageID)
}

// IsReaderable reports whether the body has enough article structure to be worth
// running readability over.
//
// MEASURED CAVEAT: this is much STRICTER than the operation it appears to gate.
// It calls the library's CheckDocument, which keeps only p/pre/article nodes, skips
// any node under 140 characters of text, and only passes once an accumulated score
// exceeds 20. FromReader — what actually processes a body — never consults it.
// Measured on real newsletters: a body this rejects 54% smaller output with no
// error, and 5 of 7 newsletter items were false negatives.
//
// So do NOT gate a per-item user action on it: offer the action and report the
// outcome. It is defensible only where a wasted attempt means a wasted NETWORK
// fetch. For a body already in the database, ApplyReadabilityGuarded is the honest
// check — it decides on the real output instead of guessing from the input.
func IsReaderable(body string) bool {
	if strings.TrimSpace(body) == "" {
		return false
	}
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return false
	}
	return readability.CheckDocument(doc)
}

// ReadabilityVerdict is the outcome of deciding whether processed output may
// replace the stored body.
type ReadabilityVerdict int

const (
	// ReadabilityAccept: the output is smaller and kept essentially every word.
	ReadabilityAccept ReadabilityVerdict = iota
	// ReadabilityNoBenefit: the output is not smaller, so there is nothing to gain.
	// Silent by design — the item should look untouched, not broken.
	ReadabilityNoBenefit
	// ReadabilityLossy: the output dropped a large share of the words. That is
	// content loss rather than chrome removal, and the caller must keep the original.
	ReadabilityLossy
)

// maxWordLossPercent is the share of words the processing may drop before its
// output is treated as having eaten real content.
//
// Measured over 165 real bodies (dev-DB bodies + a full-text RSS feed + newsletter
// items): legitimate output keeps 100% of words in almost every case and 97.9% in
// the worst, because what it removes is tracking pixels and legal footers. The
// genuinely mangled cases lost 21.7% and 34.8%. The threshold sits in that empty
// gap, so its exact value is not load-bearing.
//
// This is measured in WORDS, not bytes, and that is the point: the two are
// unrelated. The long newsletter dropped 25% of its BYTES while keeping 97.9% of
// its words (chrome), while a short one dropped 54% of its bytes while losing 21.7%
// of its words (prose). A byte rule would accept the second and could reject the
// first, which is why byte size cannot be the safety signal.
const maxWordLossPercent = 5.0

// ApplyReadabilityGuarded runs readability and decides whether its output may
// replace the stored body, reporting why when it may not.
//
// This is the honest gate: it judges the REAL output rather than pre-screening the
// input (see the caveat on IsReaderable). It never returns a result the caller
// should store together with a rejecting verdict.
func ApplyReadabilityGuarded(content, pageURL string) (ReadabilityResult, ReadabilityVerdict, error) {
	res, err := ApplyReadability(content, pageURL)
	if err != nil {
		return ReadabilityResult{}, ReadabilityNoBenefit, err
	}
	// Not smaller: the library wrapped the body without removing anything. The
	// measured shape for short teasers and stub bodies, where its ~62-byte wrapper
	// makes the output LARGER than the input.
	if len(res.Content) >= len(content) {
		return res, ReadabilityNoBenefit, nil
	}
	inWords, outWords := proseWordCount(content), proseWordCount(res.Content)
	if inWords == 0 {
		// No prose to judge, and it is smaller, so there is nothing to lose.
		return res, ReadabilityAccept, nil
	}
	if lost := 100 * float64(inWords-outWords) / float64(inWords); lost > maxWordLossPercent {
		return res, ReadabilityLossy, nil
	}
	return res, ReadabilityAccept, nil
}

// proseWordCount counts the words a reader would SEE: words made only of
// invisible characters are not words.
//
// This matters because the guard's input is a raw stored body, and newsletters
// pad their preheader with a long run of &nbsp;/zero-width characters so the
// mail client's inbox preview shows the summary. That run is invisible, but
// whitespace-separated, so a plain Fields count sees it as tens of words — and
// because readability CORRECTLY strips the padding as chrome, the guard scored
// that removal as content loss and refused the entire cleaned article. Measured
// on the affected feed: five bodies were rejected at 5.5-7.7% "loss" while the
// words a reader would actually miss were 0.5-1.0%. The genuinely mangled
// bodies in the same set lose 12-35% of visible words, so the threshold keeps
// separating the two cases with room to spare.
//
// The fix belongs here rather than in the threshold: the padding never rendered,
// so it was never prose, and lowering the cut-off would have admitted real
// content loss to compensate for counting fiction.
func proseWordCount(body string) int {
	n := 0
	for _, f := range strings.Fields(stripHTMLTags(body)) {
		if rendersInk(f) {
			n++
		}
	}
	return n
}

// rendersInk reports whether s contains any character that occupies visible
// width. Zero-width characters and non-breaking spaces do not; an ordinary
// space never reaches here because Fields already split on it.
func rendersInk(s string) bool {
	for _, r := range s {
		if r == '\u00a0' || unicode.Is(unicode.Cf, r) {
			continue
		}
		return true
	}
	return false
}
