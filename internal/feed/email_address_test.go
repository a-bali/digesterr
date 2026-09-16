package feed

import "testing"

func TestEmailSplitAddress(t *testing.T) {
	const inbox = "name@agentmail.to"

	tests := []struct {
		name       string
		addr       string
		inbox      string
		wantSuffix string
		wantOK     bool
	}{
		{"plain suffix", "name+sport@agentmail.to", inbox, "sport", true},
		{"hyphen and digits", "name+ai-weekly2@agentmail.to", inbox, "ai-weekly2", true},
		{"dotted suffix", "name+tech.news@agentmail.to", inbox, "tech.news", true},
		{"underscore suffix", "name+my_feed@agentmail.to", inbox, "my_feed", true},
		{"uppercase suffix is normalised", "name+SPORT@agentmail.to", inbox, "sport", true},
		{"mixed case normalised", "name+Ai-Weekly@agentmail.to", inbox, "ai-weekly", true},
		{"surrounding whitespace trimmed", "  name+sport@agentmail.to  ", inbox, "sport", true},
		// The inbox local part is matched case-insensitively: the live API
		// accepted a differently-cased local part, so rejecting here would
		// refuse an address that actually delivers.
		{"inbox local part case-insensitive", "NAME+sport@AGENTMAIL.TO", inbox, "sport", true},

		{"no plus at all", "name@agentmail.to", inbox, "", false},
		{"empty suffix", "name+@agentmail.to", inbox, "", false},
		{"different domain", "name+sport@example.com", inbox, "", false},
		{"different mailbox", "other+sport@agentmail.to", inbox, "", false},
		{"mailbox is a prefix but not equal", "names+sport@agentmail.to", inbox, "", false},
		{"empty addr", "", inbox, "", false},
		{"empty inbox", "name+sport@agentmail.to", "", "", false},
		{"addr missing domain", "name+sport", inbox, "", false},
		{"inbox missing domain", "name+sport@agentmail.to", "name", "", false},
		// A second '+' must not be silently truncated into a valid suffix; it
		// lands in the suffix and is rejected by the charset check.
		{"second plus rejected", "name+sport+extra@agentmail.to", inbox, "", false},
		{"at sign in suffix rejected", "name+sport@x@agentmail.to", inbox, "", false},
		// URL-structural characters: a local part carrying these is a known
		// routing/escaping bug class, so they must never become a suffix.
		{"slash rejected", "name+a/b@agentmail.to", inbox, "", false},
		{"question mark rejected", "name+a?b@agentmail.to", inbox, "", false},
		{"hash rejected", "name+a#b@agentmail.to", inbox, "", false},
		{"percent rejected", "name+a%2fb@agentmail.to", inbox, "", false},
		{"space in suffix rejected", "name+a b@agentmail.to", inbox, "", false},
		{"leading dot rejected", "name+.sport@agentmail.to", inbox, "", false},
		{"trailing dot rejected", "name+sport.@agentmail.to", inbox, "", false},
		{"leading hyphen rejected", "name+-sport@agentmail.to", inbox, "", false},
		{"trailing hyphen rejected", "name+sport-@agentmail.to", inbox, "", false},
		{"suite ascii rejected", "name+sporté@agentmail.to", inbox, "", false},
		{"empty mailbox local part", "+sport@agentmail.to", inbox, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			suffix, ok := EmailSplitAddress(tc.addr, tc.inbox)
			if ok != tc.wantOK {
				t.Fatalf("EmailSplitAddress(%q, %q) ok = %v, want %v", tc.addr, tc.inbox, ok, tc.wantOK)
			}
			if suffix != tc.wantSuffix {
				t.Fatalf("EmailSplitAddress(%q, %q) suffix = %q, want %q", tc.addr, tc.inbox, suffix, tc.wantSuffix)
			}
		})
	}
}

// The suffix length limit must accept the maximum and reject one over.
func TestValidEmailSuffixLengthBoundary(t *testing.T) {
	max := ""
	for i := 0; i < maxEmailSuffixLen; i++ {
		max += "a"
	}
	if !ValidEmailSuffix(max) {
		t.Fatalf("suffix of exactly %d chars should be valid", maxEmailSuffixLen)
	}
	if ValidEmailSuffix(max + "a") {
		t.Fatalf("suffix of %d chars should be rejected", maxEmailSuffixLen+1)
	}
}

// Build and split must round-trip, and building must not be fooled by the
// caller's casing or padding.
func TestEmailBuildAddressRoundTrip(t *testing.T) {
	const inbox = "name@agentmail.to"

	built := EmailBuildAddress(inbox, "Sport")
	if built != "name+sport@agentmail.to" {
		t.Fatalf("EmailBuildAddress = %q, want %q", built, "name+sport@agentmail.to")
	}

	suffix, ok := EmailSplitAddress(built, inbox)
	if !ok || suffix != "sport" {
		t.Fatalf("round-trip failed: suffix=%q ok=%v", suffix, ok)
	}

	// Building with a malformed inbox must not panic or fabricate a domain.
	if got := EmailBuildAddress("no-domain", "sport"); got != "sport" {
		t.Fatalf("EmailBuildAddress with malformed inbox = %q, want the suffix unchanged", got)
	}
}

func TestEmailInboxPrefixAndDomain(t *testing.T) {
	prefix := EmailInboxPrefix("name@agentmail.to")
	domain := EmailInboxDomain("name@agentmail.to")
	if prefix != "name+" {
		t.Fatalf("EmailInboxPrefix = %q, want %q", prefix, "name+")
	}
	if domain != "@agentmail.to" {
		t.Fatalf("EmailInboxDomain = %q, want %q", domain, "@agentmail.to")
	}
	// A malformed inbox yields empty context rather than a partial string the
	// UI would render as if it were real.
	if EmailInboxPrefix("no-domain") != "" || EmailInboxDomain("no-domain") != "" {
		t.Fatal("malformed inbox should yield empty prefix and domain")
	}
}

func TestEmailSuffixToName(t *testing.T) {
	tests := map[string]string{
		"sport":      "Sport",
		"ai-weekly":  "Ai Weekly",
		"tech.news":  "Tech News",
		"my_feed":    "My Feed",
		"a-b_c.d":    "A B C D",
		"already-ok": "Already Ok",
	}
	for suffix, want := range tests {
		if got := EmailSuffixToName(suffix); got != want {
			t.Errorf("EmailSuffixToName(%q) = %q, want %q", suffix, got, want)
		}
	}
}
