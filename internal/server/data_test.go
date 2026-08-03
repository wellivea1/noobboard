package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/models"
)

// The restart-loop rule counts status changes in a window, so a burst of
// operator-initiated stops reads as a crash loop until the events age out.
// Clearing that app's recorded history is the escape hatch, and it must remove
// exactly that app.
func TestAdminDataClearRemovesOnlyTheNamedSubject(t *testing.T) {
	app, cookie, csrf := newDataTestApp(t, "data-clear-subject")

	rec := postJSON(t, app.Router(), "/api/admin/data/clear", `{"scope":"status_history","subject_type":"app","subject_id":"emby"}`, cookie, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Removed int `json:"removed"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Removed != 2 {
		t.Fatalf("removed = %d, want 2", result.Removed)
	}

	remaining, err := app.deps.History.Query(db.HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining events = %d, want 2", len(remaining))
	}
	for _, event := range remaining {
		if event.SubjectID == "emby" && event.SubjectType == models.SubjectApp {
			t.Fatalf("cleared subject survived: %#v", event)
		}
	}
}

// An app and an infrastructure probe can share a name, so an id with no type is
// ambiguous. Refuse rather than clear the wrong one.
func TestAdminDataClearRequiresSubjectTypeWithSubjectID(t *testing.T) {
	app, cookie, csrf := newDataTestApp(t, "data-clear-ambiguous")
	rec := postJSON(t, app.Router(), "/api/admin/data/clear", `{"scope":"status_history","subject_id":"emby"}`, cookie, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous clear status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	events, err := app.deps.History.Query(db.HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("refused clear still deleted events: %d remain, want 4", len(events))
	}
}

func TestAdminDataClearRejectsUnknownScope(t *testing.T) {
	app, cookie, csrf := newDataTestApp(t, "data-clear-scope")
	rec := postJSON(t, app.Router(), "/api/admin/data/clear", `{"scope":"everything"}`, cookie, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown scope status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// Losing the record of an outage is itself worth a record.
func TestAdminDataClearIsAudited(t *testing.T) {
	app, cookie, csrf := newDataTestApp(t, "data-clear-audit")
	if rec := postJSON(t, app.Router(), "/api/admin/data/clear", `{"scope":"status_history"}`, cookie, csrf); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", rec.Code, rec.Body.String())
	}
	entries, err := app.deps.Store.AuditTail(20)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Action == "history.cleared" {
			return
		}
	}
	t.Fatalf("clearing history was not audited: %#v", entries)
}

func TestAdminDataSummaryCountsRecentChangesPerSubject(t *testing.T) {
	app, cookie, _ := newDataTestApp(t, "data-summary")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/data/summary", nil)
	req.AddCookie(cookie)
	app.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var summary adminDataSummaryView
	if err := json.NewDecoder(rec.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.StatusEventCount != 4 {
		t.Fatalf("status_event_count = %d, want 4", summary.StatusEventCount)
	}
	var emby *adminDataSubjectView
	for i := range summary.Subjects {
		if summary.Subjects[i].SubjectID == "emby" && summary.Subjects[i].SubjectType == string(models.SubjectApp) {
			emby = &summary.Subjects[i]
		}
	}
	if emby == nil {
		t.Fatalf("summary did not report the emby subject: %#v", summary.Subjects)
	}
	if emby.EventCount != 2 {
		t.Fatalf("emby event_count = %d, want 2", emby.EventCount)
	}
	// One of the two seeded emby events is inside the restart-loop window and one
	// is a day old; the recent count is the figure the rule actually reads.
	if emby.RecentEventCount != 1 {
		t.Fatalf("emby recent_event_count = %d, want 1", emby.RecentEventCount)
	}
}

// Clearing must not re-seed a baseline event on the next poll: the point is to
// leave the app with no recorded churn until something really changes.
func TestClearedHistoryStaysClearWithoutNewChanges(t *testing.T) {
	app, cookie, csrf := newDataTestApp(t, "data-clear-stays-clear")
	if rec := postJSON(t, app.Router(), "/api/admin/data/clear", `{"scope":"status_history"}`, cookie, csrf); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", rec.Code, rec.Body.String())
	}
	snapshot, err := app.latestSnapshot(t.Context(), models.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.recordSnapshotHistory(snapshot); err != nil {
		t.Fatal(err)
	}
	events, err := app.deps.History.Query(db.HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("history re-seeded itself after a clear: %#v", events)
	}
}

func newDataTestApp(t *testing.T, name string) (*App, *http.Cookie, string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, name)
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	app := newTestApp(t, cfg)

	now := time.Now().UTC()
	if err := app.deps.History.Append([]models.StatusEvent{
		{ID: "e1", SubjectType: models.SubjectApp, SubjectID: "emby", DisplayName: "Emby", From: models.StatusOnline, To: models.StatusOffline, At: now.Add(-24 * time.Hour)},
		{ID: "e2", SubjectType: models.SubjectApp, SubjectID: "emby", DisplayName: "Emby", From: models.StatusOffline, To: models.StatusOnline, At: now.Add(-5 * time.Minute)},
		{ID: "e3", SubjectType: models.SubjectApp, SubjectID: "plex", DisplayName: "Plex", From: models.StatusOnline, To: models.StatusOffline, At: now.Add(-2 * time.Hour)},
		{ID: "e4", SubjectType: models.SubjectInfra, SubjectID: "emby", DisplayName: "Emby probe", From: models.StatusOnline, To: models.StatusOffline, At: now.Add(-3 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := loginAdmin(t, app.Router())
	return app, cookie, csrf
}

func postJSON(t *testing.T, router http.Handler, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	return rec
}
