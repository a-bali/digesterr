package digest

import (
	"context"
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
}

func NewScheduler(queries *store.Queries, generator *Generator, loc *time.Location) *Scheduler {
	if loc == nil {
		loc = time.Local
	}
	return &Scheduler{
		queries:   queries,
		generator: generator,
		loc:       loc,
	}
}

// SetLLMResolver installs the per-user LLM client resolver. The HTTP server
// sets this to its resolveLLMClient wrapper so scheduled runs honor the
// same per-user model override as manual generations.
func (s *Scheduler) SetLLMResolver(resolve func(ctx context.Context, userID int64) Chatter) {
	s.resolveLLM = resolve
}

func (s *Scheduler) Start(ctx context.Context) {
	slog.Info("starting digest scheduler", "timezone", s.loc.String())
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

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

	now := time.Now().In(s.loc)
	var wg sync.WaitGroup
	for _, cfg := range configs {
		if !cronMatches(cfg.Schedule, now) {
			continue
		}
		cfgLogger := slog.With("config_id", cfg.ID, "config_name", cfg.Name)

		recently, err := s.queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
			ConfigID: cfg.ID,
			UserID:   cfg.UserID,
		})
		if err == nil && recently.ID > 0 {
			if now.Sub(recently.CreatedAt) < 30*time.Minute {
				cfgLogger.Debug("skipping digest, recently generated")
				continue
			}
		}

		// Run each generation in its own goroutine so one slow LLM call
		// cannot delay (and skip the minute window of) other configs.
		//
		// The owner's LLM client is resolved per config: Generate's variadic
		// override is opt-in, so omitting it would silently run every
		// scheduled digest on the generator's default model instead of the
		// owner's per-user override.
		wg.Add(1)
		go func(cfg store.DigestConfig, cfgLogger *slog.Logger) {
			defer wg.Done()
			cfgLogger.Info("scheduled digest generation")
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
			if err != nil {
				cfgLogger.Error("scheduled digest generation failed", "error", err)
				s.queries.UpdateDigestConfigError(ctx, store.UpdateDigestConfigErrorParams{
					ID:    cfg.ID,
					Error: err.Error(),
				})
			} else {
				_ = s.queries.ClearDigestConfigError(ctx, cfg.ID)
			}
		}(cfg, cfgLogger)
	}
	wg.Wait()
}

// cronMatches reports whether a 5-field cron expression matches time t.
// Weekday follows cron convention: 0 and 7 both mean Sunday.
func cronMatches(schedule string, t time.Time) bool {
	parts := strings.Fields(schedule)
	if len(parts) != 5 {
		return false
	}
	return cronFieldMatches(parts[0], t.Minute(), 0) &&
		cronFieldMatches(parts[1], t.Hour(), 1) &&
		cronFieldMatches(parts[2], t.Day(), 2) &&
		cronFieldMatches(parts[3], int(t.Month()), 3) &&
		cronFieldMatches(parts[4], cronWeekday(int(t.Weekday())), 4)
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
