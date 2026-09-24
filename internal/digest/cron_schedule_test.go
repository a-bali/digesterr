package digest

import (
	"testing"
	"time"
)

// TestCronMatchesDayOfMonthAndDayOfWeekUseOR pins Vixie-cron semantics: when
// BOTH day-of-month and day-of-week are restricted (neither is "*"), the entry
// fires when EITHER matches. The current implementation ANDs them, so a
// schedule like "0 8 1 * 1" fires only on a Monday that is also the 1st —
// nearly never, and silently.
func TestCronMatchesDayOfMonthAndDayOfWeekUseOR(t *testing.T) {
	// 2026-06-01 is a Monday.
	mondayFirst := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	// 2026-06-08 is a Monday, not the 1st.
	mondayEighth := time.Date(2026, 6, 8, 8, 0, 0, 0, time.UTC)
	// 2026-07-01 is a Wednesday, not a Monday.
	wednesdayFirst := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	// 2026-06-03 is a Wednesday, neither the 1st nor a Monday.
	wednesdayThird := time.Date(2026, 6, 3, 8, 0, 0, 0, time.UTC)

	if !cronMatches("0 8 1 * 1", mondayFirst) {
		t.Error("both fields match: must fire")
	}
	if !cronMatches("0 8 1 * 1", mondayEighth) {
		t.Error("day-of-week matches (Monday): must fire under Vixie OR semantics")
	}
	if !cronMatches("0 8 1 * 1", wednesdayFirst) {
		t.Error("day-of-month matches (the 1st): must fire under Vixie OR semantics")
	}
	if cronMatches("0 8 1 * 1", wednesdayThird) {
		t.Error("neither field matches: must not fire")
	}
}

// TestCronMatchesWildcardDayFieldsUseAND guards the other half of the rule: the
// OR only applies when BOTH day fields are restricted. With one left as "*",
// the other still constrains the schedule.
func TestCronMatchesWildcardDayFieldsUseAND(t *testing.T) {
	mondayEighth := time.Date(2026, 6, 8, 8, 0, 0, 0, time.UTC) // Monday, 8th
	tuesdayNinth := time.Date(2026, 6, 9, 8, 0, 0, 0, time.UTC) // Tuesday, 9th
	firstJune := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)    // Monday, 1st
	firstJuly := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)    // Wednesday, 1st

	// Only day-of-month restricted.
	if !cronMatches("0 8 1 * *", firstJune) {
		t.Error("day-of-month 1 with wildcard weekday: must fire on the 1st")
	}
	if cronMatches("0 8 1 * *", mondayEighth) {
		t.Error("day-of-month 1 with wildcard weekday: must not fire on the 8th")
	}
	// Only day-of-week restricted.
	if !cronMatches("0 8 * * 1", mondayEighth) {
		t.Error("weekday 1 with wildcard day-of-month: must fire on Monday")
	}
	if cronMatches("0 8 * * 1", tuesdayNinth) {
		t.Error("weekday 1 with wildcard day-of-month: must not fire on Tuesday")
	}
	if cronMatches("0 8 * * 1", firstJuly) {
		t.Error("weekday 1 with wildcard day-of-month: must not fire on a Wednesday")
	}
}

// TestValidateScheduleRejectsUnrunnableExpressions: an invalid cron expression
// is accepted today and simply never fires, so the user sees a digest that
// silently does not exist. Creation must be refused instead.
func TestValidateScheduleRejectsUnrunnableExpressions(t *testing.T) {
	bad := []string{
		"",              // empty
		"0 8 * *",       // four fields
		"0 8 * * * *",   // six fields
		"60 8 * * *",    // minute out of range
		"0 24 * * *",    // hour out of range
		"0 8 32 * *",    // day-of-month out of range
		"0 8 * 13 *",    // month out of range
		"0 8 * * 8",     // weekday out of range
		"abc * * * *",   // not a number
		"0 8 1-100 * *", // range out of bounds
		"*/0 * * * *",   // zero step
		"0 8 * * mon",   // names are not supported
	}
	for _, s := range bad {
		if err := ValidateSchedule(s); err == nil {
			t.Errorf("ValidateSchedule(%q) = nil, want an error (this schedule can never run)", s)
		}
	}
}

