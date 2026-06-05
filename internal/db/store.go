package db

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/models"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	LoadState() (State, error)
	SaveState(State) error
	AppendAudit(models.AuditEntry) error
	AuditTail(limit int) ([]models.AuditEntry, error)
	UpsertUser(UserRecord) error
	AllUsers() ([]UserRecord, error)
	UserByUsername(username string) (UserRecord, error)
	UserByID(id string) (UserRecord, error)
	UpsertPersistentSession(PersistentSessionRecord) error
	PersistentSessionByTokenHash(tokenHash string) (PersistentSessionRecord, error)
	DeletePersistentSession(tokenHash string) error
	PrunePersistentSessions(now time.Time, maxEntries int) error
	UpsertNotificationPreference(models.NotificationPreference) error
	NotificationPreferencesForUser(userID string) ([]models.NotificationPreference, error)
	AllNotificationPreferences() ([]models.NotificationPreference, error)
	AppendNotification(NotificationRecord) error
	NotificationsForUser(userID string, limit int) ([]NotificationRecord, error)
	UpsertRepairRequest(models.RepairRequest) error
	RepairRequestByID(id string) (models.RepairRequest, error)
	RepairRequests() ([]models.RepairRequest, error)
	RepairRequestsForUser(userID string) ([]models.RepairRequest, error)
	RuntimeSettings() (RuntimeSettings, bool, error)
	SaveRuntimeSettings(RuntimeSettings) error
	Flush() error
}

type State struct {
	Incidents               []models.Incident               `json:"incidents"`
	NotificationPreferences []models.NotificationPreference `json:"notification_preferences"`
	Notifications           []NotificationRecord            `json:"notifications"`
	RepairRequests          []models.RepairRequest          `json:"repair_requests,omitempty"`
	PersistentSessions      []PersistentSessionRecord       `json:"persistent_sessions,omitempty"`
	Audit                   []models.AuditEntry             `json:"audit"`
	Users                   []UserRecord                    `json:"users"`
	RuntimeSettings         *RuntimeSettings                `json:"runtime_settings,omitempty"`
}

type RuntimeSettings struct {
	Visibility    models.VisibilitySettings `json:"visibility"`
	Privacy       config.PrivacyConfig      `json:"privacy"`
	AppCatalog    config.AppCatalogConfig   `json:"app_catalog"`
	LLM           config.LLMConfig          `json:"llm"`
	Integrations  config.IntegrationConfig  `json:"integrations"`
	Notifications config.NotificationConfig `json:"notifications"`
}

type UserRecord struct {
	ID           string      `json:"id"`
	Username     string      `json:"username"`
	DisplayName  string      `json:"display_name"`
	Role         models.Role `json:"role"`
	PasswordHash string      `json:"password_hash"`
	Salt         string      `json:"salt"`
	Iterations   int         `json:"iterations"`
	CreatedAt    string      `json:"created_at"`
	Disabled     bool        `json:"disabled"`
}

type PersistentSessionRecord struct {
	TokenHash         string    `json:"token_hash"`
	UserID            string    `json:"user_id"`
	CredentialVersion string    `json:"credential_version"`
	CSRFToken         string    `json:"csrf_token"`
	CreatedAt         time.Time `json:"created_at"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type NotificationRecord struct {
	ID      string `json:"id"`
	Dedupe  string `json:"dedupe"`
	UserID  string `json:"user_id"`
	AppID   string `json:"app_id"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

type FileStore struct {
	path  string
	mu    sync.Mutex
	state State
}

func OpenFileStore(path string) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	store := &FileStore{path: path}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.state = State{}
		return s.persistLocked()
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		s.state = State{}
		return nil
	}
	return json.Unmarshal(data, &s.state)
}

func (s *FileStore) LoadState() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.state)
}

func (s *FileStore) SaveState(state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyState, err := cloneState(state)
	if err != nil {
		return err
	}
	s.state = copyState
	return s.persistLocked()
}

func (s *FileStore) AppendAudit(entry models.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Audit = append(s.state.Audit, entry)
	if len(s.state.Audit) > 2000 {
		s.state.Audit = s.state.Audit[len(s.state.Audit)-2000:]
	}
	return s.persistLocked()
}

func (s *FileStore) AuditTail(limit int) ([]models.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.state.Audit) {
		limit = len(s.state.Audit)
	}
	out := append([]models.AuditEntry(nil), s.state.Audit[len(s.state.Audit)-limit:]...)
	return out, nil
}

func (s *FileStore) UpsertUser(user UserRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.state.Users {
		if existing.ID == user.ID || existing.Username == user.Username {
			s.state.Users[i] = user
			return s.persistLocked()
		}
	}
	s.state.Users = append(s.state.Users, user)
	return s.persistLocked()
}

func (s *FileStore) AllUsers() ([]UserRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UserRecord(nil), s.state.Users...), nil
}

func (s *FileStore) UserByUsername(username string) (UserRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.state.Users {
		if user.Username == username {
			return user, nil
		}
	}
	return UserRecord{}, ErrNotFound
}

func (s *FileStore) UserByID(id string) (UserRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.state.Users {
		if user.ID == id {
			return user, nil
		}
	}
	return UserRecord{}, ErrNotFound
}

func (s *FileStore) UpsertPersistentSession(record PersistentSessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.state.PersistentSessions {
		if existing.TokenHash == record.TokenHash {
			s.state.PersistentSessions[i] = record
			return s.persistLocked()
		}
	}
	s.state.PersistentSessions = append(s.state.PersistentSessions, record)
	return s.persistLocked()
}

