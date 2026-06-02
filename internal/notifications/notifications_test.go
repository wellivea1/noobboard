package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/wellivea1/server-status/internal/audit"
	"github.com/wellivea1/server-status/internal/config"
	"github.com/wellivea1/server-status/internal/db"
	"github.com/wellivea1/server-status/internal/models"
	"github.com/wellivea1/server-status/internal/privacy"
)

type memoryStore struct {
	db.State
}

func (m *memoryStore) LoadState() (db.State, error) { return m.State, nil }
func (m *memoryStore) SaveState(s db.State) error   { m.State = s; return nil }
func (m *memoryStore) AppendAudit(e models.AuditEntry) error {
	m.Audit = append(m.Audit, e)
	return nil
}
func (m *memoryStore) AuditTail(limit int) ([]models.AuditEntry, error) { return m.Audit, nil }
func (m *memoryStore) UpsertUser(u db.UserRecord) error {
	m.Users = append(m.Users, u)
	return nil
}
func (m *memoryStore) AllUsers() ([]db.UserRecord, error) {
	return append([]db.UserRecord(nil), m.Users...), nil
}
func (m *memoryStore) UserByUsername(username string) (db.UserRecord, error) {
	return db.UserRecord{}, db.ErrNotFound
}
func (m *memoryStore) UserByID(id string) (db.UserRecord, error) {
	return db.UserRecord{}, db.ErrNotFound
}
func (m *memoryStore) UpsertNotificationPreference(p models.NotificationPreference) error {
	for i, existing := range m.NotificationPreferences {
		if existing.UserID == p.UserID && existing.AppID == p.AppID {
			m.NotificationPreferences[i] = p
			return nil
		}
	}
	m.NotificationPreferences = append(m.NotificationPreferences, p)
	return nil
}
func (m *memoryStore) NotificationPreferencesForUser(userID string) ([]models.NotificationPreference, error) {
	var prefs []models.NotificationPreference
	for _, pref := range m.NotificationPreferences {
		if pref.UserID == userID {
			prefs = append(prefs, pref)
		}
	}
	return prefs, nil
}
func (m *memoryStore) AllNotificationPreferences() ([]models.NotificationPreference, error) {
	return append([]models.NotificationPreference(nil), m.NotificationPreferences...), nil
}
func (m *memoryStore) RuntimeSettings() (db.RuntimeSettings, bool, error) {
	if m.State.RuntimeSettings == nil {
		return db.RuntimeSettings{}, false, nil
	}
	return *m.State.RuntimeSettings, true, nil
}
func (m *memoryStore) SaveRuntimeSettings(s db.RuntimeSettings) error {
	m.State.RuntimeSettings = &s
	return nil
}
func (m *memoryStore) Flush() error { return nil }

func testManager() (*Manager, *MockBackend, *memoryStore) {
	store := &memoryStore{}
	backend := NewMockBackend()
	redactor := privacy.NewRedactor(config.PrivacyConfig{})
	auditor := audit.New(store, redactor)
	manager := NewManager(store, backend, config.NotificationConfig{Enabled: true, GlobalOptInEnabled: true, RateLimitWindow: time.Hour, WholeOutageDeduping: true}, auditor)
	return manager, backend, store
}

func TestNotificationOptInOnlyAllowsVisibleApps(t *testing.T) {
	manager, _, _ := testManager()
	err := manager.SavePreference(models.NotificationPreference{UserID: "u1", AppID: "hidden", NotifyOnDown: true}, []models.AppStatus{
		{AppID: "visible", VisibleToGeneralUsers: true, NotificationOptInAllowed: true},
		{AppID: "hidden", VisibleToGeneralUsers: false, NotificationOptInAllowed: true},
	})
	if err == nil {
		t.Fatal("expected hidden app subscription to be rejected")
	}
}

func TestNotificationDedupesDuringRateLimitWindow(t *testing.T) {
	manager, backend, store := testManager()
	store.NotificationPreferences = []models.NotificationPreference{{UserID: "u1", AppID: "emby", NotifyOnDown: true}}
	snapshot := models.Snapshot{
		Infrastructure: models.InfrastructureStatus{NASReachable: true, DockerServiceAvailable: true},
		Apps:           []models.AppStatus{{AppID: "emby", DisplayName: "Emby", VisibleToGeneralUsers: true, NotificationOptInAllowed: true, CurrentStatus: models.StatusOffline, ServerSummary: "Emby is offline."}},
	}
	if err := manager.ProcessSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if err := manager.ProcessSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if len(backend.Sent) != 1 {
		t.Fatalf("expected one notification, got %d", len(backend.Sent))
	}
}

func TestWholeNASOutageSuppressesPerAppAlerts(t *testing.T) {
	manager, backend, store := testManager()
	store.NotificationPreferences = []models.NotificationPreference{{UserID: "u1", AppID: "emby", NotifyOnDown: true}}
	snapshot := models.Snapshot{
		Infrastructure: models.InfrastructureStatus{NASReachable: false, DockerServiceAvailable: false},
		Apps:           []models.AppStatus{{AppID: "emby", DisplayName: "Emby", VisibleToGeneralUsers: true, NotificationOptInAllowed: true, CurrentStatus: models.StatusOffline}},
	}
	if err := manager.ProcessSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if len(backend.Sent) != 1 || backend.Sent[0].Dedupe != "nas-outage" {
		t.Fatalf("expected one NAS rollup alert, got %#v", backend.Sent)
	}
}

func TestRecoveryNotification(t *testing.T) {
	manager, backend, store := testManager()
	store.NotificationPreferences = []models.NotificationPreference{{UserID: "u1", AppID: "emby", NotifyOnRecovery: true, LastStatusSeen: "offline"}}
	snapshot := models.Snapshot{
		Infrastructure: models.InfrastructureStatus{NASReachable: true, DockerServiceAvailable: true},
		Apps:           []models.AppStatus{{AppID: "emby", DisplayName: "Emby", VisibleToGeneralUsers: true, NotificationOptInAllowed: true, CurrentStatus: models.StatusOnline, ServerSummary: "Emby has recovered."}},
	}
	if err := manager.ProcessSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if len(backend.Sent) != 1 {
		t.Fatalf("expected recovery notification, got %d", len(backend.Sent))
	}
}

func TestRollupUsesGlobalOptInSetting(t *testing.T) {
	rollup := RollupFromPreferences(true, false, nil)
	if !rollup.Enabled {
		t.Fatal("expected notifications to remain enabled")
	}
	if rollup.GlobalOptInEnabled {
		t.Fatal("expected global opt-in to reflect the configured setting")
	}
}
