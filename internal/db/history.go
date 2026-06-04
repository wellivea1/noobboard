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

type HistoryFilter struct {
	SubjectType models.StatusSubjectType
	SubjectID   string
	Since       time.Time
	Limit       int
}

type HistoryStore interface {
	Append([]models.StatusEvent) error
	Query(HistoryFilter) ([]models.StatusEvent, error)
	Prune(config.RetentionConfig) error
}

type FileHistoryStore struct {
	path          string
	maxPerSubject int
	mu            sync.Mutex
	events        []models.StatusEvent
}

func HistoryPathForDatabase(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "history.jsonl")
}

func OpenFileHistoryStore(path string, maxPerSubject int) (*FileHistoryStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	store := &FileHistoryStore{path: path, maxPerSubject: maxPerSubject}
	if store.maxPerSubject <= 0 {
		store.maxPerSubject = 500
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileHistoryStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		s.events = nil
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	var events []models.StatusEvent
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event models.StatusEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	sortEvents(events)
	s.events = keepPerSubject(events, s.maxPerSubject)
	return nil
}

func (s *FileHistoryStore) Append(events []models.StatusEvent) error {
	if len(events) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	s.events = append(s.events, events...)
	sortEvents(s.events)
	s.events = keepPerSubject(s.events, s.maxPerSubject)
	return nil
}

func (s *FileHistoryStore) Query(filter HistoryFilter) ([]models.StatusEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := filter.Limit
	var out []models.StatusEvent
	for i := len(s.events) - 1; i >= 0; i-- {
		event := s.events[i]
		if filter.SubjectType != "" && event.SubjectType != filter.SubjectType {
			continue
		}
		if filter.SubjectID != "" && !strings.EqualFold(event.SubjectID, filter.SubjectID) {
			continue
		}
		if !filter.Since.IsZero() && event.At.Before(filter.Since) {
			continue
		}
		out = append(out, event)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *FileHistoryStore) Prune(retention config.RetentionConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	maxPerSubject := retention.MaxStatusEventsPerSubject
	if maxPerSubject <= 0 {
		maxPerSubject = s.maxPerSubject
	}
	cutoff := time.Time{}
	if retention.MaxStatusEventAge > 0 {
		cutoff = time.Now().UTC().Add(-retention.MaxStatusEventAge)
	}
	events := make([]models.StatusEvent, 0, len(s.events))
	for _, event := range s.events {
		if !cutoff.IsZero() && event.At.Before(cutoff) {
			continue
		}
		events = append(events, event)
	}
	sortEvents(events)
	events = keepPerSubject(events, maxPerSubject)
	if err := s.writeAllLocked(events); err != nil {
		return err
	}
	s.events = events
	s.maxPerSubject = maxPerSubject
	return nil
}

func (s *FileHistoryStore) writeAllLocked(events []models.StatusEvent) error {
	tmp := s.path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func sortEvents(events []models.StatusEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].At.Before(events[j].At)
	})
}

func keepPerSubject(events []models.StatusEvent, maxPerSubject int) []models.StatusEvent {
	if maxPerSubject <= 0 {
		return append([]models.StatusEvent(nil), events...)
	}
	counts := map[string]int{}
	keep := make([]bool, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		key := historySubjectKey(events[i])
		if counts[key] >= maxPerSubject {
			continue
		}
		counts[key]++
		keep[i] = true
	}
	out := make([]models.StatusEvent, 0, len(events))
	for i, event := range events {
		if keep[i] {
			out = append(out, event)
		}
	}
	return out
}

func historySubjectKey(event models.StatusEvent) string {
	return string(event.SubjectType) + "|" + strings.ToLower(strings.TrimSpace(event.SubjectID))
}
