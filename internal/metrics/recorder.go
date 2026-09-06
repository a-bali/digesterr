// Package metrics provides an in-memory ring buffer for recording and
// querying HTTP request metrics (count, bytes, status, duration) grouped
// by reason, feed, host, and status.
package metrics

import (
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

// Record is a single HTTP request metric entry.
type Record struct {
	Timestamp  time.Time `json:"timestamp"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Host       string    `json:"host"`
	StatusCode int       `json:"status_code"`
	Bytes      int64     `json:"bytes"`
	DurationMs int64     `json:"duration_ms"`
	Reason     string    `json:"reason"` // "fetch", "enrichment", "extraction", "server"
	FeedID     int64     `json:"feed_id"`
	FeedName   string    `json:"feed_name"`
	FeedType   string    `json:"feed_type"`
	Error      string    `json:"error,omitempty"`
}

// GroupKey identifies a unique combination of grouping dimensions.
type GroupKey struct {
	Reason   string `json:"reason"`
	Feed     string `json:"feed"`
	Host     string `json:"host"`
	Status   string `json:"status"`
}

// GroupStats is an aggregated row for a group.
type GroupStats struct {
	GroupKey
	Count  int64  `json:"count"`
	Bytes  int64  `json:"bytes"`
	Errors int64  `json:"errors"`
	AvgMs  int64  `json:"avg_ms"`
}

// TimeBucket is a time-bucketed aggregate for the chart.
type TimeBucket struct {
	Time   time.Time `json:"time"`
	Count  int64     `json:"count"`
	Bytes  int64     `json:"bytes"`
	Errors int64     `json:"errors"`
}

// Summary is the full metrics response.
type Summary struct {
	TotalRequests int64        `json:"total_requests"`
	TotalBytes    int64        `json:"total_bytes"`
	SuccessCount  int64        `json:"success_count"`
	ErrorCount    int64        `json:"error_count"`
	AvgDurationMs float64      `json:"avg_duration_ms"`
	Groups        []GroupStats `json:"groups"`
	Buckets       []TimeBucket `json:"buckets"`
}

// Recorder is a thread-safe ring buffer of metric records.
type Recorder struct {
	mu        sync.RWMutex
	records   []Record
	next      int
	size      int
	transport *Transport
}

// New returns a recorder holding at most capacity records.
func New(capacity int) *Recorder {
	if capacity < 1 {
		capacity = 1
	}
	return &Recorder{
		records:   make([]Record, capacity),
		size:      capacity,
		transport: NewTransport(http.DefaultTransport),
	}
}

// Transport returns the shared HTTP transport that records response bytes.
func (r *Recorder) Transport() *Transport {
	return r.transport
}

// Reset clears all recorded metrics.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = make([]Record, r.size)
	r.next = 0
	if r.transport != nil {
		r.transport.ResetBytes()
	}
}

// Record appends a metric entry.
func (r *Recorder) Record(rec Record) {
	if rec.Host == "" && rec.URL != "" {
		u, err := url.Parse(rec.URL)
		if err == nil {
			rec.Host = u.Hostname()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[r.next] = rec
	r.next = (r.next + 1) % r.size
}

// Query returns records since the given time, optionally filtered by reason.
func (r *Recorder) Query(since time.Time, reason string) []Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Record
	for i := 0; i < r.size; i++ {
		idx := (r.next - 1 - i + 2*r.size) % r.size
		rec := r.records[idx]
		if rec.Timestamp.IsZero() || rec.Timestamp.Before(since) {
			continue
		}
		if reason != "" && rec.Reason != reason {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Pivot aggregates records by the requested dimensions and returns grouped
// stats sorted by count descending. dims is a set of dimension names:
// "reason", "feed", "host", "status".
func (r *Recorder) Pivot(since time.Time, dims []string) []GroupStats {
	records := r.Query(since, "")
	type key struct {
		reason string
		feed   string
		host   string
		status string
	}
	type agg struct {
		count   int64
		bytes   int64
		errors  int64
		totalMs int64
	}
	aggMap := make(map[key]*agg)
	for _, rec := range records {
		gk := key{}
		for _, d := range dims {
			switch d {
			case "reason":
				gk.reason = rec.Reason
			case "feed":
				if rec.FeedName != "" {
					gk.feed = rec.FeedName
				} else {
					gk.feed = "—"
				}
			case "host":
				gk.host = rec.Host
			case "status":
				gk.status = statusBucket(rec.StatusCode)
			}
		}
		a, ok := aggMap[gk]
		if !ok {
			a = &agg{}
			aggMap[gk] = a
		}
		a.count++
		a.bytes += rec.Bytes
		a.totalMs += rec.DurationMs
		if rec.StatusCode >= 400 || rec.Error != "" {
			a.errors++
		}
	}

	out := make([]GroupStats, 0, len(aggMap))
	for gk, a := range aggMap {
		avgMs := int64(0)
		if a.count > 0 {
			avgMs = a.totalMs / a.count
		}
		out = append(out, GroupStats{
			GroupKey: GroupKey{
				Reason: gk.reason,
				Feed:   gk.feed,
				Host:   gk.host,
				Status: gk.status,
			},
			Count:  a.count,
			Bytes:  a.bytes,
			Errors: a.errors,
			AvgMs:  avgMs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// Buckets groups records into time buckets of the given interval.
func (r *Recorder) Buckets(since time.Time, interval time.Duration) []TimeBucket {
	records := r.Query(since, "")
	type bucketKey struct {
		year  int
		month time.Month
		day   int
		hour  int
		min   int
	}
	bucketMap := make(map[bucketKey]*TimeBucket)

	for _, rec := range records {
		t := rec.Timestamp.Truncate(interval)
		bk := bucketKey{year: t.Year(), month: t.Month(), day: t.Day(), hour: t.Hour(), min: t.Minute()}
		b, ok := bucketMap[bk]
		if !ok {
			b = &TimeBucket{Time: t}
			bucketMap[bk] = b
		}
		b.Count++
		b.Bytes += rec.Bytes
		if rec.StatusCode >= 400 || rec.Error != "" {
			b.Errors++
		}
	}

	out := make([]TimeBucket, 0, len(bucketMap))
	for _, b := range bucketMap {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// Summary aggregates all records since the given time.
func (r *Recorder) Summary(since time.Time, dims []string, bucketInterval time.Duration) Summary {
	records := r.Query(since, "")
	var totalBytes, totalMs, successCount, errorCount int64
	for _, rec := range records {
		totalBytes += rec.Bytes
		totalMs += rec.DurationMs
		if rec.StatusCode >= 400 || rec.Error != "" {
			errorCount++
		} else {
			successCount++
		}
	}
	avgMs := float64(0)
	if n := int64(len(records)); n > 0 {
		avgMs = float64(totalMs / n)
	}
	return Summary{
		TotalRequests: int64(len(records)),
		TotalBytes:    totalBytes,
		SuccessCount:  successCount,
		ErrorCount:    errorCount,
		AvgDurationMs: avgMs,
		Groups:        r.Pivot(since, dims),
		Buckets:       r.Buckets(since, bucketInterval),
	}
}

func statusBucket(code int) string {
	switch {
	case code == 0:
		return "error"
	case code < 200:
		return "1xx"
	case code < 300:
		return "2xx"
	case code < 400:
		return "3xx"
	case code < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
