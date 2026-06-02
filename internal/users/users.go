package users

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wellivea1/server-status/internal/config"
	"github.com/wellivea1/server-status/internal/db"
	"github.com/wellivea1/server-status/internal/models"
)

const (
	AdminRole       = models.RoleAdmin
	GeneralUserRole = models.RoleGeneralUser
)

type Registry struct {
	store db.Store
	auth  config.AuthConfig
}

type User struct {
	ID          string      `json:"id"`
	Username    string      `json:"username"`
	DisplayName string      `json:"display_name"`
	Role        models.Role `json:"role"`
	Disabled    bool        `json:"disabled"`
}

type SaveUserRequest struct {
	Username    string      `json:"username"`
	DisplayName string      `json:"display_name"`
	Role        models.Role `json:"role"`
	Password    string      `json:"password,omitempty"`
	Disabled    bool        `json:"disabled"`
}

func NewRegistry(store db.Store, auth config.AuthConfig) *Registry {
	reg := &Registry{store: store, auth: auth}
	reg.bootstrap()
	return reg
}

func (r *Registry) bootstrap() {
	if _, err := r.store.UserByUsername(r.auth.BootstrapAdminUsername); err == nil {
		return
	}
	password := r.auth.BootstrapAdminPassword
	if password == "" {
		password = "change-me-now"
	}
	user, err := r.newRecord("admin-1", r.auth.BootstrapAdminUsername, "Administrator", models.RoleAdmin, password)
	if err == nil {
		_ = r.store.UpsertUser(user)
	}
	general, err := r.newRecord("user-1", "viewer", "Viewer", models.RoleGeneralUser, "change-me-now")
	if err == nil {
		_ = r.store.UpsertUser(general)
	}
}

func (r *Registry) Authenticate(username, password string) (User, error) {
	record, err := r.store.UserByUsername(username)
	if err != nil {
		return User{}, err
	}
	if record.Disabled {
		return User{}, errors.New("user disabled")
	}
	if !verifyPassword(password, record.Salt, record.PasswordHash, record.Iterations) {
		return User{}, errors.New("invalid credentials")
	}
	return toUser(record), nil
}

func (r *Registry) ByID(id string) (User, error) {
	record, err := r.store.UserByID(id)
	if err != nil {
		return User{}, err
	}
	return toUser(record), nil
}

func (r *Registry) List() ([]User, error) {
	records, err := r.store.AllUsers()
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(records))
	for _, record := range records {
		out = append(out, toUser(record))
	}
	return out, nil
}

func (r *Registry) Save(req SaveUserRequest, defaultRole models.Role) (User, error) {
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Role = models.Role(strings.TrimSpace(string(req.Role)))
	if req.Username == "" {
		return User{}, errors.New("username is required")
	}
	if strings.ContainsAny(req.Username, " \t\r\n") {
		return User{}, errors.New("username cannot contain whitespace")
	}
	if req.Role == "" {
		req.Role = defaultRole
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}
	record, err := r.store.UserByUsername(req.Username)
	if errors.Is(err, db.ErrNotFound) {
		if strings.TrimSpace(req.Password) == "" {
			return User{}, errors.New("password is required for new users")
		}
		record, err = r.newRecord(newUserID(), req.Username, req.DisplayName, req.Role, req.Password)
		if err != nil {
			return User{}, err
		}
		record.Disabled = req.Disabled
		if err := r.store.UpsertUser(record); err != nil {
			return User{}, err
		}
		return toUser(record), nil
	}
	if err != nil {
		return User{}, err
	}
	record.DisplayName = req.DisplayName
	record.Role = req.Role
	record.Disabled = req.Disabled
	if strings.TrimSpace(req.Password) != "" {
		updated, err := r.newRecord(record.ID, record.Username, record.DisplayName, record.Role, req.Password)
		if err != nil {
			return User{}, err
		}
		updated.CreatedAt = record.CreatedAt
		updated.Disabled = record.Disabled
		record = updated
	}
	if err := r.store.UpsertUser(record); err != nil {
		return User{}, err
	}
	return toUser(record), nil
}

func newUserID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("user-%d", time.Now().UTC().UnixNano())
	}
	return "user-" + base64.RawURLEncoding.EncodeToString(buf)
}

func (r *Registry) newRecord(id, username, displayName string, role models.Role, password string) (db.UserRecord, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return db.UserRecord{}, err
	}
	iterations := 210000
	hash := hashPassword(password, salt, iterations)
	return db.UserRecord{
		ID:           id,
		Username:     username,
		DisplayName:  displayName,
		Role:         role,
		PasswordHash: base64.RawStdEncoding.EncodeToString(hash),
		Salt:         base64.RawStdEncoding.EncodeToString(salt),
		Iterations:   iterations,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func toUser(record db.UserRecord) User {
	return User{
		ID:          record.ID,
		Username:    record.Username,
		DisplayName: record.DisplayName,
		Role:        record.Role,
		Disabled:    record.Disabled,
	}
}

func verifyPassword(password, saltText, hashText string, iterations int) bool {
	salt, err := base64.RawStdEncoding.DecodeString(saltText)
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(hashText)
	if err != nil {
		return false
	}
	actual := hashPassword(password, salt, iterations)
	return hmac.Equal(expected, actual)
}

func hashPassword(password string, salt []byte, iterations int) []byte {
	if iterations < 1 {
		iterations = 1
	}
	block := pbkdf2SHA256([]byte(password), salt, iterations, 32)
	return block
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	var dk []byte
	hashLen := sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	for block := 1; block <= blocks; block++ {
		u := hmacSHA256(password, append(append([]byte(nil), salt...), byte(block>>24), byte(block>>16), byte(block>>8), byte(block)))
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			u = hmacSHA256(password, u)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func RequireRole(user User, allowed ...models.Role) error {
	for _, role := range allowed {
		if user.Role == role {
			return nil
		}
	}
	return fmt.Errorf("role %s is not allowed", user.Role)
}
