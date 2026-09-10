package feed

import "time"

// QuotaSnapshot is a JSON-friendly view of one provider's rate-limit state,
// surfaced through the metrics API so quota exhaustion is visible before it
// turns into failed polls. Providers that advertise a budget fill one in;
// providers that do not (RSS, Hacker News, Lobsters) are absent entirely.
type QuotaSnapshot struct {
	// Provider is the feed type the budget belongs to, e.g. "twitter".
	Provider string `json:"provider"`
	// Measured is false until a response carrying rate-limit headers has been
	// seen, so a zero Remaining is not mistaken for an exhausted budget.
	Measured  bool `json:"measured"`
	Limit     int  `json:"limit"`
	Remaining int  `json:"remaining"`
	// Reset and CheckedAt are pointers so omitempty actually omits them:
	// omitempty has no effect on a time.Time, which would otherwise serialise
	// as the year-1 zero time before anything has been measured.
	Reset     *time.Time `json:"reset,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	// Endpoint narrows the budget to the call that governs whether feeds keep
	// updating, for providers that meter each endpoint separately.
	Endpoint string `json:"endpoint,omitempty"`
	// Blocked is set when a 429 has suppressed requests until its reset.
	Blocked      bool       `json:"blocked"`
	BlockedUntil *time.Time `json:"blocked_until,omitempty"`
	// Reserve is the headroom kept for ingestion; BudgetLow is true when the
	// remaining budget has fallen below it.
	Reserve   int  `json:"reserve"`
	BudgetLow bool `json:"budget_low"`
}
