// Package llmjson locates JSON documents inside model replies.
//
// Prompts ask for bare JSON, but models routinely wrap it in a markdown code
// fence, surround it with prose, or double-encode it as a JSON string. These
// helpers find the document so each caller can decode it in its own shape,
// instead of every caller re-implementing fence trimming.
package llmjson

import (
	"encoding/json"
	"strings"
)

// FirstObject returns the outermost {...} span in s, ignoring braces inside
// quoted strings so a brace in a summary does not end the scan early.
func FirstObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// UnwrapString handles a model that double-encodes its answer: the reply is a
// JSON string whose value is the object we actually asked for
// ("{\"title\": ...}"). One Unmarshal yields that inner document.
func UnwrapString(raw string) (string, bool) {
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" || !strings.Contains(s, "{") {
		return "", false
	}
	return s, true
}

// Excerpt collapses whitespace in s and truncates it to max runes, so a raw
// model reply can ride along in a log line or an error message. Truncation is
// rune-based to keep the result valid UTF-8.
func Excerpt(s string, max int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	runes := []rune(collapsed)
	if len(runes) <= max {
		return collapsed
	}
	return string(runes[:max]) + "…"
}
