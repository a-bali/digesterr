package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/feed"
)

func (s *Server) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Parse since parameter (default: 6h ago).
	sinceStr := q.Get("since")
	var since time.Time
	if sinceStr != "" {
		var err error
		since, err = time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since parameter; use RFC3339")
			return
		}
	} else {
		since = time.Now().Add(-6 * time.Hour)
	}

	// Parse group_by dimensions.
	dims := []string{"reason", "feed", "status"} // defaults
	if gb := q.Get("group_by"); gb != "" {
		dims = strings.Split(gb, ",")
		// Validate dimensions.
		valid := map[string]bool{"reason": true, "feed": true, "host": true, "status": true}
		for _, d := range dims {
			d = strings.TrimSpace(d)
			if !valid[d] {
				writeError(w, http.StatusBadRequest, "invalid dimension: "+d)
				return
			}
		}
	}

	// Parse bucket interval.
	interval := 5 * time.Minute // default
	if bi := q.Get("bucket"); bi != "" {
		switch bi {
		case "1m":
			interval = time.Minute
		case "5m":
			interval = 5 * time.Minute
		case "15m":
			interval = 15 * time.Minute
		case "1h":
			interval = time.Hour
		case "6h":
			interval = 6 * time.Hour
		default:
			// Try parsing as duration.
			d, err := time.ParseDuration(bi)
			if err != nil || d < time.Minute {
				writeError(w, http.StatusBadRequest, "invalid bucket; use 1m, 5m, 15m, 1h, 6h")
				return
			}
			interval = d
		}
	} else {
		// Auto-select based on timespan.
		span := time.Since(since)
		switch {
		case span <= 12*time.Hour:
			interval = 5 * time.Minute
		case span <= 48*time.Hour:
			interval = 15 * time.Minute
		default:
			interval = time.Hour
		}
	}

	// Limit dimensions to avoid combinatorial explosion.
	if len(dims) > 4 {
		dims = dims[:4]
	}

	summary := s.metrics.Summary(since, dims, interval)

	// Add group-level percentage.
	total := summary.TotalRequests
	type enrichedGroup struct {
		Reason   string `json:"reason"`
		Feed     string `json:"feed"`
		Host     string `json:"host"`
		Status   string `json:"status"`
		Count    int64  `json:"count"`
		Bytes    int64  `json:"bytes"`
		Errors   int64  `json:"errors"`
		AvgMs    int64  `json:"avg_ms"`
		PctTotal float64 `json:"pct_total"`
	}
	groups := make([]enrichedGroup, len(summary.Groups))
	for i, g := range summary.Groups {
		pct := float64(0)
		if total > 0 {
			pct = float64(g.Count) / float64(total) * 100
		}
		groups[i] = enrichedGroup{
			Reason: g.Reason, Feed: g.Feed, Host: g.Host, Status: g.Status,
			Count: g.Count, Bytes: g.Bytes, Errors: g.Errors, AvgMs: g.AvgMs,
			PctTotal: pct,
		}
	}

	// Rate-limit headroom per provider, reported alongside the counters so
	// quota exhaustion is visible before it shows up as failed polls. Only
	// providers that actually advertise a budget are listed.
	var quotas []feed.QuotaSnapshot
	if s.poller != nil {
		quotas = s.poller.Quotas()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_requests":      summary.TotalRequests,
		"total_http_requests": summary.TotalHTTPRequests,
		"total_bytes":         summary.TotalBytes,
		"success_count":       summary.SuccessCount,
		"error_count":         summary.ErrorCount,
		"avg_duration_ms":     summary.AvgDurationMs,
		"groups":              groups,
		"buckets":             summary.Buckets,
		"quotas":              quotas,
	})
}

func parseOrDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}
