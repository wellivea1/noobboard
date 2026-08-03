package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/models"
)

func TestSummariseLatencyExcludesFailuresFromTimings(t *testing.T) {
	// A failed probe measures the timeout, not the link. Letting it into the
	// median would hide the next real spike, so it is counted and excluded.
	bucket := SummariseLatency("internet", time.Now().UTC(), []int64{30, 10, 20}, 2)
	if bucket.MinMS != 10 || bucket.MedianMS != 20 || bucket.MaxMS != 30 {
		t.Fatalf("bucket timings = %#v, want min 10 median 20 max 30", bucket)
	}
	if bucket.Failures != 2 || bucket.Samples != 5 {
		t.Fatalf("bucket counts = %#v, want 2 failures of 5 samples", bucket)
	}
}

func TestSummariseLatencyKeepsAnAllFailureBucket(t *testing.T) {
	// An all-failure window is the most interesting thing on the chart, so it
	// must produce a row rather than vanishing.
	bucket := SummariseLatency("internet", time.Now().UTC(), nil, 6)
	if bucket.Samples != 6 || bucket.Failures != 6 {
		t.Fatalf("bucket = %#v, want six failed samples", bucket)
	}
	if bucket.MedianMS != 0 || bucket.MaxMS != 0 {
		t.Fatalf("bucket = %#v, want no timings when every sample failed", bucket)
	}
}

func TestBucketStartSnapsToAFixedGrid(t *testing.T) {
	// Every writer has to agree on boundaries regardless of when it runs, or
	// two partial buckets appear for the same window.
	base := time.Date(2026, 8, 2, 14, 37, 42, 0, time.UTC)
	if got := BucketStart(base); !got.Equal(time.Date(2026, 8, 2, 14, 35, 0, 0, time.UTC)) {
		t.Fatalf("BucketStart = %s, want 14:35:00", got)
	}
	if !BucketStart(base).Equal(BucketStart(base.Add(90 * time.Second))) {
		t.Fatal("two times inside one window produced different bucket starts")
	}
}

func TestMetricStoreRoundTripsFiltersAndPrunes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latency.jsonl")
	store, err := OpenFileMetricStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := models.LatencyBucket{Subject: "internet", At: now.Add(-30 * 24 * time.Hour), MedianMS: 40, Samples: 1}
	recent := models.LatencyBucket{Subject: "internet", At: now.Add(-time.Hour), MedianMS: 30, Samples: 1}
	other := models.LatencyBucket{Subject: "dns", At: now.Add(-time.Hour), MedianMS: 12, Samples: 1}
	if err := store.AppendLatency([]models.LatencyBucket{old, recent, other}); err != nil {
		t.Fatal(err)
	}

	got, err := store.QueryLatency(MetricFilter{Subject: "internet"})
	if err != nil || len(got) != 2 {
		t.Fatalf("subject filter returned %#v (%v)", got, err)
	}
	got, err = store.QueryLatency(MetricFilter{Subject: "internet", Since: now.Add(-2 * time.Hour)})
	if err != nil || len(got) != 1 || got[0].MedianMS != 30 {
		t.Fatalf("since filter returned %#v (%v)", got, err)
	}

	if err := store.PruneLatency(config.RetentionConfig{MaxLatencyBucketAge: 14 * 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	// Reopening proves the prune reached disk, not just the in-memory copy.
	reopened, err := OpenFileMetricStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err = reopened.QueryLatency(MetricFilter{})
	if err != nil || len(got) != 2 {
		t.Fatalf("after prune and reopen: %#v (%v), want the two recent buckets", got, err)
	}
}

func TestQueryLatencyLimitKeepsTheMostRecent(t *testing.T) {
	// A window is always the newest N buckets; trimming from the wrong end would
	// silently chart ancient history.
	path := filepath.Join(t.TempDir(), "latency.jsonl")
	store, err := OpenFileMetricStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var buckets []models.LatencyBucket
	for i := 0; i < 10; i++ {
		buckets = append(buckets, models.LatencyBucket{
			Subject: "internet", At: now.Add(time.Duration(i-10) * time.Hour), MedianMS: int64(i), Samples: 1,
		})
	}
	if err := store.AppendLatency(buckets); err != nil {
		t.Fatal(err)
	}
	got, err := store.QueryLatency(MetricFilter{Subject: "internet", Limit: 3})
	if err != nil || len(got) != 3 {
		t.Fatalf("limited query = %#v (%v)", got, err)
	}
	if got[len(got)-1].MedianMS != 9 {
		t.Fatalf("last bucket = %d, want the newest (9)", got[len(got)-1].MedianMS)
	}
}

func TestFileMetricStoreClearLatency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latency.jsonl")
	store, err := OpenFileMetricStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.AppendLatency([]models.LatencyBucket{
		{Subject: "router", At: BucketStart(now), Samples: 3, MedianMS: 2},
		{Subject: "internet", At: BucketStart(now), Samples: 3, MedianMS: 30},
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := store.ClearLatency("router")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	reopened, err := OpenFileMetricStore(path)
	if err != nil {
		t.Fatal(err)
	}
	buckets, err := reopened.QueryLatency(MetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].Subject != "internet" {
		t.Fatalf("buckets after clear = %#v, want only internet", buckets)
	}
}
