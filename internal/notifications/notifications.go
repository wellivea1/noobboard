package notifications

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/audit"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/models"
)

type Backend interface {
	Send(context.Context, Message) error
}

type Message struct {
	UserID  string
	AppID   string
	Subject string
	Body    string
	Dedupe  string
}

type MockBackend struct {
	mu   sync.Mutex
	Sent []Message
}

func NewMockBackend() *MockBackend {
	return &MockBackend{}
}

func (m *MockBackend) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Sent = append(m.Sent, msg)
	return nil
}

type Manager struct {
	mu      sync.RWMutex
	store   db.Store
	backend Backend
	cfg     config.NotificationConfig
	audit   *audit.Auditor
}

func NewManager(store db.Store, backend Backend, cfg config.NotificationConfig, auditor *audit.Auditor) *Manager {
	return &Manager{store: store, backend: backend, cfg: cfg, audit: auditor}
}

func (m *Manager) UpdateConfig(cfg config.NotificationConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
}

func (m *Manager) Preferences(userID string) ([]models.NotificationPreference, error) {
	return m.store.NotificationPreferencesForUser(userID)
}

func (m *Manager) SavePreference(pref models.NotificationPreference, visibleApps []models.AppStatus) error {
	cfg := m.configSnapshot()
	if !cfg.Enabled || !cfg.GlobalOptInEnabled {
		return fmt.Errorf("notification opt-in is disabled")
	}
	var allowed bool
	for _, app := range visibleApps {
		if app.AppID == pref.AppID && app.VisibleToGeneralUsers && app.NotificationOptInAllowed {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("app %s is not subscribable for this user", pref.AppID)
	}
	return m.store.UpsertNotificationPreference(pref)
}

func (m *Manager) NotifyAdmins(ctx context.Context, subject, body, appID, dedupe string) (int, error) {
	cfg := m.configSnapshot()
	if !cfg.Enabled {
		return 0, nil
	}
	users, err := m.store.AllUsers()
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, user := range users {
		if user.Disabled || user.Role != models.RoleAdmin {
			continue
		}
		msg := Message{UserID: user.ID, AppID: appID, Subject: subject, Body: body, Dedupe: dedupe}
		if err := m.backend.Send(ctx, msg); err != nil {
			return sent, err
		}
		_ = m.store.AppendNotification(db.NotificationRecord{
			ID:      notificationRecordID(user.ID, appID, dedupe),
			Dedupe:  dedupe,
			UserID:  user.ID,
			AppID:   appID,
			Message: subject + ": " + body,
			Time:    time.Now().UTC().Format(time.RFC3339Nano),
		})
		sent++
	}
	if sent > 0 {
		m.audit.Record("system", "notification.admin_message", map[string]interface{}{"app_id": appID, "sent": sent, "dedupe": dedupe})
	}
	return sent, nil
}

func (m *Manager) NotifyUser(ctx context.Context, userID, subject, body, appID, dedupe string) error {
	cfg := m.configSnapshot()
	if !cfg.Enabled || strings.TrimSpace(userID) == "" {
		return nil
	}
	msg := Message{UserID: userID, AppID: appID, Subject: subject, Body: body, Dedupe: dedupe}
	if err := m.backend.Send(ctx, msg); err != nil {
		return err
	}
	_ = m.store.AppendNotification(db.NotificationRecord{
		ID:      notificationRecordID(userID, appID, dedupe),
		Dedupe:  dedupe,
		UserID:  userID,
		AppID:   appID,
		Message: subject + ": " + body,
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	m.audit.Record("system", "notification.user_message", map[string]interface{}{"app_id": appID, "user_id": userID, "dedupe": dedupe})
	return nil
}

func (m *Manager) ProcessSnapshot(ctx context.Context, snapshot models.Snapshot) error {
	cfg := m.configSnapshot()
	if !cfg.Enabled {
		return nil
	}
	if cfg.WholeOutageDeduping && !snapshot.Infrastructure.NASReachable {
		return m.sendRollup(ctx, "nas-outage", "NAS outage", "NAS is unreachable; suppressing per-app alerts.")
	}
	if cfg.WholeOutageDeduping && snapshot.Infrastructure.NASReachable && !snapshot.Infrastructure.DockerServiceAvailable {
		return m.sendRollup(ctx, "docker-outage", "Docker outage", "Docker service is unavailable; suppressing per-app alerts.")
	}
	prefs, err := m.store.AllNotificationPreferences()
	if err != nil {
		return err
	}
	apps := map[string]models.AppStatus{}
	for _, app := range snapshot.Apps {
		apps[app.AppID] = app
	}
	for _, pref := range prefs {
		app, ok := apps[pref.AppID]
		if !ok || !app.VisibleToGeneralUsers || !app.NotificationOptInAllowed {
			continue
		}
		if shouldNotify(pref, app.CurrentStatus) {
			key := fmt.Sprintf("%s:%s:%s", pref.UserID, pref.AppID, app.CurrentStatus)
			if pref.DedupeKey == key && time.Since(pref.LastSentAt) < cfg.RateLimitWindow {
				continue
			}
			msg := Message{
				UserID:  pref.UserID,
				AppID:   pref.AppID,
				Subject: fmt.Sprintf("%s is %s", app.DisplayName, app.CurrentStatus),
				Body:    app.ServerSummary,
				Dedupe:  key,
			}
			if err := m.backend.Send(ctx, msg); err != nil {
				return err
			}
			pref.DedupeKey = key
			pref.LastSentAt = time.Now().UTC()
			pref.LastStatusSeen = string(app.CurrentStatus)
			_ = m.store.UpsertNotificationPreference(pref)
			m.audit.Record("system", "notification.sent", map[string]interface{}{"app_id": pref.AppID, "user_id": pref.UserID, "status": string(app.CurrentStatus)})
		}
	}
	return nil
}

func (m *Manager) configSnapshot() config.NotificationConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func shouldNotify(pref models.NotificationPreference, status models.CurrentStatus) bool {
	if pref.NotifyOnDown && (status == models.StatusOffline || status == models.StatusDegraded) {
		return true
	}
	if pref.NotifyOnRecovery && pref.LastStatusSeen != "" && pref.LastStatusSeen != string(models.StatusOnline) && status == models.StatusOnline {
		return true
	}
	return false
}

func (m *Manager) sendRollup(ctx context.Context, key, subject, body string) error {
	msg := Message{Subject: subject, Body: body, Dedupe: key}
	if err := m.backend.Send(ctx, msg); err != nil {
		return err
	}
	m.audit.Record("system", "notification.rollup", map[string]interface{}{"dedupe": key, "subject": subject, "body": body})
	return nil
}

func RollupFromPreferences(enabled, globalOptInEnabled bool, prefs []models.NotificationPreference) models.NotificationRollup {
	return models.NotificationRollup{
		Enabled:              enabled,
		GlobalOptInEnabled:   globalOptInEnabled,
		PendingDedupedEvents: countDedupeKeys(prefs),
	}
}

func countDedupeKeys(prefs []models.NotificationPreference) int {
	seen := map[string]bool{}
	for _, pref := range prefs {
		if strings.TrimSpace(pref.DedupeKey) != "" {
			seen[pref.DedupeKey] = true
		}
	}
	return len(seen)
}

func notificationRecordID(values ...string) string {
	return fmt.Sprintf("notification-%d-%s", time.Now().UTC().UnixNano(), strings.NewReplacer(" ", "-", ":", "-", "/", "-").Replace(strings.Join(values, "-")))
}
