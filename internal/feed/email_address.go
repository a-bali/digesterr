// Package feed implements feed fetching, enrichment and extraction.
//
// This file owns the email-feed address contract: how a plus-addressed
// AgentMail address maps to a feed. Everything that reads or writes an email
// feed's URL goes through here, so the rules cannot drift between the HTTP
// layer, the fetcher and the frontend.
package feed

import (
	"fmt"
	"strings"
)

// maxEmailSuffixLen bounds the subaddress suffix. Long enough for a readable
// slug, short enough to stay well inside the 64-octet local-part limit once
// the inbox's own local part and the '+' are prepended.
const maxEmailSuffixLen = 64

// EmailSplitAddress splits an email feed URL into its subaddress suffix.
//
// The inbox argument is the configured mailbox (config.Email.InboxID), e.g.
// "name@agentmail.to". A feed for a sports newsletter is addressed
// "name+sport@agentmail.to"; the suffix ("sport") is what identifies the feed,
// because one inbox serves every email feed on the instance.
//
// Returns ok=false when:
//   - either argument is empty or malformed
//   - the address domain differs from the inbox domain
//   - the address local part has no '+', or has an empty suffix
//   - the mailbox local part does not match the inbox's (i.e. the address
//     points at a different mailbox entirely)
//   - the suffix contains characters that are not allowed in a slug
//
// The suffix is lowercased so "Sport" and "sport" cannot become two colliding
// feeds; AgentMail matched the local part case-insensitively when this was
// verified against the live API, so normalising cannot break delivery.
func EmailSplitAddress(addr, inbox string) (suffix string, ok bool) {
	addr = strings.TrimSpace(addr)
	inbox = strings.TrimSpace(inbox)
	if addr == "" || inbox == "" {
		return "", false
	}

	addrLocal, addrDomain, found := strings.Cut(addr, "@")
	if !found || addrLocal == "" || addrDomain == "" {
		return "", false
	}
	inboxLocal, inboxDomain, found := strings.Cut(inbox, "@")
	if !found || inboxLocal == "" || inboxDomain == "" {
		return "", false
	}
	if !strings.EqualFold(addrDomain, inboxDomain) {
		return "", false
	}

	// strings.Cut on the FIRST '+' only, so a malformed address containing a
	// second '+' lands in the suffix and is then rejected by the charset check
	// rather than being silently truncated at the second one.
	base, suf, found := strings.Cut(addrLocal, "+")
	if !found {
		return "", false
	}
	if !strings.EqualFold(base, inboxLocal) {
		return "", false
	}
	suf = strings.ToLower(strings.TrimSpace(suf))
	if !ValidEmailSuffix(suf) {
		return "", false
	}
	return suf, true
}

// ValidEmailSuffix reports whether s is an acceptable feed suffix.
//
// The charset is deliberately narrow. Beyond tidiness it is a URL concern: the
// suffix travels in places that treat '/', '?', '#' and '%' as structural, and
// a local part carrying them is a known class of routing/escaping bug. '@' and
// '+' are excluded so a suffix cannot itself reintroduce an address or a second
// subaddress level.
func ValidEmailSuffix(s string) bool {
	if s == "" || len(s) > maxEmailSuffixLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	// A leading or trailing separator reads as a typo and produces addresses
	// that are easy to mistype back; reject rather than normalise.
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") ||
		strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return false
	}
	return true
}

// EmailBuildAddress is the inverse of EmailSplitAddress: it materialises the
// full address for an inbox and suffix. The suffix is normalised the same way,
// so callers can pass whatever the user typed.
//
// It does NOT validate — call ValidEmailSuffix (or EmailSplitAddress) first
// when the suffix came from user input.
func EmailBuildAddress(inbox, suffix string) string {
	local, domain, found := strings.Cut(strings.TrimSpace(inbox), "@")
	if !found {
		// Malformed inbox: return the suffix unchanged rather than inventing an
		// address. Callers validate the inbox at startup.
		return suffix
	}
	return fmt.Sprintf("%s+%s@%s", local, strings.ToLower(strings.TrimSpace(suffix)), domain)
}

// EmailInboxPrefix returns the "local+" part of an inbox address, which the UI
// renders as fixed context next to the editable suffix field.
func EmailInboxPrefix(inbox string) string {
	local, _, found := strings.Cut(strings.TrimSpace(inbox), "@")
	if !found {
		return ""
	}
	return local + "+"
}

// EmailInboxDomain returns the "@domain" part of an inbox address, also shown
// as fixed context in the UI.
func EmailInboxDomain(inbox string) string {
	_, domain, found := strings.Cut(strings.TrimSpace(inbox), "@")
	if !found {
		return ""
	}
	return "@" + domain
}

// EmailSuffixToName derives a display name from a suffix, used when the feed
// name is not supplied: "sport" -> "Sport", "ai-weekly" -> "Ai Weekly".
//
// This is a convenience, not a lookup. AgentMail has no "name this address"
// call, and a lookup that could time out would look like a broken form.
func EmailSuffixToName(suffix string) string {
	fields := strings.FieldsFunc(suffix, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i, f := range fields {
		// Uppercase the first rune, not the first byte: a suffix is normally
		// ASCII (see ValidEmailSuffix) but this must not mangle input it was
		// never given, and byte slicing would split a multi-byte rune.
		r := []rune(f)
		fields[i] = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	return strings.Join(fields, " ")
}
