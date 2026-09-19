package feed

import (
	"strings"
	"testing"
)

// The button processes the body we STORE, so which body it picks is the whole
// contract: an extracted body wins, everything else falls back to the original.
// 'failed' matters most — a failed extraction keeps its error log in
// extract_content, so treating that as content would feed an error message to
// readability.
func TestReadabilityInput(t *testing.T) {
	cases := []struct {
		name           string
		extractContent string
		extractStatus  string
		original       string
		wantBody       string
		wantWhich      string
	}{
		{"successful extraction wins", "<p>extracted</p>", "done", "<p>original</p>", "<p>extracted</p>", "extracted"},
		{"never extracted falls back", "", "", "<p>original</p>", "<p>original</p>", "original"},
		{"skipped falls back", "", "skipped", "<p>original</p>", "<p>original</p>", "original"},
		{"pending falls back", "", "pending", "<p>original</p>", "<p>original</p>", "original"},
		{"cleared falls back", "", "cleared", "<p>original</p>", "<p>original</p>", "original"},
		// A failed extraction stores its error log in extract_content. Falling
		// back to the original is what stops that log becoming the input.
		{"failed uses the original, not the error log", "EOF fetching page", "failed", "<p>original</p>", "<p>original</p>", "original"},
		// Defensive: 'done' with an empty body is not usable content.
		{"done with empty body falls back", "", "done", "<p>original</p>", "<p>original</p>", "original"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, which := ReadabilityInput(tc.extractContent, tc.extractStatus, tc.original)
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
			if which != tc.wantWhich {
				t.Errorf("which = %q, want %q", which, tc.wantWhich)
			}
		})
	}
}

func TestApplyReadabilityRejectsEmptyContent(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t "} {
		if _, err := ApplyReadability(in, "https://example.com/a"); err == nil {
			t.Errorf("ApplyReadability(%q) = nil error, want an error", in)
		}
	}
}

// An article-shaped document must come back as cleaned markup with the clutter
// gone. This is the case the experiment exists for.
func TestApplyReadabilityExtractsArticle(t *testing.T) {
	doc := `<html><head><title>Real Title</title></head><body>
	  <nav><a href="/">Home</a><a href="/about">About</a></nav>
	  <div class="ad">Buy this now</div>
	  <article>
	    <h1>A real headline</h1>
	    <p>` + strings.Repeat("This is a substantial paragraph of article prose. ", 20) + `</p>
	    <p>` + strings.Repeat("And a second paragraph that also carries real content. ", 20) + `</p>
	  </article>
	  <footer>Copyright nobody</footer>
	</body></html>`

	res, err := ApplyReadability(doc, "https://example.com/story")
	if err != nil {
		t.Fatalf("ApplyReadability: %v", err)
	}
	if res.Content == "" {
		t.Fatal("empty content")
	}
	// The article body survives.
	if !strings.Contains(res.Content, "substantial paragraph") {
		t.Error("article prose missing from the output")
	}
	// The chrome is gone. Without an assertion like this the test would pass on a
	// function that simply returns its input unchanged.
	if strings.Contains(res.Content, "Buy this now") {
		t.Error("advert survived readability")
	}
	if strings.Contains(res.Content, "//example.com/story") {
		t.Error("unexpected synthetic content")
	}
	// Reduced is the signal the UI shows; it must reflect reality.
	if !res.Reduced {
		t.Errorf("Reduced = false, want true (in=%d out=%d)", len(doc), len(res.Content))
	}
}

// Content whose body is ALREADY readability output (what the extraction path
// stores) is the near-no-op case. It must succeed and report Reduced=false rather
// than erroring or claiming a reduction.
func TestApplyReadabilityOnExistingReadabilityOutputIsANoop(t *testing.T) {
	inner := `<p>` + strings.Repeat("Already-cleaned article prose here. ", 30) + `</p>`
	already := `<div id="readability-page-1" class="page"><div>` + inner + `</div></div>`

	res, err := ApplyReadability(already, "https://example.com/story")
	if err != nil {
		t.Fatalf("ApplyReadability: %v", err)
	}
	if !strings.Contains(res.Content, "Already-cleaned article prose") {
		t.Error("body lost")
	}
	if res.Reduced {
		t.Errorf("Reduced = true for already-cleaned input (in=%d out=%d)", len(already), len(res.Content))
	}
}

// A body that is not an article at all: readability cannot find main content.
// This must be an error the caller can report, not an empty success.
func TestApplyReadabilityOnNonArticleFails(t *testing.T) {
	// A fragment with no article structure and no prose.
	res, err := ApplyReadability(`<div></div>`, "https://example.com/story")
	if err == nil && res.Content == "" {
		t.Fatal("returned neither content nor an error")
	}
	if err != nil && res.Content != "" {
		t.Fatal("returned both content and an error")
	}
}

// The whole point of the fingerprint is to recognise what the extraction path
// stored, so it must match the marker the library actually emits.
func TestIsReadabilityOutput(t *testing.T) {
	libraryOutput := `<div id="readability-page-1" class="page"><div><p>Body</p></div></div>`
	if !IsReadabilityOutput(libraryOutput) {
		t.Error("did not recognise go-readability output")
	}
	// The single-child shape the library can also return.
	if !IsReadabilityOutput(`<div id="readability-page-1" class="page"><p>Body</p></div>`) {
		t.Error("did not recognise the single-child shape")
	}
	for _, body := range []string{
		"",
		"<p>plain original body</p>",
		// Close to the marker but not it: a body merely mentioning the class must
		// not be mistaken for processed output, or the toggle would hide wrongly.
		`<div class="page">Body</div>`,
		`<p>readability-page-1 is mentioned in prose</p>`,
	} {
		if IsReadabilityOutput(body) {
			t.Errorf("IsReadabilityOutput(%q) = true, want false", body)
		}
	}
}

// A body already produced by readability must survive a round trip through the
// real library, so the fingerprint matches genuine output rather than only a
// hand-written string.
func TestApplyReadabilityOutputIsDetected(t *testing.T) {
	doc := `<html><head><title>T</title></head><body><article><h1>H</h1><p>` +
		strings.Repeat("Real article prose that is long enough to be kept. ", 30) +
		`</p></article></body></html>`
	res, err := ApplyReadability(doc, "https://example.com/story")
	if err != nil {
		t.Fatalf("ApplyReadability: %v", err)
	}
	if !IsReadabilityOutput(res.Content) {
		t.Error("output of ApplyReadability is not recognised by IsReadabilityOutput")
	}
}

func TestIsReaderable(t *testing.T) {
	article := "<html><body><article><p>" +
		strings.Repeat("Substantial article prose for the readerable check. ", 30) +
		"</p></article></body></html>"
	if !IsReaderable(article) {
		t.Error("an article-shaped body was judged not readerable")
	}
	// The cases the toggle should NOT be offered for: a stored feed body is often
	// just a link, an embed or a stub.
	for _, body := range []string{
		"",
		"   ",
		"<p>short</p>",
		`<p><a href="https://example.com/x">An interesting link</a></p>`,
		`<img src="https://example.com/i.png">`,
	} {
		if IsReaderable(body) {
			t.Errorf("IsReaderable(%q) = true, want false", body)
		}
	}
}
