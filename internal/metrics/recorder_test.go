package metrics

import (
	"testing"
	"time"
)

func TestRecordAndQuery(t *testing.T) {
	r := New(100)
	now := time.Now()

	r.Record(Record{Timestamp: now, StatusCode: 200, Bytes: 100, Reason: "fetch", Host: "example.com"})
	r.Record(Record{Timestamp: now.Add(time.Minute), StatusCode: 500, Bytes: 50, Reason: "enrichment", Host: "api.test.com", Error: "timeout"})
	r.Record(Record{Timestamp: now.Add(2 * time.Minute), StatusCode: 200, Bytes: 200, Reason: "fetch", Host: "example.com"})

	// Query all
	all := r.Query(now.Add(-time.Second), "")
	if len(all) != 3 {
		t.Fatalf("expected 3 records, got %d", len(all))
	}

	// Query by reason
	fetches := r.Query(now.Add(-time.Second), "fetch")
	if len(fetches) != 2 {
		t.Fatalf("expected 2 fetch records, got %d", len(fetches))
	}

	// Query with time filter — now+90s is between the 2nd and 3rd records
	recent := r.Query(now.Add(90*time.Second), "")
	if len(recent) != 1 {
		t.Fatalf("expected 1 recent record, got %d", len(recent))
	}
}

func TestRingBufferEviction(t *testing.T) {
	r := New(5)
	now := time.Now()

	for i := 0; i < 10; i++ {
		r.Record(Record{Timestamp: now.Add(time.Duration(i) * time.Second), StatusCode: 200, Bytes: int64(i)})
	}

	all := r.Query(now.Add(-time.Second), "")
	if len(all) != 5 {
		t.Fatalf("expected 5 records (ring buffer), got %d", len(all))
	}
	// Should be the last 5 (indices 5-9), newest first
	if all[0].Bytes != 9 {
		t.Fatalf("expected newest record (bytes=9), got %d", all[0].Bytes)
	}
}

func TestSummary(t *testing.T) {
	r := New(100)
	now := time.Now()

	r.Record(Record{Timestamp: now, StatusCode: 200, Bytes: 1000, DurationMs: 100, Reason: "fetch", Host: "a.com", FeedName: "HN"})
	r.Record(Record{Timestamp: now, StatusCode: 200, Bytes: 500, DurationMs: 200, Reason: "fetch", Host: "a.com", FeedName: "HN"})
	r.Record(Record{Timestamp: now, StatusCode: 500, Bytes: 0, DurationMs: 500, Reason: "enrichment", Host: "b.com", FeedName: "Reddit", Error: "fail"})

	sum := r.Summary(now.Add(-time.Second), []string{"reason", "feed"}, time.Minute)

	if sum.TotalRequests != 3 {
		t.Errorf("total_requests = %d, want 3", sum.TotalRequests)
	}
	if sum.TotalBytes != 1500 {
		t.Errorf("total_bytes = %d, want 1500", sum.TotalBytes)
	}
	if sum.SuccessCount != 2 {
		t.Errorf("success_count = %d, want 2", sum.SuccessCount)
	}
	if sum.ErrorCount != 1 {
		t.Errorf("error_count = %d, want 1", sum.ErrorCount)
	}
	// avg_ms = (100+200+500)/3 = 266 (integer division)
	if sum.AvgDurationMs != 266 {
		t.Errorf("avg_duration_ms = %f, want 266", sum.AvgDurationMs)
	}
	if len(sum.Groups) == 0 {
		t.Fatal("expected groups")
	}
	if len(sum.Buckets) == 0 {
		t.Fatal("expected buckets")
	}
}

func TestPivot(t *testing.T) {
	r := New(100)
	now := time.Now()

	r.Record(Record{Timestamp: now, StatusCode: 200, Bytes: 100, Reason: "fetch", Host: "a.com", FeedName: "HN"})
	r.Record(Record{Timestamp: now, StatusCode: 200, Bytes: 200, Reason: "fetch", Host: "a.com", FeedName: "HN"})
	r.Record(Record{Timestamp: now, StatusCode: 500, Bytes: 0, Reason: "fetch", Host: "b.com", FeedName: "Reddit", Error: "fail"})
	r.Record(Record{Timestamp: now, StatusCode: 200, Bytes: 300, Reason: "enrichment", Host: "c.com", FeedName: "HN"})

	groups := r.Pivot(now.Add(-time.Second), []string{"reason"})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	// fetch should be first (3 records)
	if groups[0].Reason != "fetch" || groups[0].Count != 3 {
		t.Errorf("first group: reason=%s count=%d, want fetch/3", groups[0].Reason, groups[0].Count)
	}
	if groups[0].Errors != 1 {
		t.Errorf("fetch errors = %d, want 1", groups[0].Errors)
	}
}

func TestHostExtraction(t *testing.T) {
	r := New(100)
	now := time.Now()

	r.Record(Record{Timestamp: now, URL: "https://news.ycombinator.com/item?id=123", StatusCode: 200})
	records := r.Query(now.Add(-time.Second), "")
	if records[0].Host != "news.ycombinator.com" {
		t.Errorf("host = %q, want news.ycombinator.com", records[0].Host)
	}
}

func TestBareHostnameExtraction(t *testing.T) {
	r := New(100)
	now := time.Now()

	// Bare hostname without scheme: url.Parse treats it as a path, so host is empty.
	r.Record(Record{Timestamp: now, URL: "sdf", StatusCode: 200})
	records := r.Query(now.Add(-time.Second), "")
	if records[0].Host != "" {
		t.Errorf("host = %q, want empty for bare URL", records[0].Host)
	}

	// Full URL with scheme: host is extracted correctly.
	r.Record(Record{Timestamp: now, URL: "https://hacker-news.firebaseio.com/v0", StatusCode: 200})
	records = r.Query(now.Add(-time.Second), "")
	if records[0].Host != "hacker-news.firebaseio.com" {
		t.Errorf("host = %q, want hacker-news.firebaseio.com", records[0].Host)
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := New(1000)
	now := time.Now()
	done := make(chan struct{})

	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				r.Record(Record{
					Timestamp:  now,
					StatusCode: 200,
					Bytes:      100,
					Reason:     "fetch",
				})
			}
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	all := r.Query(now.Add(-time.Second), "")
	if len(all) != 1000 {
		t.Errorf("expected 1000 records, got %d", len(all))
	}
}
