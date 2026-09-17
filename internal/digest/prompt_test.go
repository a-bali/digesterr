package digest

import (
	"strings"
	"testing"
)

// TestWithJSONRuleIsIdempotent guards the retry path: the corrective retry
// prompt is built from the already-hardened first-attempt prompt, so adding the
// rule unconditionally would ship it twice and lengthen an already oversized
// prompt.
func TestWithJSONRuleIsIdempotent(t *testing.T) {
	base := `Format your response as JSON with keys "title" and "highlights".`
	once := withJSONRule(base)
	twice := withJSONRule(once)
	if once != twice {
		t.Errorf("withJSONRule is not idempotent:\nonce:  %q\ntwice: %q", once, twice)
	}
}

// TestWithJSONRuleCoversQuotingAndSeparators pins the failure modes seen in
// production logs: unescaped quotes inside a string value (a 200 reply whose
// JSON could not be parsed at all), prose/fences around the object, and
// trailing commas. Each must be named by the rule, because the corrective retry
// quotes the same text back to the model.
func TestWithJSONRuleCoversQuotingAndSeparators(t *testing.T) {
	rule := withJSONRule("")
	for _, want := range []string{
		"valid JSON",
		`\"`,
		"no markdown fences",
		"no prose",
		"no trailing commas",
	} {
		if !strings.Contains(rule, want) {
			t.Errorf("JSON rule does not mention %q:\n%s", want, rule)
		}
	}
}

func TestWithJSONRuleOnEmptyPrompt(t *testing.T) {
	if got := withJSONRule(""); got == "" {
		t.Fatal(`withJSONRule("") = "", want the rule itself`)
	}
}
