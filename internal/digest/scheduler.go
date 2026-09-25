package digest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"digesterr/internal/store"
)

type Scheduler struct {
	queries   *store.Queries
	generator *Generator
	// resolveLLM picks the LLM client for a digest config's owner: the
	// owner's per-user model override when set, otherwise the shared
	// default. Nil means "use the generator's default client" — callers
	// that predate per-user models pass nothing. Every scheduled run must
	// go through it, or digests silently generate on the wrong model.
	resolveLLM func(ctx context.Context, userID int64) Chatter
	// loc evaluates cron schedules. Nil means server-local time.
	loc *time.Location
	// now returns the current time. Nil means time.Now; tests override it to
	// place the scheduler at a specific instant.
	now func() time.Time
}

// Startup catch-up bounds.
const (
	// catchUpGrace is how late a missed occurrence may be and still be
	// generated at startup, and therefore also the backward search horizon.
	// A morning digest delivered later the same morning is useful; one
	// delivered after the next occurrence is nearly due is not — and by then
	// the next run is imminent anyway.
	catchUpGrace = 6 * time.Hour

	// recentGenerationWindow is how long after a digest result its schedule
	// counts as already served. It absorbs the DST fall-back duplicate
	// wall-clock minute, a manual "Generate" clicked just before a scheduled
	// minute, and restarts that land inside the same window.
	recentGenerationWindow = 30 * time.Minute
)

func NewScheduler(queries *store.Queries, generator *Generator, loc *time.Location) *Scheduler {
	if loc == nil {
		loc = time.Local
	}
	return &Scheduler{
		queries:   queries,
		generator: generator,
		loc:       loc,
		now:       time.Now,
	}
}

// SetClock overrides the scheduler's clock. Tests use it to pin the instant a
// tick or startup catch-up believes it is; production never calls it.
func (s *Scheduler) SetClock(now func() time.Time) {
	s.now = now
}

// currentTime is s.now with the nil fallback, so a Scheduler built as a bare
// struct literal (as some tests do) still works.
func (s *Scheduler) currentTime() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// SetLLMResolver installs the per-user LLM client resolver. The HTTP server
// sets this to its resolveLLMClient wrapper so scheduled runs honor the
// same per-user model override as manual generations.
func (s *Scheduler) SetLLMResolver(resolve func(ctx context.Context, userID int64) Chatter) {
	s.resolveLLM = resolve
}

