package digest

import "strings"

// jsonOutputRule is appended to the digest prompt (and to the corrective retry
// prompt) to cut down malformed replies from weaker models.
//
// Measured motivation: a free-tier failover model answered HTTP 200 with
// unescaped double quotes inside a summary ("glitter milk"), which
// encoding/json rejects with "Expecting ',' delimiter" at the first bare quote
// and cost the whole digest run. Every current failure mode of that shape is a
// quoting/separator mistake, so the rule names the remedy explicitly instead of
// asking for JSON in general.
//
// Wording is deliberately a single sentence with examples: an earlier attempt
// that restructured the format instruction (numbering every entry) tripled
// malformed replies, so the rule was added alongside the existing instruction
// rather than replacing it. See generator.go buildContent for that history.
const jsonOutputRule = "The reply must be valid JSON: emit ONE bare JSON object and nothing else — " +
	"no prose, no markdown fences, no trailing commas, no comments — and escape every double " +
	`quote that appears INSIDE a string value as \" (write the magazine "Wired" as \"Wired\"); ` +
	"never leave an unescaped quote inside a string."

// withJSONRule appends the JSON-validity rule to a prompt that does not carry
// it yet. It is idempotent so a caller that already added the rule (or a test
// fixture built from a prompt that did) cannot end up with it twice.
func withJSONRule(prompt string) string {
	if strings.Contains(prompt, jsonOutputRule) {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return jsonOutputRule
	}
	return strings.TrimRight(prompt, "\n") + " " + jsonOutputRule
}
