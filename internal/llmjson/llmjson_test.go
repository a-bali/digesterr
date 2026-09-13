package llmjson

import (
	"strings"
	"testing"
)

func TestFirstObject(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "bare", in: `{"a":1}`, want: `{"a":1}`, ok: true},
		{name: "fenced", in: "```json\n{\"a\":1}\n```", want: `{"a":1}`, ok: true},
		{name: "prose", in: `Here: {"a":1} — done`, want: `{"a":1}`, ok: true},
		{name: "nested", in: `{"a":{"b":[1,2]}}tail`, want: `{"a":{"b":[1,2]}}`, ok: true},
		{name: "braces in strings", in: `{"a":"} {"}`, want: `{"a":"} {"}`, ok: true},
		{name: "escaped quote in string", in: `{"a":"he said \"} \""}`, want: `{"a":"he said \"} \""}`, ok: true},
		{name: "no object", in: "just prose", ok: false},
		{name: "unterminated", in: `{"a":1`, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FirstObject(tt.in)
			if ok != tt.ok {
				t.Fatalf("FirstObject(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("FirstObject(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnwrapString(t *testing.T) {
	got, ok := UnwrapString(`"{\"a\":1}"`)
	if !ok || got != `{"a":1}` {
		t.Errorf("UnwrapString(double-encoded) = %q/%v, want the inner object", got, ok)
	}
	if _, ok := UnwrapString(`{"a":1}`); ok {
		t.Error("UnwrapString(object) = ok, want false (not a JSON string)")
	}
	if _, ok := UnwrapString(`"no object here"`); ok {
		t.Error("UnwrapString(string without object) = ok, want false")
	}
}

func TestExcerpt(t *testing.T) {
	got := Excerpt("line one\n\n  line   two", 100)
	if got != "line one line two" {
		t.Errorf("Excerpt collapsed = %q, want %q", got, "line one line two")
	}

	// Truncation must stay valid UTF-8 even when the cut lands mid-rune.
	long := strings.Repeat("é", 20)
	got = Excerpt(long, 5)
	if want := strings.Repeat("é", 5) + "…"; got != want {
		t.Errorf("Excerpt(long) = %q, want %q", got, want)
	}
}