func (s *Scheduler) Start(ctx context.Context) {
	slog.Info("starting digest scheduler", "timezone", s.loc.String())

	// Create the ticker before catch-up so its anchor is process start and
	// not delayed by however long recovery takes. Go buffers one tick, so a
	// fire during catch-up is delivered as soon as the loop is reached.
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// A restart does not guarantee a tick inside the scheduled minute. The
	// eviction/restart that prompted this catch-up landed at :03 while the
	// pod's fresh 1-minute ticker fired at :06, so the 05:00 occurrence was
	// never observed and the day's digest silently never existed. Catch up
	// before serving ticks so a missed occurrence is recovered, not lost.
	s.catchUp(ctx)

	for {
		select {
		case <-ticker.C:
			s.tick(ctx)
		case <-ctx.Done():
			slog.Info("digest scheduler stopped")
			return
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	configs, err := s.queries.ListAllDigestConfigs(ctx)
	if err != nil {
		slog.Error("failed to list digest configs for scheduling", "error", err)
		return
	}

	now := s.currentTime().In(s.loc)
	var wg sync.WaitGroup
	for _, cfg := range configs {
		if !cronMatches(cfg.Schedule, now) {
			continue
		}
		cfgLogger := slog.With("config_id", cfg.ID, "config_name", cfg.Name)
		if s.alreadyServed(ctx, cfg, now, cfgLogger) {
			continue
		}

		// Run each generation in its own goroutine so one slow LLM call
		// cannot delay (and skip the minute window of) other configs.
		wg.Add(1)
		go func(cfg store.DigestConfig, cfgLogger *slog.Logger) {
			defer wg.Done()
			s.generate(ctx, cfg, cfgLogger)
		}(cfg, cfgLogger)
	}
	wg.Wait()
}

// catchUp generates the most recent scheduled occurrence for every config
// whose digest is overdue, and does nothing for configs that already ran.
//
// The scheduler fires only when cronMatches is true at the instant the ticker
// fires, and the ticker is anchored to process start. So an occurrence is lost
// outright if no tick lands inside its minute — a restart, a crash, or (as in
// production) a pod eviction a few seconds before the tick. Nothing rechecks
// old minutes, so without this the day's digest could silently never exist.
//
// The current minute is included, not left to the ticker: a ticker anchored at
// T0 first fires in minute(T0)+1, so it can never observe minute(T0). A process
// that starts at 05:00:06 for a "0 5 * * *" schedule has exactly that minute as
// its only chance.
//
// It runs once at Start, before the ticker, and reuses the same run path as a
// normal tick so the per-user LLM resolution, the in-flight lock and the
// last_error bookkeeping all behave identically.
func (s *Scheduler) catchUp(ctx context.Context) {
	configs, err := s.queries.ListAllDigestConfigs(ctx)
	if err != nil {
		slog.Error("failed to list digest configs for scheduling", "error", err)
		return
	}

	now := s.currentTime().In(s.loc)
	var wg sync.WaitGroup
	for _, cfg := range configs {
		missed, ok := previousOccurrence(cfg.Schedule, now)
		if !ok {
			continue
		}
		// Too stale to be useful: a digest from most of a day ago would be
		// presented as today's, and the next occurrence is imminent anyway.
		if now.Sub(missed) > catchUpGrace {
			continue
		}

		cfgLogger := slog.With("config_id", cfg.ID, "config_name", cfg.Name)
		// Anchored on the occurrence, not the clock: see servedSince.
		if s.servedSince(ctx, cfg, missed, cfgLogger) {
			continue
		}
		cfgLogger.Info("catching up missed scheduled digest generation",
			"missed_at", missed.Format(time.RFC3339),
			"late_by", now.Sub(missed).Round(time.Second).String())

		wg.Add(1)
		go func(cfg store.DigestConfig, cfgLogger *slog.Logger) {
			defer wg.Done()
			s.generate(ctx, cfg, cfgLogger)
		}(cfg, cfgLogger)
	}
	wg.Wait()
}

// previousOccurrence returns the most recent minute at or before now that
// matches schedule, searching back at most catchUpGrace minutes. The cron
// matcher is a predicate with no inverse, so this scans backwards minute by
// minute; a schedule whose last match is older than the horizon reports false,
// which is what bounds both the scan and the recovery.
func previousOccurrence(schedule string, now time.Time) (time.Time, bool) {
	// Start at the current minute: an occurrence that is due right now is the
	// one catch-up exists to rescue.
	t := now.Truncate(time.Minute)
	maxSteps := int(catchUpGrace/time.Minute) + 1
	for i := 0; i < maxSteps; i++ {
		if cronMatches(schedule, t) {
			return t, true
		}
		t = t.Add(-time.Minute)
	}
	return time.Time{}, false
}

// alreadyServed reports whether a digest ran recently enough that a catch-up
// would be a duplicate. The same recency rule guards the normal tick.
func (s *Scheduler) alreadyServed(ctx context.Context, cfg store.DigestConfig, now time.Time, cfgLogger *slog.Logger) bool {
	return s.servedSince(ctx, cfg, now.Add(-recentGenerationWindow), cfgLogger)
}

// servedSince reports whether a digest result exists at or after since.
//
// Catch-up must ask this rather than the recency window: the pod restarts on
// an hourly cadence, so a digest generated at 05:00:53 is already 59 minutes
// old when the 06:00 restart runs catch-up. A 30-minute "recently generated"
// window would call that unserved and generate a duplicate of a digest the
// user already has — once per hour for as long as the occurrence stays inside
// the grace window. Anchoring on the occurrence instead answers the question
// catch-up actually asks: has this occurrence been served?
func (s *Scheduler) servedSince(ctx context.Context, cfg store.DigestConfig, since time.Time, cfgLogger *slog.Logger) bool {
	latest, err := s.queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID,
		UserID:   cfg.UserID,
	})
	if err != nil || latest.ID == 0 {
		return false
	}
	// created_at is stored by SQLite as UTC; compare absolute instants so the
	// result is identical whatever zone the scheduler evaluates cron in.
	if !latest.CreatedAt.UTC().Before(since.UTC()) {
		cfgLogger.Debug("skipping digest, occurrence already has a result",
			"result_created_at", latest.CreatedAt.UTC().Format(time.RFC3339))
		return true
	}
	return false
}

