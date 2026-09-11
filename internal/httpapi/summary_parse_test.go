package httpapi

import (
	"strings"
	"testing"
)

// TestParseSummaryResponse covers the shapes a model actually returns for the
// JSON prompt. Wrapping it in a markdown fence or surrounding it with prose used
// to leak the raw JSON into the UI, because the whole reply became the summary.
func TestParseSummaryResponse(t *testing.T) {
	const wantContent = "Mounting evidence indicates ADAS crashes less often."
	const wantComments = "Commenters offer skepticism and alternatives."

	tests := []struct {
		name  string
		reply string
		want  summarizeResponse
	}{
		{
			name:  "bare JSON",
			reply: `{"content_summary":"` + wantContent + `","comments_summary":"` + wantComments + `"}`,
			want:  summarizeResponse{ContentSummary: wantContent, CommentsSummary: wantComments},
		},
		{
			name:  "json code fence",
			reply: "```json\n{\"content_summary\":\"" + wantContent + "\",\"comments_summary\":\"" + wantComments + "\"}\n```",
			want:  summarizeResponse{ContentSummary: wantContent, CommentsSummary: wantComments},
		},
		{
			name:  "bare code fence without a language tag",
			reply: "```\n{\"content_summary\":\"" + wantContent + "\",\"comments_summary\":\"" + wantComments + "\"}\n```",
			want:  summarizeResponse{ContentSummary: wantContent, CommentsSummary: wantComments},
		},
		{
			name:  "fence plus surrounding prose",
			reply: "Here is the summary:\n\n```json\n{\"content_summary\":\"" + wantContent + "\",\"comments_summary\":\"" + wantComments + "\"}\n```\n\nLet me know if you need more detail.",
			want:  summarizeResponse{ContentSummary: wantContent, CommentsSummary: wantComments},
		},
		{
			name:  "leading whitespace and trailing newline",
			reply: "\n\n  {\"content_summary\":\"" + wantContent + "\",\"comments_summary\":\"" + wantComments + "\"}  \n",
			want:  summarizeResponse{ContentSummary: wantContent, CommentsSummary: wantComments},
		},
		{
			name:  "only one field present",
			reply: `{"content_summary":"` + wantContent + `"}`,
			want:  summarizeResponse{ContentSummary: wantContent},
		},
		{
			name:  "brace inside the summary text",
			reply: `{"content_summary":"Uses {curly} braces","comments_summary":"and {more}"}`,
			want:  summarizeResponse{ContentSummary: "Uses {curly} braces", CommentsSummary: "and {more}"},
		},
		{
			name:  "escaped quote inside the summary text",
			reply: `{"content_summary":"He said \"hi\" then left.","comments_summary":"ok"}`,
			want:  summarizeResponse{ContentSummary: `He said "hi" then left.`, CommentsSummary: "ok"},
		},
		{
			name:  "sentences returned as a list",
			reply: `{"content_summary":["First.","Second."],"comments_summary":"ok"}`,
			want:  summarizeResponse{ContentSummary: "First.\n\nSecond.", CommentsSummary: "ok"},
		},
		{
			name:  "nested object for one field",
			reply: `{"content_summary":{"text":"` + wantContent + `"},"comments_summary":"ok"}`,
			want:  summarizeResponse{ContentSummary: wantContent, CommentsSummary: "ok"},
		},
		{
			name:  "plain prose reply is kept verbatim",
			reply: "The article argues that ADAS reduces crashes. Commenters disagree.",
			want:  summarizeResponse{ContentSummary: "The article argues that ADAS reduces crashes. Commenters disagree."},
		},
		{
			name:  "empty reply yields nothing",
			reply: "   \n  ",
			want:  summarizeResponse{},
		},
		{
			// Changed deliberately: JSON that carries none of our keys cannot be
			// a usable summary, and passing it through is precisely how raw JSON
			// reached the pane. Prose is still kept (see the case above).
			name:  "undecodable JSON is dropped rather than rendered",
			reply: `{"unexpected":"value"}`,
			want:  summarizeResponse{},
		},
		{
			// Some models double-encode: the reply is a JSON string whose value
			// is the object we asked for.
			name:  "JSON string wrapping the object",
			reply: `"{\"content_summary\":\"Wrapped summary.\",\"comments_summary\":\"Wrapped comments.\"}"`,
			want:  summarizeResponse{ContentSummary: "Wrapped summary.", CommentsSummary: "Wrapped comments."},
		},
		{
			name:  "JSON string wrapping a fenced object",
			reply: "\"```json\\n{\\\"content_summary\\\":\\\"Fenced and wrapped.\\\"}\\n```\"",
			want:  summarizeResponse{ContentSummary: "Fenced and wrapped."},
		},
		{
			name:  "truncated JSON is dropped, not shown",
			reply: `{"content_summary":"The article argues that ADAS reduces crashe`,
			want:  summarizeResponse{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSummaryResponse(tt.reply)
			if got != tt.want {
				t.Errorf("parseSummaryResponse()\n got %+v\nwant %+v", got, tt.want)
			}
			// A regression guard for the reported bug: the rendered summary must
			// never contain a JSON object, whichever shape came back.
			for _, field := range []string{got.ContentSummary, got.CommentsSummary} {
				if strings.Contains(field, "content_summary") || strings.Contains(field, "```") {
					t.Errorf("summary still contains raw JSON or a code fence: %q", field)
				}
			}
		})
	}
}
