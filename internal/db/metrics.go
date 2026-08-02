package db

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/models"
)

// LatencyBucketWindow is the downsampling interval. Raw per-poll samples are
// never persisted — see models.LatencyBucket for why.
const LatencyBucketWindow = 5 * time.Minute

type MetricFilter struct {
	Subject string
	Since   time.Time
	Limit   int
}

type MetricStore interface {
	AppendLatency([]models.LatencyBucket) error
	QueryLatency(MetricFilter) ([]models.LatencyBucket, error)
	PruneLatency(config.RetentionConfig) error
}

// FileMetricStore mirrors FileHistoryStore: a JSONL file, loaded once, kept in
// memory, rewritten on prune. The volume is bounded by bucketing plus retention,
// so the whole series fits in memory comfortably.
type FileMetricStore struct {
	path    string
	mu      sync.Mutex
	buckets []models.LatencyBucket
}

func MetricsPathForDatabase(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "latency.jsonl")
}

func OpenFileMetricStore(path string) (*FileMetricStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	store := &FileMetricStore{path: path}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileMetricStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		s.buckets = nil
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var buckets []models.LatencyBucket
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var bucket models.LatencyBucket
		// A truncated final line after an unclean shutdown must not stop the
		// rest of the series loading.
		if err := json.Unmarshal([]byte(line), &bucket); err != nil {
			continue
		}
		if bucket.Subject == "" || bucket.At.IsZero() {
			continue
		}
		buckets = append(buckets, bucket)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	sortBuckets(buckets)
	s.buckets = buckets
	return nil
}

func (s *FileMetricStore) AppendLatency(buckets []models.LatencyBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, bucket := range buckets {
		data, err := json.Marshal(bucket)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	s.buckets = append(s.buckets, buckets...)
	sortBuckets(s.buckets)
	return nil
}

func (s *FileMetricStore) QueryLatency(filter MetricFilter) ([]models.LatencyBucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]models.LatencyBucket, 0, len(s.buckets))
	for _, bucket := range s.buckets {
		if filter.Subject != "" && !strings.EqualFold(bucket.Subject, filter.Subject) {
			continue
		}
		if !filter.Since.IsZero() && bucket.At.Before(filter.Since) {
			continue
		}
		out = append(out, bucket)
	}
	// Trim from the front: a window is always the most recent N buckets.
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[len(out)-filter.Limit:]
	}
	return out, nil
}

func (s *FileMetricStore) PruneLatency(retention config.RetentionConfig) error {
	if retention.MaxLatencyBucketAge <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().UTC().Add(-retention.MaxLatencyBucketAge)
	kept := make([]models.LatencyBucket, 0, len(s.buckets))
	for _, bucket := range s.buckets {
		if bucket.At.Before(cutoff) {
			continue
		}
		kept = append(kept, bucket)
	}
	if len(kept) == len(s.buckets) {
		return nil
	}
	s.buckets = kept
	return s.rewriteLocked()
}

// rewriteLocked writes through a temporary file and renames, so a crash mid-prune
// leaves the previous series intact rather than a half-written one.
func (s *FileMetricStore) rewriteLocked() error {
	tmp := s.path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	for _, bucket := range s.buckets {
		data, err := json.Marshal(bucket)
		if err != nil {
			file.Close()
			return err
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			file.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func sortBuckets(buckets []models.LatencyBucket) {
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].At.Equal(buckets[j].At) {
			return buckets[i].Subject < buckets[j].Subject
		}
		return buckets[i].At.Before(buckets[j].At)
	})
}

// BucketStart snaps a time to the start of its bucket, so every writer agrees on
// bucket boundaries regardless of when it happens to run.
func BucketStart(at time.Time) time.Time {
	return at.UTC().Truncate(LatencyBucketWindow)
}

// SummariseLatency reduces one bucket's raw samples. Failures are counted but
// excluded from the timing figures: a failed probe measures the timeout, not the
// link, and letting it into the median would hide the next real spike.
func SummariseLatency(subject string, at time.Time, latenciesMS []int64, failures int) models.LatencyBucket {
	bucket := models.LatencyBucket{
		Subject:  subject,
		At:       BucketStart(at),
		Samples:  len(latenciesMS) + failures,
		Failures: failures,
	}
	if len(latenciesMS) == 0 {
		return bucket
	}
	values := append([]int64(nil), latenciesMS...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	bucket.MinMS = values[0]
	bucket.MaxMS = values[len(values)-1]
	middle := len(values) / 2
	if len(values)%2 == 1 {
		bucket.MedianMS = values[middle]
	} else {
		bucket.MedianMS = (values[middle-1] + values[middle]) / 2
	}
	return bucket
}
