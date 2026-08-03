package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/models"
)

func TestFileHistoryStoreAppendQueryAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store, err := OpenFileHistoryStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	events := []models.StatusEvent{
		statusEventForTest("1", "emby", models.StatusOnline, models.StatusOffline, now.Add(-time.Minute)),
		statusEventForTest("2", "emby", models.StatusOffline, models.StatusOnline, now),
		statusEventForTest("3", "plex", models.StatusOnline, models.StatusOffline, now),
	}
	if err := store.Append(events); err != nil {
		t.Fatal(err)
	}
	got, err := store.Query(HistoryFilter{SubjectType: models.SubjectApp, SubjectID: "emby"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "2" || got[1].ID != "1" {
		t.Fatalf("query order/events = %#v", got)
	}
	reloaded, err := OpenFileHistoryStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	got, err = reloaded.Query(HistoryFilter{SubjectType: models.SubjectApp, SubjectID: "emby", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("reloaded limited query = %#v", got)
	}
}

func TestFileHistoryStorePruneByAgeAndPerSubjectCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store, err := OpenFileHistoryStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	events := []models.StatusEvent{
		statusEventForTest("old", "emby", models.StatusOnline, models.StatusOffline, now.Add(-10*24*time.Hour)),
		statusEventForTest("keep-1", "emby", models.StatusOffline, models.StatusOnline, now.Add(-3*time.Hour)),
		statusEventForTest("keep-2", "emby", models.StatusOnline, models.StatusOffline, now.Add(-2*time.Hour)),
		statusEventForTest("keep-3", "emby", models.StatusOffline, models.StatusOnline, now.Add(-time.Hour)),
		statusEventForTest("plex", "plex", models.StatusOnline, models.StatusOffline, now.Add(-time.Hour)),
	}
	if err := store.Append(events); err != nil {
		t.Fatal(err)
	}
	if err := store.Prune(config.RetentionConfig{MaxStatusEventAge: 24 * time.Hour, MaxStatusEventsPerSubject: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Query(HistoryFilter{SubjectType: models.SubjectApp, SubjectID: "emby"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "keep-3" || got[1].ID != "keep-2" {
		t.Fatalf("pruned emby events = %#v", got)
	}
	all, err := store.Query(HistoryFilter{SubjectType: models.SubjectApp})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all pruned events = %#v", all)
	}
}

func statusEventForTest(id, appID string, from, to models.CurrentStatus, at time.Time) models.StatusEvent {
	return models.StatusEvent{
		ID:          id,
		SubjectType: models.SubjectApp,
		SubjectID:   appID,
		DisplayName: appID,
		From:        from,
		To:          to,
		At:          at,
	}
}

// Clear is scoped by the same predicate Query filters on, so a per-app clear
// must not touch an infrastructure subject that happens to share its id.
func TestFileHistoryStoreClearIsScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store, err := OpenFileHistoryStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.Append([]models.StatusEvent{
		{ID: "a", SubjectType: models.SubjectApp, SubjectID: "emby", At: now.Add(-time.Hour)},
		{ID: "b", SubjectType: models.SubjectApp, SubjectID: "plex", At: now.Add(-time.Hour)},
		{ID: "c", SubjectType: models.SubjectInfra, SubjectID: "emby", At: now.Add(-time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := store.Clear(HistoryFilter{SubjectType: models.SubjectApp, SubjectID: "emby"})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	// Reopening proves the deletion reached the file, not just the slice.
	reopened, err := OpenFileHistoryStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	events, err := reopened.Query(HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events after reopen = %d, want 2", len(events))
	}
	for _, event := range events {
		if event.ID == "a" {
			t.Fatal("cleared event survived a reopen")
		}
	}
}

func TestFileHistoryStoreClearAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store, err := OpenFileHistoryStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append([]models.StatusEvent{
		{ID: "a", SubjectType: models.SubjectApp, SubjectID: "emby", At: time.Now().UTC()},
		{ID: "b", SubjectType: models.SubjectInfra, SubjectID: "internet", At: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := store.Clear(HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	events, err := store.Query(HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events after clear-all = %d, want 0", len(events))
	}
}