// generate runs one scheduled generation for cfg and records the outcome:
// success clears any stored error, failure stores a truncated message, and
// ErrGenerationInProgress is neither. Shared by tick and catchUp so the two
// paths cannot drift.
func (s *Scheduler) generate(ctx context.Context, cfg store.DigestConfig, cfgLogger *slog.Logger) {
	cfgLogger.Info("scheduled digest generation")
	// The owner's LLM client is resolved per config: Generate's variadic
	// override is opt-in, so omitting it would silently run every scheduled
	// digest on the generator's default model instead of the owner's
	// per-user override.
	var llm Chatter
	if s.resolveLLM != nil {
		llm = s.resolveLLM(ctx, cfg.UserID)
	}
	var err error
	if llm != nil {
		_, err = s.generator.Generate(ctx, cfg, llm)
	} else {
		_, err = s.generator.Generate(ctx, cfg)
	}
	if errors.Is(err, ErrGenerationInProgress) {
		// Not a failure: the previous run for this config is still going.
		// Writing it to last_error would show the user an error they did not
		// cause, on a config that is working correctly.
		cfgLogger.Info("skipping scheduled generation, one is already in progress")
	} else if err != nil {
		cfgLogger.Error("scheduled digest generation failed", "error", err)
		// Truncated like the manual path: this column is rendered in the UI,
		// and upstream LLM bodies are unbounded.
		msg := err.Error()
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		s.queries.UpdateDigestConfigError(ctx, store.UpdateDigestConfigErrorParams{
			ID:    cfg.ID,
			Error: msg,
		})
	} else {
		_ = s.queries.ClearDigestConfigError(ctx, cfg.ID)
	}
}

// cronMatches reports whether a 5-field cron expression matches time t.
// Weekday follows cron convention: 0 and 7 both mean Sunday.
//
// Day-of-month and day-of-week follow Vixie cron: when BOTH are restricted
// (neither is "*"), the entry fires when EITHER matches. This is the documented
// crontab(5) rule and the behaviour every other cron implementation has, so
// "0 8 1 * 1" means "the 1st of the month, and every Monday" rather than the
// nearly-never "a Monday that is also the 1st".
func cronMatches(schedule string, t time.Time) bool {
	parts := strings.Fields(schedule)
	if len(parts) != 5 {
		return false
	}
	if !cronFieldMatches(parts[0], t.Minute(), 0) ||
		!cronFieldMatches(parts[1], t.Hour(), 1) ||
		!cronFieldMatches(parts[3], int(t.Month()), 3) {
		return false
	}

	domRestricted := strings.TrimSpace(parts[2]) != "*"
	dowRestricted := strings.TrimSpace(parts[4]) != "*"
	domMatch := cronFieldMatches(parts[2], t.Day(), 2)
	dowMatch := cronFieldMatches(parts[4], cronWeekday(int(t.Weekday())), 4)

	switch {
	case domRestricted && dowRestricted:
		return domMatch || dowMatch
	case domRestricted:
		return domMatch
	case dowRestricted:
		return dowMatch
	default:
		return true
	}
}

