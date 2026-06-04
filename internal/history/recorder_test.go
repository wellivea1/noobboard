package history

import (
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

func TestRecorderSeedsBaselineAndRecordsTransitions(t *testing.T) {
	recorder := NewRecorder()
	first := snapshotWithAppStatus(models.StatusOnline, time.Now().UTC())
	if events := recorder.Record(first); len(events) != 0 {
		t.Fatalf("baseline emitted events: %#v", events)
	}
	second := snapshotWithAppStatus(models.StatusOffline, first.GeneratedAt.Add(time.Minute))
	events := recorder.Record(second)
	if len(events) != 1 {
		t.Fatalf("transition events = %#v", events)
	}
	if events[0].SubjectType != models.SubjectApp || events[0].SubjectID != "emby" || events[0].From != models.StatusOnline || events[0].To != models.StatusOffline {
		t.Fatalf("unexpected transition: %#v", events[0])
	}
	third := snapshotWithAppStatus(models.StatusOnline, second.GeneratedAt.Add(time.Minute))
	events = recorder.Record(third)
	if len(events) != 1 || events[0].From != models.StatusOffline || events[0].To != models.StatusOnline {
		t.Fatalf("flap transition = %#v", events)
	}
}

func TestRecorderRecordsDisappearingAppAsUnknownOnce(t *testing.T) {
	recorder := NewRecorder()
	first := snapshotWithAppStatus(models.StatusOnline, time.Now().UTC())
	if events := recorder.Record(first); len(events) != 0 {
		t.Fatalf("baseline emitted events: %#v", events)
	}
	empty := first
	empty.GeneratedAt = first.GeneratedAt.Add(time.Minute)
	empty.Apps = nil
	events := recorder.Record(empty)
	if len(events) != 1 || events[0].SubjectID != "emby" || events[0].To != models.StatusUnknown {
		t.Fatalf("missing app event = %#v", events)
	}
	empty.GeneratedAt = empty.GeneratedAt.Add(time.Minute)
	if events := recorder.Record(empty); len(events) != 0 {
		t.Fatalf("disappearing app emitted more than once: %#v", events)
	}
}

func TestRecorderRecordsInfrastructureTransitions(t *testing.T) {
	recorder := NewRecorder()
	first := snapshotWithInfra(true, time.Now().UTC())
	if events := recorder.Record(first); len(events) != 0 {
		t.Fatalf("baseline emitted events: %#v", events)
	}
	second := snapshotWithInfra(false, first.GeneratedAt.Add(time.Minute))
	events := recorder.Record(second)
	found := false
	for _, event := range events {
		if event.SubjectType == models.SubjectInfra && event.SubjectID == "internet" {
			found = true
			if event.From != models.StatusOnline || event.To != models.StatusOffline {
				t.Fatalf("internet event = %#v", event)
			}
		}
	}
	if !found {
		t.Fatalf("missing internet transition: %#v", events)
	}
}

func snapshotWithAppStatus(status models.CurrentStatus, at time.Time) models.Snapshot {
	return models.Snapshot{
		GeneratedAt: at,
		Infrastructure: models.InfrastructureStatus{
			InternetReachable:  true,
			DNSOK:              true,
			UniFiWANUp:         true,
			NASReachable:       true,
			UnraidAPIReachable: true,
			UnraidArrayState:   "started",
			UnraidArrayHealthy: true,
		},
		Apps: []models.AppStatus{{
			AppID:                 "emby",
			DisplayName:           "Emby",
			VisibleToGeneralUsers: true,
			CurrentStatus:         status,
			ServerSummary:         "Emby changed.",
		}},
	}
}

func snapshotWithInfra(internet bool, at time.Time) models.Snapshot {
	snapshot := snapshotWithAppStatus(models.StatusOnline, at)
	snapshot.Infrastructure.InternetReachable = internet
	return snapshot
}