func (s *FileStore) PersistentSessionByTokenHash(tokenHash string) (PersistentSessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.state.PersistentSessions {
		if record.TokenHash == tokenHash {
			return record, nil
		}
	}
	return PersistentSessionRecord{}, ErrNotFound
}

func (s *FileStore) DeletePersistentSession(tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, record := range s.state.PersistentSessions {
		if record.TokenHash == tokenHash {
			s.state.PersistentSessions = append(s.state.PersistentSessions[:i], s.state.PersistentSessions[i+1:]...)
			return s.persistLocked()
		}
	}
	return nil
}

func (s *FileStore) PrunePersistentSessions(now time.Time, maxEntries int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.state.PersistentSessions[:0]
	for _, record := range s.state.PersistentSessions {
		if record.TokenHash == "" || record.UserID == "" || record.ExpiresAt.IsZero() || now.After(record.ExpiresAt) {
			continue
		}
		kept = append(kept, record)
	}
	s.state.PersistentSessions = kept
	if maxEntries < 0 {
		maxEntries = 0
	}
	for maxEntries > 0 && len(s.state.PersistentSessions) > maxEntries {
		oldest := 0
		oldestSeen := persistentSessionSeenAt(s.state.PersistentSessions[0])
		for i := 1; i < len(s.state.PersistentSessions); i++ {
			if seenAt := persistentSessionSeenAt(s.state.PersistentSessions[i]); seenAt.Before(oldestSeen) {
				oldest = i
				oldestSeen = seenAt
			}
		}
		s.state.PersistentSessions = append(s.state.PersistentSessions[:oldest], s.state.PersistentSessions[oldest+1:]...)
	}
	return s.persistLocked()
}

func persistentSessionSeenAt(record PersistentSessionRecord) time.Time {
	if !record.LastSeenAt.IsZero() {
		return record.LastSeenAt
	}
	return record.CreatedAt
}

func (s *FileStore) UpsertNotificationPreference(pref models.NotificationPreference) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.state.NotificationPreferences {
		if existing.UserID == pref.UserID && existing.AppID == pref.AppID {
			s.state.NotificationPreferences[i] = pref
			return s.persistLocked()
		}
	}
	s.state.NotificationPreferences = append(s.state.NotificationPreferences, pref)
	return s.persistLocked()
}

func (s *FileStore) NotificationPreferencesForUser(userID string) ([]models.NotificationPreference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var prefs []models.NotificationPreference
	for _, pref := range s.state.NotificationPreferences {
		if pref.UserID == userID {
			prefs = append(prefs, pref)
		}
	}
	return prefs, nil
}

func (s *FileStore) AllNotificationPreferences() ([]models.NotificationPreference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]models.NotificationPreference(nil), s.state.NotificationPreferences...), nil
}

func (s *FileStore) AppendNotification(record NotificationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Notifications = append(s.state.Notifications, record)
	if len(s.state.Notifications) > 1000 {
		s.state.Notifications = s.state.Notifications[len(s.state.Notifications)-1000:]
	}
	return s.persistLocked()
}

func (s *FileStore) NotificationsForUser(userID string, limit int) ([]NotificationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []NotificationRecord
	for i := len(s.state.Notifications) - 1; i >= 0; i-- {
		record := s.state.Notifications[i]
		if record.UserID == "" || record.UserID == userID {
			out = append(out, record)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *FileStore) UpsertRepairRequest(request models.RepairRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.state.RepairRequests {
		if existing.ID == request.ID {
			s.state.RepairRequests[i] = request
			return s.persistLocked()
		}
	}
	s.state.RepairRequests = append(s.state.RepairRequests, request)
	return s.persistLocked()
}

func (s *FileStore) RepairRequestByID(id string) (models.RepairRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, request := range s.state.RepairRequests {
		if request.ID == id {
			return request, nil
		}
	}
	return models.RepairRequest{}, ErrNotFound
}

func (s *FileStore) RepairRequests() ([]models.RepairRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]models.RepairRequest(nil), s.state.RepairRequests...), nil
}

func (s *FileStore) RepairRequestsForUser(userID string) ([]models.RepairRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []models.RepairRequest
	for _, request := range s.state.RepairRequests {
		if request.RequesterID == userID {
			out = append(out, request)
		}
	}
	return out, nil
}

func (s *FileStore) RuntimeSettings() (RuntimeSettings, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.RuntimeSettings == nil {
		return RuntimeSettings{}, false, nil
	}
	out, err := cloneRuntimeSettings(*s.state.RuntimeSettings)
	if err != nil {
		return RuntimeSettings{}, false, err
	}
	return out, true, nil
}

func (s *FileStore) SaveRuntimeSettings(settings RuntimeSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copySettings, err := cloneRuntimeSettings(settings)
	if err != nil {
		return err
	}
	s.state.RuntimeSettings = &copySettings
	return s.persistLocked()
}

func (s *FileStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

func (s *FileStore) persistLocked() error {
	tmp := s.path + ".tmp"
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func cloneState(state State) (State, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return State{}, err
	}
	var out State
	if err := json.Unmarshal(data, &out); err != nil {
		return State{}, err
	}
	return out, nil
}

func cloneRuntimeSettings(settings RuntimeSettings) (RuntimeSettings, error) {
	data, err := json.Marshal(settings)
	if err != nil {
		return RuntimeSettings{}, err
	}
	var out RuntimeSettings
	if err := json.Unmarshal(data, &out); err != nil {
		return RuntimeSettings{}, err
	}
	return out, nil
}