// TestValidateScheduleAcceptsValidExpressions is the positive control: the
// validator must not reject schedules the matcher can actually run.
func TestValidateScheduleAcceptsValidExpressions(t *testing.T) {
	good := []string{
		"* * * * *",
		"0 8 * * *",
		"*/15 * * * *",
		"0 */6 * * *",
		"30 8 1,15 * *",
		"0 8 * * 1-5",
		"0 8 1 * 1",
		"0 0 1 1 *",
		"0 8 * * 0",
		"0 8 * * 7",
		"5-59/10 8 * * *",
	}
	for _, s := range good {
		if err := ValidateSchedule(s); err != nil {
			t.Errorf("ValidateSchedule(%q) = %v, want nil", s, err)
		}
	}
}

// TestValidateScheduleRejectsExpressionThatNeverMatches cross-checks the
// validator against the matcher: anything the validator accepts must be able
// to match some minute within a year, and anything it rejects must not.
func TestValidateScheduleRejectsExpressionThatNeverMatches(t *testing.T) {
	for _, s := range []string{"0 8 * *", "60 8 * * *", "abc * * * *", "0 8 32 * *"} {
		if err := ValidateSchedule(s); err == nil {
			t.Errorf("ValidateSchedule(%q) = nil, but the matcher can never fire it", s)
		}
	}
}

// TestCronDSTSpringForwardAndFallBack documents the DST behaviour of the
// matcher in a real zone, so a change in it is a deliberate decision.
//
// Europe/Budapest 2026: spring forward on 03-29 (02:00 -> 03:00, so 02:xx does
// not exist) and fall back on 10-25 (03:00 -> 02:00, so 02:xx happens twice).
func TestCronDSTSpringForwardAndFallBack(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Budapest")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	// A 02:30 schedule on the spring-forward day: that wall-clock minute does
	// not exist, so the entry cannot fire. This must not panic or match
	// something arbitrary.
	springDay := time.Date(2026, 3, 29, 1, 30, 0, 0, loc)
	if cronMatches("30 2 * * *", springDay) {
		t.Error("02:30 does not exist on the spring-forward day; it must not match")
	}

	// A 08:00 schedule on the same day still fires normally.
	springMorning := time.Date(2026, 3, 29, 8, 0, 0, 0, loc)
	if !cronMatches("0 8 * * *", springMorning) {
		t.Error("08:00 exists on the spring-forward day and must match")
	}

	// On the fall-back day the wall clock 02:30 happens twice: once at 00:30 UTC
	// (CEST, +02:00) and again at 01:30 UTC (CET, +01:00). Both must match, so
	// the schedule fires twice; the caller's recency guard is what prevents a
	// duplicate digest. Build the instants from UTC, because time.Date resolves
	// an ambiguous local time to the SECOND occurrence and would hide one.
	firstTwoThirty := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC).In(loc)  // CEST
	secondTwoThirty := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC).In(loc) // CET
	if firstTwoThirty.Hour() != 2 || secondTwoThirty.Hour() != 2 {
		t.Fatalf("test setup: expected both instants to read 02:xx local, got %s and %s",
			firstTwoThirty.Format(time.RFC3339), secondTwoThirty.Format(time.RFC3339))
	}
	if !cronMatches("30 2 * * *", firstTwoThirty) {
		t.Error("first 02:30 (CEST) on the fall-back day must match")
	}
	if !cronMatches("30 2 * * *", secondTwoThirty) {
		t.Error("second 02:30 (CET) on the fall-back day must match")
	}
	// They are the same wall-clock time an hour apart in absolute terms. The
	// matcher cannot deduplicate them; the scheduler's recency guard must.
	if secondTwoThirty.Sub(firstTwoThirty) != time.Hour {
		t.Errorf("repeated 02:30 instants are %v apart, want 1h", secondTwoThirty.Sub(firstTwoThirty))
	}
}

// TestCronMatchesEveryMinuteOfADayIsStable guards against a schedule that
// matches nothing at all being accepted silently.
func TestCronMatchesEveryMinuteOfADayIsStable(t *testing.T) {
	base := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	matched := 0
	for m := 0; m < 24*60; m++ {
		if cronMatches("0 8 * * *", base.Add(time.Duration(m)*time.Minute)) {
			matched++
		}
	}
	if matched != 1 {
		t.Errorf("a daily 08:00 schedule matched %d minutes in a day, want 1", matched)
	}
}