// ValidateSchedule reports whether a 5-field cron expression can ever run. The
// scheduler silently ignores an expression it cannot parse, so an invalid
// schedule means a digest that simply never fires — invisible to the user.
// Config create/update must reject it instead.
func ValidateSchedule(schedule string) error {
	parts := strings.Fields(schedule)
	if len(parts) != 5 {
		return fmt.Errorf("schedule must have 5 fields (minute hour day-of-month month day-of-week), got %d", len(parts))
	}
	names := []string{"minute", "hour", "day-of-month", "month", "day-of-week"}
	for i, part := range parts {
		if err := validateCronField(part, i); err != nil {
			return fmt.Errorf("invalid %s field %q: %w", names[i], part, err)
		}
	}
	return nil
}

// validateCronField checks one field's syntax and bounds.
func validateCronField(field string, fieldIdx int) error {
	if field == "*" {
		return nil
	}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return errors.New("empty list element")
		}

		if idx := strings.Index(part, "/"); idx >= 0 {
			s, err := strconv.Atoi(part[idx+1:])
			if err != nil {
				return fmt.Errorf("step is not a number: %q", part[idx+1:])
			}
			if s < 1 {
				return fmt.Errorf("step must be >= 1, got %d", s)
			}
			part = part[:idx]
		}

		lo, hi := fieldBounds(fieldIdx)
		if part == "" || part == "*" {
			continue
		}
		if idx := strings.Index(part, "-"); idx >= 0 {
			l, err := strconv.Atoi(part[:idx])
			if err != nil {
				return fmt.Errorf("range start is not a number: %q", part[:idx])
			}
			h, err := strconv.Atoi(part[idx+1:])
			if err != nil {
				return fmt.Errorf("range end is not a number: %q", part[idx+1:])
			}
			if l < lo || h > hi {
				return fmt.Errorf("range %d-%d outside %d-%d", l, h, lo, hi)
			}
			if l > h {
				return fmt.Errorf("range start %d is after end %d", l, h)
			}
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return fmt.Errorf("not a number: %q", part)
		}
		if v < lo || v > hi {
			return fmt.Errorf("value %d outside %d-%d", v, lo, hi)
		}
	}
	return nil
}

// cronWeekday maps Go's Weekday (Sunday=0..Saturday=6) to cron's field
// where both 0 and 7 denote Sunday. Returns the Go value unchanged except
// for a special-case check in cronPartMatches via fieldIdx 4.
func cronWeekday(w int) int {
	return w
}

// fieldBounds returns the valid [min, max] for a cron field index:
// minute, hour, day-of-month, month, day-of-week.
func fieldBounds(fieldIdx int) (lo, hi int) {
	switch fieldIdx {
	case 0:
		return 0, 59
	case 1:
		return 0, 23
	case 2:
		return 1, 31
	case 3:
		return 1, 12
	default: // day of week
		return 0, 7
	}
}

func cronFieldMatches(field string, value int, fieldIdx int) bool {
	if field == "*" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if cronPartMatches(strings.TrimSpace(part), value, fieldIdx) {
			return true
		}
	}
	return false
}

func cronPartMatches(part string, value int, fieldIdx int) bool {
	if part == "*" {
		return true
	}

	step := 1
	if idx := strings.Index(part, "/"); idx >= 0 {
		s, err := strconv.Atoi(part[idx+1:])
		if err != nil || s < 1 {
			return false
		}
		step = s
		part = part[:idx]
	}

	lo, hi := fieldBounds(fieldIdx)
	if part != "" && part != "*" {
		if idx := strings.Index(part, "-"); idx >= 0 {
			l, err := strconv.Atoi(part[:idx])
			if err != nil {
				return false
			}
			h, err := strconv.Atoi(part[idx+1:])
			if err != nil {
				return false
			}
			lo = l
			hi = h
		} else {
			v, err := strconv.Atoi(part)
			if err != nil {
				return false
			}
			// Cron weekday: both 0 and 7 mean Sunday.
			if fieldIdx == 4 && v == 7 {
				v = 0
			}
			return value == v
		}
	}

	// Reject out-of-range values instead of silently accepting e.g. 1-100.
	minLo, maxHi := fieldBounds(fieldIdx)
	if lo < minLo || hi > maxHi || lo > hi {
		return false
	}

	if value < lo || value > hi {
		return false
	}
	return (value-lo)%step == 0
}
