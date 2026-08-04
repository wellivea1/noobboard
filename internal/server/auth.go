package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/users"
)

// Sessions, login, and the middleware every route passes through. The session
// store, the login throttle and the cookie handling live together because they
// share one invariant: a request is authenticated exactly when a live session
// token resolves to a user whose credential version still matches.

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		RememberMe bool   `json:"remember_me"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	throttleKey := loginThrottleKey(r, req.Username)
	if retryAfter, blocked := a.loginLimiter.retryAfter(throttleKey); blocked {
		seconds := int((retryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
		a.deps.Audit.Record("anonymous", "auth.throttled", map[string]interface{}{"username": req.Username})
		writeError(w, http.StatusTooManyRequests, errors.New("too many login attempts; try again later"))
		return
	}
	user, err := a.deps.Users.Authenticate(req.Username, req.Password)
	if err != nil {
		a.loginLimiter.recordFailure(throttleKey)
		a.deps.Audit.Record("anonymous", "auth.failed", map[string]interface{}{"username": req.Username})
		writeError(w, http.StatusUnauthorized, errors.New("invalid credentials"))
		return
	}
	a.loginLimiter.recordSuccess(throttleKey)
	record, err := a.deps.Store.UserByID(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sessionTTL := a.deps.Config.Auth.SessionTimeout
	if req.RememberMe {
		sessionTTL = a.deps.Config.Auth.RememberSessionTimeout
	}
	session, err := a.sessions.createWithOptions(user, users.CredentialVersion(record), sessionTTL, req.RememberMe)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if req.RememberMe {
		if err := a.savePersistentSession(session); err != nil {
			a.sessions.delete(session.Token)
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	a.setSessionCookie(w, session)
	a.deps.Audit.Record(user.ID, "auth.login", map[string]interface{}{"username": user.Username, "remember": req.RememberMe})
	writeJSON(w, http.StatusOK, map[string]interface{}{"user": user, "csrf_token": session.CSRFToken})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if session := sessionFromRequest(r); session != "" {
		a.sessions.delete(session)
		_ = a.deps.Store.DeletePersistentSession(persistentSessionTokenHash(session))
	}
	http.SetCookie(w, &http.Cookie{Name: "noobboard_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.deps.Config.Auth.CookieSecure})
	http.SetCookie(w, &http.Cookie{Name: "hsd_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.deps.Config.Auth.CookieSecure})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	session := mustSession(r)
	if session.Persistent {
		session = a.renewPersistentSession(w, session)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"user": user, "csrf_token": session.CSRFToken})
}

type contextKey string

const (
	userContextKey    contextKey = "user"
	sessionContextKey contextKey = "session"
)

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := sessionFromRequest(r)
		session, ok := a.sessions.get(token)
		if !ok {
			session, ok = a.restorePersistentSession(token)
		}
		if ok {
			session, ok = a.validateSession(token, session)
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, session.User)
		ctx = context.WithValue(ctx, sessionContextKey, session)
		next(w, r.WithContext(ctx))
	}
}

func (a *App) validateSession(token string, sess session) (session, bool) {
	if sess.CredentialVersion == "" {
		return sess, true
	}
	user, err := a.deps.Users.ValidateSession(sess.User.ID, sess.CredentialVersion)
	if err != nil {
		a.sessions.delete(token)
		if sess.Persistent {
			_ = a.deps.Store.DeletePersistentSession(persistentSessionTokenHash(token))
		}
		return session{}, false
	}
	sess.User = user
	return sess, true
}

func (a *App) restorePersistentSession(token string) (session, bool) {
	if token == "" {
		return session{}, false
	}
	tokenHash := persistentSessionTokenHash(token)
	record, err := a.deps.Store.PersistentSessionByTokenHash(tokenHash)
	if err != nil {
		return session{}, false
	}
	now := time.Now().UTC()
	if record.ExpiresAt.IsZero() || now.After(record.ExpiresAt) {
		_ = a.deps.Store.DeletePersistentSession(tokenHash)
		return session{}, false
	}
	user, err := a.deps.Users.ValidateSession(record.UserID, record.CredentialVersion)
	if err != nil {
		_ = a.deps.Store.DeletePersistentSession(tokenHash)
		return session{}, false
	}
	record.LastSeenAt = now
	_ = a.deps.Store.UpsertPersistentSession(record)
	sess := session{
		Token:             token,
		CSRFToken:         record.CSRFToken,
		User:              user,
		CredentialVersion: record.CredentialVersion,
		CreatedAt:         record.CreatedAt,
		ExpiresAt:         record.ExpiresAt,
		Persistent:        true,
	}
	a.sessions.put(sess)
	return sess, true
}

func (a *App) renewPersistentSession(w http.ResponseWriter, sess session) session {
	if sess.Token == "" {
		return sess
	}
	now := time.Now().UTC()
	sess.ExpiresAt = now.Add(a.deps.Config.Auth.RememberSessionTimeout)
	sess.Persistent = true
	a.sessions.put(sess)
	if err := a.savePersistentSession(sess); err == nil {
		a.setSessionCookie(w, sess)
	}
	return sess
}

func (a *App) savePersistentSession(sess session) error {
	now := time.Now().UTC()
	createdAt := sess.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	record := db.PersistentSessionRecord{
		TokenHash:         persistentSessionTokenHash(sess.Token),
		UserID:            sess.User.ID,
		CredentialVersion: sess.CredentialVersion,
		CSRFToken:         sess.CSRFToken,
		CreatedAt:         createdAt,
		LastSeenAt:        now,
		ExpiresAt:         sess.ExpiresAt,
	}
	if err := a.deps.Store.UpsertPersistentSession(record); err != nil {
		return err
	}
	return a.deps.Store.PrunePersistentSessions(now, maxPersistentSessionEntries)
}

func (a *App) setSessionCookie(w http.ResponseWriter, sess session) {
	http.SetCookie(w, &http.Cookie{
		Name:     "noobboard_session",
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.deps.Config.Auth.CookieSecure,
		Expires:  sess.ExpiresAt,
	})
}

func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if err := users.RequireRole(mustUser(r), models.RoleAdmin); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
		next(w, r)
	})
}

func requireCSRF(r *http.Request) error {
	session := mustSession(r)
	if !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRFToken)) {
		return errors.New("csrf token required")
	}
	return nil
}

func sessionFromRequest(r *http.Request) string {
	for _, name := range []string{"noobboard_session", "hsd_session"} {
		cookie, err := r.Cookie(name)
		if err == nil {
			return cookie.Value
		}
	}
	return ""
}

func persistentSessionTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func mustUser(r *http.Request) users.User {
	user, _ := r.Context().Value(userContextKey).(users.User)
	return user
}

func mustSession(r *http.Request) session {
	session, _ := r.Context().Value(sessionContextKey).(session)
	return session
}

type session struct {
	Token             string
	CSRFToken         string
	User              users.User
	CredentialVersion string
	CreatedAt         time.Time
	ExpiresAt         time.Time
	Persistent        bool
	AgentArmedUntil   time.Time
}

type sessionStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]session
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, entries: map[string]session{}}
}

func (s *sessionStore) create(user users.User) (session, error) {
	return s.createWithOptions(user, "", s.ttl, false)
}

func (s *sessionStore) createWithOptions(user users.User, credentialVersion string, ttl time.Duration, persistent bool) (session, error) {
	now := time.Now().UTC()
	token, err := randomToken()
	if err != nil {
		return session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return session{}, err
	}
	if ttl <= 0 {
		ttl = s.ttl
	}
	entry := session{Token: token, CSRFToken: csrf, User: user, CredentialVersion: credentialVersion, CreatedAt: now, ExpiresAt: now.Add(ttl), Persistent: persistent}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(now)
	s.enforceLimitLocked(maxSessionEntries - 1)
	s.entries[token] = entry
	return entry, nil
}

func (s *sessionStore) put(entry session) {
	if entry.Token == "" {
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(now)
	s.enforceLimitLocked(maxSessionEntries - 1)
	s.entries[entry.Token] = entry
}

func (s *sessionStore) get(token string) (session, bool) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok || now.After(entry.ExpiresAt) {
		delete(s.entries, token)
		return session{}, false
	}
	if !entry.AgentArmedUntil.IsZero() && !now.Before(entry.AgentArmedUntil) {
		entry.AgentArmedUntil = time.Time{}
		s.entries[token] = entry
	}
	return entry, true
}

func (s *sessionStore) setAgentArmed(token string, until time.Time) (session, bool) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok || now.After(entry.ExpiresAt) {
		delete(s.entries, token)
		return session{}, false
	}
	if until.IsZero() || !until.After(now) {
		entry.AgentArmedUntil = time.Time{}
	} else {
		if until.After(entry.ExpiresAt) {
			until = entry.ExpiresAt
		}
		entry.AgentArmedUntil = until.UTC()
	}
	s.entries[token] = entry
	return entry, true
}

func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, token)
}

func (s *sessionStore) pruneExpiredLocked(now time.Time) {
	for token, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			delete(s.entries, token)
		}
	}
}

func (s *sessionStore) enforceLimitLocked(maxEntries int) {
	if maxEntries < 0 {
		maxEntries = 0
	}
	for len(s.entries) > maxEntries {
		var oldestToken string
		var oldestExpires time.Time
		for token, entry := range s.entries {
			if oldestToken == "" || entry.ExpiresAt.Before(oldestExpires) {
				oldestToken = token
				oldestExpires = entry.ExpiresAt
			}
		}
		if oldestToken == "" {
			return
		}
		delete(s.entries, oldestToken)
	}
}

type loginAttempt struct {
	Failures     int
	FirstFailure time.Time
	LockedUntil  time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	failures map[string]loginAttempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: map[string]loginAttempt{}}
}

func (l *loginLimiter) retryAfter(key string) (time.Duration, bool) {
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpiredLocked(now)
	entry, ok := l.failures[key]
	if !ok {
		return 0, false
	}
	if !entry.LockedUntil.IsZero() && now.Before(entry.LockedUntil) {
		return entry.LockedUntil.Sub(now), true
	}
	if !entry.FirstFailure.IsZero() && now.Sub(entry.FirstFailure) > loginFailureWindow {
		delete(l.failures, key)
	}
	return 0, false
}

func (l *loginLimiter) recordFailure(key string) {
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpiredLocked(now)
	l.enforceLimitLocked(maxLoginFailureKeys - 1)
	entry := l.failures[key]
	if entry.FirstFailure.IsZero() || now.Sub(entry.FirstFailure) > loginFailureWindow {
		entry = loginAttempt{FirstFailure: now}
	}
	entry.Failures++
	if entry.Failures >= maxLoginFailures {
		entry.LockedUntil = now.Add(loginLockoutTimeout)
	}
	l.failures[key] = entry
}

func (l *loginLimiter) recordSuccess(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

func (l *loginLimiter) pruneExpiredLocked(now time.Time) {
	for key, entry := range l.failures {
		if !entry.LockedUntil.IsZero() && now.Before(entry.LockedUntil) {
			continue
		}
		if entry.FirstFailure.IsZero() || now.Sub(entry.FirstFailure) > loginFailureWindow {
			delete(l.failures, key)
		}
	}
}

func (l *loginLimiter) enforceLimitLocked(maxEntries int) {
	if maxEntries < 0 {
		maxEntries = 0
	}
	for len(l.failures) > maxEntries {
		var oldestKey string
		var oldestTime time.Time
		for key, entry := range l.failures {
			timestamp := entry.FirstFailure
			if timestamp.IsZero() {
				timestamp = entry.LockedUntil
			}
			if oldestKey == "" || timestamp.Before(oldestTime) {
				oldestKey = key
				oldestTime = timestamp
			}
		}
		if oldestKey == "" {
			return
		}
		delete(l.failures, oldestKey)
	}
}

func loginThrottleKey(r *http.Request, username string) string {
	normalizedUser := strings.ToLower(strings.TrimSpace(username))
	if normalizedUser == "" {
		normalizedUser = "<empty>"
	}
	return clientAddress(r) + "|" + normalizedUser
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// --- probe latency baseline --------------------------------------------------

// probeSample is one poll's reading for one probe subject.
