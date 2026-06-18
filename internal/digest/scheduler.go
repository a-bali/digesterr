package digest

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/store"
)

type Scheduler struct {
	queries   *store.Queries
	generator *Generator
}

func NewScheduler(queries *store.Queries, generator *Generator) *Scheduler {
	return &Scheduler{
		queries:   queries,
		generator: generator,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	slog.Info("starting digest scheduler")
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

	now := time.Now()
	for _, cfg := range configs {
		if !cronMatches(cfg.Schedule, now) {
			continue
		}

		recently, err := s.queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
			ConfigID: cfg.ID,
			UserID:   cfg.UserID,
		})
		if err == nil && recently.ID > 0 {
			if now.Sub(recently.CreatedAt) < 30*time.Minute {
				slog.Debug("skipping digest, recently generated", "config_id", cfg.ID)
				continue
			}
		}

		slog.Info("scheduled digest generation", "config_id", cfg.ID)
		_, err = s.generator.Generate(ctx, cfg)
		if err != nil {
			slog.Error("scheduled digest generation failed", "config_id", cfg.ID, "error", err)
		}
	}
}

func cronMatches(schedule string, t time.Time) bool {
	parts := strings.Fields(schedule)
	if len(parts) != 5 {
		return false
	}
	return cronFieldMatches(parts[0], t.Minute()) &&
		cronFieldMatches(parts[1], t.Hour()) &&
		cronFieldMatches(parts[2], t.Day()) &&
		cronFieldMatches(parts[3], int(t.Month())) &&
		cronFieldMatches(parts[4], int(t.Weekday()))
}

func cronFieldMatches(field string, value int) bool {
	if field == "*" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if cronPartMatches(strings.TrimSpace(part), value) {
			return true
		}
	}
	return false
}

func cronPartMatches(part string, value int) bool {
	if part == "*" {
		return true
	}

	step := 1
	if idx := strings.Index(part, "/"); idx >= 0 {
		s, err := strconv.Atoi(part[idx+1:])
		if err != nil {
			return false
		}
		step = s
		part = part[:idx]
	}

	lo := 0
	hi := 59
	if part != "*" {
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
			return value == v
		}
	}

	if value < lo || value > hi {
		return false
	}
	return (value-lo)%step == 0
}
