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
