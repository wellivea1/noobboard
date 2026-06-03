package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/adapters/probes"
	"github.com/wellivea1/noobboard/internal/adapters/unifi"
	"github.com/wellivea1/noobboard/internal/adapters/unraid"
	"github.com/wellivea1/noobboard/internal/audit"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/diagnostics"
	"github.com/wellivea1/noobboard/internal/llm"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/notifications"
	"github.com/wellivea1/noobboard/internal/privacy"
	"github.com/wellivea1/noobboard/internal/users"
	"github.com/wellivea1/noobboard/web"
)

type Collectors struct {
	Unraid unraid.Client
	Docker docker.Client
	UniFi  unifi.Client
	Probes probes.Client
}

type Dependencies struct {
	Config        config.Config
	Collectors    Collectors
	Store         db.Store
	Users         *users.Registry
	Audit         *audit.Auditor
	Notifications *notifications.Manager
	Redactor      *privacy.Redactor
	Diagnostics   diagnostics.RuleEngine
	LLM           llm.Client
	Version       string
}

type App struct {
	deps                   Dependencies
	sessions               *sessionStore
	loginLimiter           *loginLimiter
	settingsMu             sync.RWMutex
	runtimeIntegrationsSet bool
	openAIAuth             *openAIAuthStore
}

const maxRequestBodyBytes int64 = 1 << 20

type siteMode string

const (
	siteModeAdmin   siteMode = "admin"
	siteModeCompact siteMode = "compact"

	maxLoginFailures    = 5
	loginFailureWindow  = 5 * time.Minute
	loginLockoutTimeout = 10 * time.Minute
	maxSessionEntries   = 512
	maxLoginFailureKeys = 2048
	defaultLogLimit     = 80
	maxLogLimit         = 200
)

func New(deps Dependencies) (*App, error) {
	if deps.Store == nil || deps.Users == nil || deps.Redactor == nil || deps.LLM == nil {
		return nil, errors.New("server dependencies are incomplete")
	}
	deps.Config.Visibility = normalizeVisibilitySettings(deps.Config.Visibility)
	app := &App{
		deps:         deps,
		sessions:     newSessionStore(deps.Config.Auth.SessionTimeout),
		loginLimiter: newLoginLimiter(),
		openAIAuth:   newOpenAIAuthStore(),
	}
	if settings, ok, err := deps.Store.RuntimeSettings(); err != nil {
		return nil, err
	} else if ok {
		if err := app.applyRuntimeSettings(settings); err != nil {
			return nil, err
		}
	}
	return app, nil
}

func (a *App) Router() http.Handler {
	return a.AdminRouter()
}

func (a *App) AdminRouter() http.Handler {
	mux := http.NewServeMux()
	a.registerSharedRoutes(mux)
	mux.HandleFunc("GET /api/admin/status/full", a.requireAdmin(a.adminStatus))
	mux.HandleFunc("GET /api/admin/incidents", a.requireAdmin(a.adminIncidents))
	mux.HandleFunc("GET /api/admin/audit", a.requireAdmin(a.adminAudit))
	mux.HandleFunc("GET /api/admin/users", a.requireAdmin(a.listUsers))
	mux.HandleFunc("POST /api/admin/users", a.requireAdmin(a.saveUser))
	mux.HandleFunc("GET /api/admin/apps/", a.requireAdmin(a.adminAppRead))
	mux.HandleFunc("POST /api/admin/apps/", a.requireAdmin(a.adminAppMutation))
	mux.HandleFunc("POST /api/admin/diagnose", a.requireAdmin(a.adminDiagnose))
	mux.HandleFunc("GET /api/admin/settings/visibility", a.requireAdmin(a.getVisibilitySettings))
	mux.HandleFunc("POST /api/admin/settings/visibility", a.requireAdmin(a.updateVisibilitySettings))
	mux.HandleFunc("GET /api/admin/settings/roles", a.requireAdmin(a.getRoleSettings))
	mux.HandleFunc("POST /api/admin/settings/roles", a.requireAdmin(a.updateRoleSettings))
	mux.HandleFunc("GET /api/admin/settings/blacklist", a.requireAdmin(a.getBlacklistSettings))
	mux.HandleFunc("POST /api/admin/settings/blacklist", a.requireAdmin(a.updateBlacklistSettings))
	mux.HandleFunc("GET /api/admin/settings/apps", a.requireAdmin(a.getAppCatalogSettings))
	mux.HandleFunc("POST /api/admin/settings/apps", a.requireAdmin(a.updateAppCatalogSettings))
	mux.HandleFunc("GET /api/admin/settings/llm", a.requireAdmin(a.getLLMSettings))
	mux.HandleFunc("POST /api/admin/settings/llm", a.requireAdmin(a.updateLLMSettings))
	mux.HandleFunc("POST /api/admin/settings/llm/openai/browser/start", a.requireAdmin(a.startOpenAIChatGPTBrowserAuth))
	mux.HandleFunc("POST /api/admin/settings/llm/openai/browser/finish", a.requireAdmin(a.finishOpenAIChatGPTBrowserAuth))
	mux.HandleFunc("POST /api/admin/settings/llm/openai/headless/start", a.requireAdmin(a.startOpenAIChatGPTHeadlessAuth))
	mux.HandleFunc("POST /api/admin/settings/llm/openai/headless/poll", a.requireAdmin(a.pollOpenAIChatGPTHeadlessAuth))
	mux.HandleFunc("GET /api/admin/settings/integrations", a.requireAdmin(a.getIntegrationSettings))
	mux.HandleFunc("POST /api/admin/settings/integrations", a.requireAdmin(a.updateIntegrationSettings))
	mux.HandleFunc("GET /api/admin/settings/notifications", a.requireAdmin(a.getNotificationSettings))
	mux.HandleFunc("POST /api/admin/settings/notifications", a.requireAdmin(a.updateNotificationSettings))
	mux.HandleFunc("GET /site-config.js", a.siteConfig(siteModeAdmin))
	mux.Handle("GET /", a.staticFiles())
	return securityHeaders(limitRequestBody(mux), a.deps.Config.Server)
}

func (a *App) CompactRouter() http.Handler {
	mux := http.NewServeMux()
	a.registerSharedRoutes(mux)
	a.registerCompactAdminUnavailable(mux)
	mux.HandleFunc("GET /site-config.js", a.siteConfig(siteModeCompact))
	mux.Handle("GET /", a.staticFiles())
	return securityHeaders(limitRequestBody(mux), a.deps.Config.Server)
}

func (a *App) registerSharedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", a.requireAuth(a.logout))
	mux.HandleFunc("GET /api/auth/me", a.requireAuth(a.me))
	mux.HandleFunc("GET /api/status/summary", a.requireAuth(a.statusSummary))
	mux.HandleFunc("GET /api/apps", a.requireAuth(a.apps))
	mux.HandleFunc("GET /api/apps/", a.requireAuth(a.appByID))
	mux.HandleFunc("POST /api/user/diagnose", a.requireAuth(a.userDiagnose))
	mux.HandleFunc("POST /api/user/notify-admin", a.requireAuth(a.notifyAdmin))
	mux.HandleFunc("GET /api/user/notification-preferences", a.requireAuth(a.getNotificationPreferences))
	mux.HandleFunc("POST /api/user/notification-preferences", a.requireAuth(a.saveNotificationPreferences))
}

func (a *App) registerCompactAdminUnavailable(mux *http.ServeMux) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		mux.HandleFunc(method+" /api/admin", a.adminUnavailable)
		mux.HandleFunc(method+" /api/admin/", a.adminUnavailable)
	}
}

func (a *App) Snapshot(ctx context.Context, role models.Role) (models.Snapshot, error) {
	full, err := a.fullSnapshot(ctx)
	if err != nil {
		return models.Snapshot{}, err
	}
	if role == models.RoleAdmin {
		return full, nil
	}
	return privacy.FilterSnapshotForRole(full, role, a.redactorSnapshot()), nil
}

func (a *App) Flush() {
	_ = a.deps.Store.Flush()
}

func (a *App) fullSnapshot(ctx context.Context) (models.Snapshot, error) {
	return a.collectSnapshot(ctx, true)
}

func (a *App) readOnlySnapshot(ctx context.Context) (models.Snapshot, error) {
	return a.collectSnapshot(ctx, false)
}

func (a *App) collectSnapshot(ctx context.Context, processNotifications bool) (models.Snapshot, error) {
	cfg, collectors := a.runtimeSnapshot()
	infra, unraidLogs, err := collectors.Unraid.Status(ctx)
	if err != nil {
		infra = collectorFailureStatus("unraid", err)
		unraidLogs = nil
	}
	apps, err := collectors.Docker.Apps(ctx)
	if err != nil {
		infra.DockerServiceAvailable = false
		infra.SourceHealth.Docker = err.Error()
		apps = nil
	} else if infra.SourceHealth.Docker == "" {
		infra.DockerServiceAvailable = true
		infra.SourceHealth.Docker = fmt.Sprintf("%d app(s) collected", len(apps))
	} else {
		infra.DockerServiceAvailable = true
	}
	if unifiInfra, err := collectors.UniFi.Status(ctx); err == nil {
		mergeUniFiStatus(&infra, unifiInfra)
	} else {
		infra.UniFiWANUp = false
		infra.UniFiGatewayReachable = false
		infra.SourceHealth.UniFi = err.Error()
	}
	if probeInfra, err := collectors.Probes.Status(ctx); err == nil {
		if probeSourceHasData(probeInfra.SourceHealth.Probes, "internet") {
			infra.InternetReachable = probeInfra.InternetReachable
		}
		if probeSourceHasData(probeInfra.SourceHealth.Probes, "dns") {
			infra.DNSOK = probeInfra.DNSOK
		}
		if probeSourceHasData(probeInfra.SourceHealth.Probes, "router") {
			infra.RouterReachable = probeInfra.RouterReachable
		}
		if probeSourceHasData(probeInfra.SourceHealth.Probes, "nas") {
			infra.NASReachable = probeInfra.NASReachable
		}
		infra.SourceHealth.Probes = probeInfra.SourceHealth.Probes
	} else {
		infra.SourceHealth.Probes = err.Error()
	}
	for i := range apps {
		apps[i].RecentLogs = append(apps[i].RecentLogs, unraidLogs...)
	}
	applyAppCatalog(apps, cfg.AppCatalog)
	snapshot := models.Snapshot{
		GeneratedAt:          time.Now().UTC(),
		Infrastructure:       infra,
		Apps:                 apps,
		Visibility:           cfg.Visibility,
		LLMPolicies:          cfg.LLM.Policies,
		DiagnosticsAvailable: llm.ProviderAvailable(cfg.LLM),
		DiagnosticsProvider:  cfg.LLM.Provider,
		IntegrationMode:      cfg.Integrations.Mode,
	}
	if cfg.Integrations.Mode == "fixture" {
		snapshot.FixtureScenario = cfg.FixtureScenario
	}
	result := a.deps.Diagnostics.Evaluate(snapshot)
	snapshot.OverallStatus = result.OverallStatus
	snapshot.ServerSummary = result.ServerSummary
	snapshot.AdminSummary = result.AdminSummary
	snapshot.Facts = result.Facts
	snapshot.Incidents = result.Incidents
	snapshot.Apps = result.Apps
	prefs, _ := a.deps.Store.AllNotificationPreferences()
	snapshot.NotificationInfo = notifications.RollupFromPreferences(cfg.Notifications.Enabled, cfg.Notifications.GlobalOptInEnabled, prefs)
	auditTail, _ := a.deps.Store.AuditTail(50)
	snapshot.AuditTail = auditTail
	if processNotifications {
		_ = a.deps.Notifications.ProcessSnapshot(ctx, snapshot)
	}
	return snapshot, nil
}

func collectorFailureStatus(source string, err error) models.InfrastructureStatus {
	infra := models.InfrastructureStatus{
		LastCheckedAt: time.Now().UTC(),
		SourceHealth:  models.SourceHealth{},
	}
	switch source {
	case "unraid":
		infra.NASReachable = false
		infra.UnraidAPIReachable = false
		infra.UnraidArrayState = "unknown"
		infra.UnraidArrayHealthy = false
		infra.DockerServiceAvailable = false
		infra.SourceHealth.Unraid = err.Error()
	}
	return infra
}

func mergeUniFiStatus(infra *models.InfrastructureStatus, unifiInfra models.InfrastructureStatus) {
	infra.UniFiWANUp = unifiInfra.UniFiWANUp
	infra.UniFiGatewayReachable = unifiInfra.UniFiGatewayReachable
	if unifiInfra.UniFiGatewayReachable {
		infra.RouterReachable = true
	}
	infra.UniFiSiteID = unifiInfra.UniFiSiteID
	infra.UniFiSiteName = unifiInfra.UniFiSiteName
	infra.UniFiDeviceCount = unifiInfra.UniFiDeviceCount
	infra.UniFiOfflineDeviceCount = unifiInfra.UniFiOfflineDeviceCount
	infra.UniFiClientCount = unifiInfra.UniFiClientCount
	infra.UniFiFirmwareUpdates = unifiInfra.UniFiFirmwareUpdates
	infra.UniFiWANCount = unifiInfra.UniFiWANCount
	infra.UniFiWarnings = append([]string(nil), unifiInfra.UniFiWarnings...)
	if unifiInfra.NASLinkSpeedMbps > 0 {
		infra.NASLinkSpeedMbps = unifiInfra.NASLinkSpeedMbps
	}
	if unifiInfra.ExpectedNASLinkMbps > 0 {
		infra.ExpectedNASLinkMbps = unifiInfra.ExpectedNASLinkMbps
	}
	if unifiInfra.SourceHealth.UniFi != "" {
		infra.SourceHealth.UniFi = unifiInfra.SourceHealth.UniFi
	}
}

func probeSourceHasData(source, name string) bool {
	health := strings.ToLower(strings.TrimSpace(source))
	if health == "" {
		return false
	}
	return !strings.Contains(health, strings.ToLower(name)+" skipped")
}

func applyAppCatalog(apps []models.AppStatus, catalog config.AppCatalogConfig) {
	for i := range apps {
		if iconURL := appIconOverride(apps[i], catalog.IconOverrides); iconURL != "" {
			apps[i].IconURL = iconURL
			apps[i].IconSource = "custom"
			continue
		}
		if apps[i].IconURL == "" {
			if iconURL := builtInAppIcon(apps[i]); iconURL != "" {
				apps[i].IconURL = iconURL
				apps[i].IconSource = "built-in"
			}
		}
	}
}

func appIconOverride(app models.AppStatus, overrides map[string]string) string {
	for _, key := range []string{app.AppID, app.ContainerName, app.DisplayName} {
		if key == "" {
			continue
		}
		if value := overrides[key]; value != "" {
			return value
		}
		if value := overrides[strings.ToLower(key)]; value != "" {
			return value
		}
	}
	return ""
}

func builtInAppIcon(app models.AppStatus) string {
	terms := []string{app.AppID, app.ContainerName, app.DisplayName, app.ImageRef}
	for _, term := range terms {
		slug := commonAppIconSlug(term)
		if slug == "" {
			continue
		}
		path := "/app-icons/" + slug + ".svg"
		if staticAssetExists("public" + path) {
			return path
		}
	}
	if app.ContainerID != "" || app.ContainerName != "" || app.DockerState != "" || app.ImageRef != "" {
		if staticAssetExists("public/app-icons/container.svg") {
			return "/app-icons/container.svg"
		}
	}
	return ""
}

func commonAppIconSlug(value string) string {
	normalized := strings.ToLower(value)
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch {
	case strings.Contains(normalized, "emby"),
		strings.Contains(normalized, "plex"),
		strings.Contains(normalized, "jellyfin"):
		return "media-server"
	case strings.Contains(normalized, "home-assistant"),
		strings.Contains(normalized, "homeassistant"):
		return "smart-home"
	case strings.Contains(normalized, "nextcloud"),
		strings.Contains(normalized, "seafile"),
		strings.Contains(normalized, "owncloud"),
		strings.Contains(normalized, "syncthing"),
		strings.Contains(normalized, "duplicati"):
		return "cloud-storage"
	case strings.Contains(normalized, "qbittorrent"),
		strings.Contains(normalized, "sabnzbd"),
		strings.Contains(normalized, "nzbget"),
		strings.Contains(normalized, "deluge"),
		strings.Contains(normalized, "transmission"):
		return "download-client"
	case strings.Contains(normalized, "sonarr"),
		strings.Contains(normalized, "radarr"),
		strings.Contains(normalized, "lidarr"),
		strings.Contains(normalized, "readarr"),
		strings.Contains(normalized, "prowlarr"):
		return "media-automation"
	case strings.Contains(normalized, "pihole"),
		strings.Contains(normalized, "pi-hole"),
		strings.Contains(normalized, "adguard"):
		return "dns-filter"
	case strings.Contains(normalized, "unifi"),
		strings.Contains(normalized, "omada"),
		strings.Contains(normalized, "traefik"),
		strings.Contains(normalized, "nginx"),
		strings.Contains(normalized, "swag"):
		return "network"
	case strings.Contains(normalized, "postgres"),
		strings.Contains(normalized, "mariadb"),
		strings.Contains(normalized, "mysql"),
		strings.Contains(normalized, "redis"),
		strings.Contains(normalized, "database"):
		return "database"
	default:
		return ""
	}
}

func staticAssetExists(path string) bool {
	file, err := web.Files.Open(path)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func (a *App) staticFiles() http.Handler {
	public, err := fs.Sub(web.Files, "public")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(public))
}

func (a *App) siteConfig(mode siteMode) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintf(w, "window.__NOOBBOARD_SITE_MODE__ = %q;\nwindow.__HSD_SITE_MODE__ = window.__NOOBBOARD_SITE_MODE__;\n", string(mode))
	}
}

func (a *App) adminUnavailable(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, errors.New("admin API is not available on this site"))
}

func (a *App) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": a.deps.Version})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
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
	session, err := a.sessions.create(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "noobboard_session",
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.deps.Config.Auth.CookieSecure,
		Expires:  session.ExpiresAt,
	})
	a.deps.Audit.Record(user.ID, "auth.login", map[string]interface{}{"username": user.Username})
	writeJSON(w, http.StatusOK, map[string]interface{}{"user": user, "csrf_token": session.CSRFToken})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if session := sessionFromRequest(r); session != "" {
		a.sessions.delete(session)
	}
	http.SetCookie(w, &http.Cookie{Name: "noobboard_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.deps.Config.Auth.CookieSecure})
	http.SetCookie(w, &http.Cookie{Name: "hsd_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.deps.Config.Auth.CookieSecure})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	token := mustSession(r).CSRFToken
	writeJSON(w, http.StatusOK, map[string]interface{}{"user": user, "csrf_token": token})
}

func (a *App) statusSummary(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.Snapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) apps(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.Snapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot.Apps)
}

func (a *App) appByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	snapshot, err := a.Snapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, app := range snapshot.Apps {
		if app.AppID == id {
			writeJSON(w, http.StatusOK, app)
			return
		}
	}
	writeError(w, http.StatusNotFound, db.ErrNotFound)
}

func (a *App) adminStatus(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.Snapshot(r.Context(), models.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) adminIncidents(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.Snapshot(r.Context(), models.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot.Incidents)
}

func (a *App) adminAudit(w http.ResponseWriter, _ *http.Request) {
	tail, err := a.deps.Store.AuditTail(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, tail)
}

func (a *App) listUsers(w http.ResponseWriter, _ *http.Request) {
	usersList, err := a.deps.Users.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, usersList)
}

func (a *App) saveUser(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body users.SaveUserRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	defaultRole := a.defaultRole()
	user, err := a.deps.Users.Save(body, defaultRole)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "user.saved", map[string]interface{}{"username": user.Username, "role": string(user.Role), "disabled": user.Disabled})
	writeJSON(w, http.StatusOK, user)
}

func (a *App) adminAppMutation(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/icon"):
		a.updateAppIcon(w, r)
	case strings.HasSuffix(r.URL.Path, "/action"):
		a.controlApp(w, r)
	default:
		writeError(w, http.StatusNotFound, db.ErrNotFound)
	}
}

func (a *App) adminAppRead(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/logs"):
		a.appLogs(w, r)
	default:
		writeError(w, http.StatusNotFound, db.ErrNotFound)
	}
}

func (a *App) updateAppIcon(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/apps/"), "/icon")
	id = strings.Trim(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	var body struct {
		IconURL string `json:"icon_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	iconURL, err := config.NormalizeIconURL(body.IconURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.Lock()
	if a.deps.Config.AppCatalog.IconOverrides == nil {
		a.deps.Config.AppCatalog.IconOverrides = map[string]string{}
	}
	if iconURL == "" {
		delete(a.deps.Config.AppCatalog.IconOverrides, id)
		delete(a.deps.Config.AppCatalog.IconOverrides, strings.ToLower(id))
	} else {
		a.deps.Config.AppCatalog.IconOverrides[id] = iconURL
		a.deps.Config.AppCatalog.IconOverrides[strings.ToLower(id)] = iconURL
	}
	settings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(settings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "app.icon.saved", map[string]interface{}{"app_id": id, "has_icon": iconURL != ""})
	writeJSON(w, http.StatusOK, a.configSnapshot().AppCatalog)
}

func (a *App) controlApp(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/apps/"), "/action")
	id = strings.Trim(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	action, err := docker.ParseContainerAction(body.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	snapshot, err := a.Snapshot(r.Context(), models.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, id)
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	result, err := a.deps.Collectors.Docker.ControlContainer(r.Context(), app, action)
	if err != nil {
		a.deps.Audit.Record(mustUser(r).ID, "app.container.action_failed", map[string]interface{}{"app_id": id, "action": string(action), "error": err.Error()})
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "app.container.action", map[string]interface{}{"app_id": id, "action": string(action), "container_name": app.ContainerName})
	writeJSON(w, http.StatusAccepted, result)
}

func (a *App) appLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/apps/"), "/logs")
	id = strings.Trim(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	limit := parseLogLimit(r.URL.Query().Get("limit"))
	snapshot, err := a.Snapshot(r.Context(), models.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, id)
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	lines, err := a.deps.Collectors.Docker.Logs(r.Context(), app, docker.LogOptions{Limit: limit})
	if err != nil {
		a.deps.Audit.Record(mustUser(r).ID, "app.container.logs_failed", map[string]interface{}{"app_id": id, "container_name": app.ContainerName, "error": err.Error()})
		writeError(w, http.StatusBadGateway, err)
		return
	}
	redacted, changed := a.deps.Redactor.RedactLogs(lines)
	a.deps.Audit.Record(mustUser(r).ID, "app.container.logs", map[string]interface{}{"app_id": id, "container_name": app.ContainerName, "line_count": len(redacted), "redacted": changed})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"app_id":         app.AppID,
		"container_name": app.ContainerName,
		"limit":          limit,
		"redacted":       changed,
		"logs":           redacted,
	})
}

func parseLogLimit(value string) int {
	limit := defaultLogLimit
	if strings.TrimSpace(value) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > maxLogLimit {
		return maxLogLimit
	}
	return limit
}

func findAppByID(apps []models.AppStatus, id string) (models.AppStatus, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, app := range apps {
		for _, candidate := range []string{app.AppID, app.ContainerName, app.DisplayName} {
			if strings.ToLower(strings.TrimSpace(candidate)) == id {
				return app, true
			}
		}
	}
	return models.AppStatus{}, false
}

func (a *App) adminDiagnose(w http.ResponseWriter, r *http.Request) {
	a.diagnose(w, r, llm.ModeAdminRequested, models.RoleAdmin)
}

func (a *App) userDiagnose(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.RLock()
	visibility := a.deps.Config.Visibility
	role := compactDiagnosisRole(mustUser(r).Role, visibility.DefaultRole)
	allowed := roleCanUseLLM(visibility, role)
	a.settingsMu.RUnlock()
	if !allowed {
		writeError(w, http.StatusForbidden, errors.New("status chat is disabled for this role"))
		return
	}
	a.diagnose(w, r, llm.ModeGeneralUserRequested, role)
}

func (a *App) diagnose(w http.ResponseWriter, r *http.Request, mode llm.Mode, role models.Role) {
	cfg, llmClient := a.llmRuntimeSnapshot()
	if !cfg.LLM.Enabled {
		writeError(w, http.StatusForbidden, errors.New("llm is disabled"))
		return
	}
	if !llm.ProviderAvailable(cfg.LLM) {
		writeError(w, http.StatusForbidden, errors.New("diagnostics require NOOBBOARD_LLM_PROVIDER=openai or anthropic with a matching API key, or an OpenAI ChatGPT connector configured in LLM settings"))
		return
	}
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	body.Question = strings.TrimSpace(body.Question)
	if len(body.Question) > 1000 {
		body.Question = body.Question[:1000]
	}
	full, err := a.fullSnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	policy, ok := cfg.LLM.Policies[string(mode)]
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("missing llm policy %s", mode))
		return
	}
	policy.RecipientRole = role
	diagnosis, err := llmClient.Diagnose(r.Context(), llm.Request{
		Mode:         mode,
		Policy:       policy,
		Snapshot:     full,
		Question:     body.Question,
		ActorID:      mustUser(r).ID,
		LiveSnapshot: a.readOnlySnapshot,
		ToolAudit: func(name string, ok bool, errText string) {
			details := map[string]interface{}{"mode": string(mode), "tool": name, "ok": ok}
			if errText != "" {
				details["error"] = errText
			}
			a.deps.Audit.Record(mustUser(r).ID, "llm.agent_tool", details)
		},
	})
	if err != nil {
		a.deps.Audit.Record(mustUser(r).ID, "llm.failed", map[string]interface{}{"mode": string(mode), "error": err.Error()})
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "llm.diagnosis", map[string]interface{}{"mode": string(mode), "incident_type": string(diagnosis.IncidentType), "admin_message": diagnosis.AdminMessage})
	writeJSON(w, http.StatusOK, diagnosis)
}

func (a *App) notifyAdmin(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	if len(body.Message) > 1000 {
		body.Message = body.Message[:1000]
	}
	a.deps.Audit.Record(mustUser(r).ID, "user.notify_admin", map[string]interface{}{"message": body.Message})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func (a *App) getNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := a.deps.Notifications.Preferences(mustUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (a *App) saveNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var pref models.NotificationPreference
	if err := json.NewDecoder(r.Body).Decode(&pref); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pref.UserID = mustUser(r).ID
	snapshot, err := a.Snapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := a.deps.Notifications.SavePreference(pref, snapshot.Apps); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	a.deps.Audit.Record(pref.UserID, "notification.preference.saved", map[string]interface{}{"app_id": pref.AppID})
	writeJSON(w, http.StatusOK, pref)
}

func (a *App) getVisibilitySettings(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.RLock()
	visibility := a.deps.Config.Visibility
	a.settingsMu.RUnlock()
	writeJSON(w, http.StatusOK, visibility)
}

func (a *App) getRoleSettings(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.RLock()
	visibility := normalizeVisibilitySettings(a.deps.Config.Visibility)
	a.settingsMu.RUnlock()
	snapshot, err := a.Snapshot(r.Context(), models.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	usersList, err := a.deps.Users.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"visibility": visibility,
		"apps":       snapshot.Apps,
		"users":      usersList,
	})
}

func (a *App) getBlacklistSettings(w http.ResponseWriter, _ *http.Request) {
	a.settingsMu.RLock()
	privacyCfg := a.deps.Config.Privacy
	a.settingsMu.RUnlock()
	writeJSON(w, http.StatusOK, privacyCfg)
}

func (a *App) getAppCatalogSettings(w http.ResponseWriter, _ *http.Request) {
	a.settingsMu.RLock()
	cfg := a.deps.Config.AppCatalog
	a.settingsMu.RUnlock()
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) getLLMSettings(w http.ResponseWriter, _ *http.Request) {
	a.settingsMu.RLock()
	cfg := a.deps.Config.LLM
	a.settingsMu.RUnlock()
	writeJSON(w, http.StatusOK, llmSettingsResponse(cfg))
}

func (a *App) getIntegrationSettings(w http.ResponseWriter, _ *http.Request) {
	a.settingsMu.RLock()
	cfg := a.deps.Config.Integrations
	a.settingsMu.RUnlock()
	writeJSON(w, http.StatusOK, integrationSettingsResponse(cfg))
}

func (a *App) getNotificationSettings(w http.ResponseWriter, _ *http.Request) {
	a.settingsMu.RLock()
	cfg := a.deps.Config.Notifications
	a.settingsMu.RUnlock()
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) updateVisibilitySettings(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var settings models.VisibilitySettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings = normalizeVisibilitySettings(settings)
	next := a.configSnapshot()
	next.Visibility = settings
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.Lock()
	a.deps.Config.Visibility = settings
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "settings.visibility.saved", map[string]interface{}{"path": r.URL.Path})
	writeJSON(w, http.StatusOK, settings)
}

func (a *App) updateRoleSettings(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var settings models.VisibilitySettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings = normalizeVisibilitySettings(settings)
	next := a.configSnapshot()
	next.Visibility = settings
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.Lock()
	a.deps.Config.Visibility = settings
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "settings.roles.saved", map[string]interface{}{"roles": len(settings.Roles)})
	writeJSON(w, http.StatusOK, settings)
}

func (a *App) updateBlacklistSettings(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var settings config.PrivacyConfig
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.Lock()
	a.deps.Config.Privacy = settings
	a.deps.Redactor = privacy.NewRedactor(settings)
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "settings.blacklist.saved", map[string]interface{}{"path": r.URL.Path})
	writeJSON(w, http.StatusOK, settings)
}

func (a *App) updateAppCatalogSettings(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var settings config.AppCatalogConfig
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if settings.IconOverrides == nil {
		settings.IconOverrides = map[string]string{}
	}
	for key, iconURL := range settings.IconOverrides {
		normalized, err := config.NormalizeIconURL(iconURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("icon override %s: %w", key, err))
			return
		}
		if normalized == "" {
			delete(settings.IconOverrides, key)
		} else {
			settings.IconOverrides[key] = normalized
		}
	}
	a.settingsMu.Lock()
	a.deps.Config.AppCatalog = settings
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "settings.apps.saved", map[string]interface{}{"path": r.URL.Path, "icon_overrides": len(settings.IconOverrides)})
	writeJSON(w, http.StatusOK, settings)
}

func (a *App) updateLLMSettings(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	current := a.configSnapshot().LLM
	settings, err := decodeLLMSettingsUpdate(r, current)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings = normalizeLLMSettings(settings)
	next := a.configSnapshot()
	next.LLM = settings
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.Lock()
	a.deps.Config.LLM = settings
	a.deps.LLM = llm.NewClient(settings, a.deps.Redactor)
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "settings.llm.saved", map[string]interface{}{"path": r.URL.Path, "provider": settings.Provider})
	if chatGPTAuthPresent(current) && !chatGPTAuthPresent(settings) {
		a.deps.Audit.Record(mustUser(r).ID, "settings.llm.chatgpt.cleared", map[string]interface{}{"path": r.URL.Path})
	}
	writeJSON(w, http.StatusOK, llmSettingsResponse(settings))
}

func (a *App) updateIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	current := a.configSnapshot().Integrations
	settings, err := decodeIntegrationSettingsUpdate(r, current)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings, err = normalizeIntegrationSettings(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings, err = hydrateIntegrationSecretFiles(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	next := a.configSnapshot()
	next.Integrations = settings
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	collectors := collectorsForConfig(next)
	a.settingsMu.Lock()
	a.deps.Config.Integrations = settings
	a.deps.Collectors = collectors
	a.runtimeIntegrationsSet = true
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "settings.integrations.saved", map[string]interface{}{
		"path":       r.URL.Path,
		"mode":       settings.Mode,
		"unraid_set": settings.UnraidBaseURL != "" && settings.UnraidAPIKey != "",
		"unifi_set":  settings.UniFiBaseURL != "" && settings.UniFiAPIKey != "",
	})
	writeJSON(w, http.StatusOK, integrationSettingsResponse(settings))
}

func (a *App) updateNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var settings config.NotificationConfig
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.Lock()
	a.deps.Config.Notifications = settings
	a.deps.Notifications.UpdateConfig(settings)
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "settings.notifications.saved", map[string]interface{}{"path": r.URL.Path, "enabled": settings.Enabled})
	writeJSON(w, http.StatusOK, settings)
}

type llmSettingsView struct {
	Enabled               bool                        `json:"enabled"`
	Provider              string                      `json:"provider"`
	OpenAIAuthMethod      string                      `json:"openai_auth_method"`
	OpenAIModel           string                      `json:"openai_model"`
	OpenAIAPIKeySet       bool                        `json:"openai_api_key_set"`
	ChatGPTConnected      bool                        `json:"chatgpt_connected"`
	ChatGPTAccessTokenSet bool                        `json:"chatgpt_access_token_set"`
	ChatGPTAccountIDSet   bool                        `json:"chatgpt_account_id_set"`
	AnthropicModel        string                      `json:"anthropic_model"`
	AnthropicAPIKeySet    bool                        `json:"anthropic_api_key_set"`
	Timeout               time.Duration               `json:"timeout"`
	Policies              map[string]models.LLMPolicy `json:"policies"`
}

type llmSettingsUpdate struct {
	Enabled              *bool                       `json:"enabled"`
	Provider             *string                     `json:"provider"`
	OpenAIAuthMethod     *string                     `json:"openai_auth_method"`
	OpenAIModel          *string                     `json:"openai_model"`
	OpenAIAPIKey         *string                     `json:"openai_api_key"`
	ClearOpenAIAPIKey    bool                        `json:"clear_openai_api_key"`
	ClearChatGPTAuth     bool                        `json:"clear_chatgpt_auth"`
	AnthropicModel       *string                     `json:"anthropic_model"`
	AnthropicAPIKey      *string                     `json:"anthropic_api_key"`
	ClearAnthropicAPIKey bool                        `json:"clear_anthropic_api_key"`
	Timeout              *time.Duration              `json:"timeout"`
	Policies             map[string]models.LLMPolicy `json:"policies"`
}

type integrationSettingsView struct {
	Mode              string `json:"mode"`
	UnraidBaseURL     string `json:"unraid_base_url"`
	UnraidAPIKeySet   bool   `json:"unraid_api_key_set"`
	UnraidAPIKeyFile  string `json:"unraid_api_key_file,omitempty"`
	UnraidSSHFallback bool   `json:"unraid_ssh_fallback"`
	UnraidSSHHost     string `json:"unraid_ssh_host,omitempty"`
	UnraidSSHPort     int    `json:"unraid_ssh_port"`
	UnraidSSHUser     string `json:"unraid_ssh_user,omitempty"`
	UnraidSSHKeyFile  string `json:"unraid_ssh_key_file,omitempty"`
	UnraidSSHCommand  string `json:"unraid_ssh_command,omitempty"`
	UniFiBaseURL      string `json:"unifi_base_url"`
	UniFiAPIKeySet    bool   `json:"unifi_api_key_set"`
	UniFiAPIKeyFile   string `json:"unifi_api_key_file,omitempty"`
	UniFiSiteID       string `json:"unifi_site_id"`
	UniFiInsecureTLS  bool   `json:"unifi_insecure_tls"`
	InternetProbeURL  string `json:"internet_probe_url"`
	DNSProbeHost      string `json:"dns_probe_host"`
	RouterProbeTarget string `json:"router_probe_target"`
	NASProbeTarget    string `json:"nas_probe_target"`
}

type integrationSettingsUpdate struct {
	Mode              *string `json:"mode"`
	UnraidBaseURL     *string `json:"unraid_base_url"`
	UnraidAPIKey      *string `json:"unraid_api_key"`
	ClearUnraidAPIKey bool    `json:"clear_unraid_api_key"`
	UnraidAPIKeyFile  *string `json:"unraid_api_key_file"`
	UnraidSSHFallback *bool   `json:"unraid_ssh_fallback"`
	UnraidSSHHost     *string `json:"unraid_ssh_host"`
	UnraidSSHPort     *int    `json:"unraid_ssh_port"`
	UnraidSSHUser     *string `json:"unraid_ssh_user"`
	UnraidSSHKeyFile  *string `json:"unraid_ssh_key_file"`
	UnraidSSHCommand  *string `json:"unraid_ssh_command"`
	UniFiBaseURL      *string `json:"unifi_base_url"`
	UniFiAPIKey       *string `json:"unifi_api_key"`
	ClearUniFiAPIKey  bool    `json:"clear_unifi_api_key"`
	UniFiAPIKeyFile   *string `json:"unifi_api_key_file"`
	UniFiSiteID       *string `json:"unifi_site_id"`
	UniFiInsecureTLS  *bool   `json:"unifi_insecure_tls"`
	InternetProbeURL  *string `json:"internet_probe_url"`
	DNSProbeHost      *string `json:"dns_probe_host"`
	RouterProbeTarget *string `json:"router_probe_target"`
	NASProbeTarget    *string `json:"nas_probe_target"`
}

func llmSettingsResponse(cfg config.LLMConfig) llmSettingsView {
	return llmSettingsView{
		Enabled:               cfg.Enabled,
		Provider:              cfg.Provider,
		OpenAIAuthMethod:      firstNonEmpty(strings.TrimSpace(cfg.OpenAIAuthMethod), config.OpenAIAuthMethodAPIKey),
		OpenAIModel:           cfg.OpenAIModel,
		OpenAIAPIKeySet:       strings.TrimSpace(cfg.OpenAIAPIKey) != "",
		ChatGPTConnected:      strings.TrimSpace(cfg.ChatGPTRefreshToken) != "" && strings.TrimSpace(cfg.ChatGPTAccountID) != "",
		ChatGPTAccessTokenSet: strings.TrimSpace(cfg.ChatGPTAccessToken) != "",
		ChatGPTAccountIDSet:   strings.TrimSpace(cfg.ChatGPTAccountID) != "",
		AnthropicModel:        cfg.AnthropicModel,
		AnthropicAPIKeySet:    strings.TrimSpace(cfg.AnthropicAPIKey) != "",
		Timeout:               cfg.Timeout,
		Policies:              cfg.Policies,
	}
}

func decodeLLMSettingsUpdate(r *http.Request, current config.LLMConfig) (config.LLMConfig, error) {
	var update llmSettingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		return config.LLMConfig{}, err
	}
	settings := current
	if update.Enabled != nil {
		settings.Enabled = *update.Enabled
	}
	if update.Provider != nil {
		settings.Provider = strings.TrimSpace(*update.Provider)
	}
	if update.OpenAIAuthMethod != nil {
		settings.OpenAIAuthMethod = strings.TrimSpace(*update.OpenAIAuthMethod)
	}
	if update.OpenAIModel != nil {
		settings.OpenAIModel = strings.TrimSpace(*update.OpenAIModel)
	}
	if update.ClearOpenAIAPIKey {
		settings.OpenAIAPIKey = ""
	} else if update.OpenAIAPIKey != nil {
		if key := strings.TrimSpace(*update.OpenAIAPIKey); key != "" {
			settings.OpenAIAPIKey = key
		}
	}
	if update.ClearChatGPTAuth {
		settings.ChatGPTRefreshToken = ""
		settings.ChatGPTAccessToken = ""
		settings.ChatGPTTokenExpiresAt = time.Time{}
		settings.ChatGPTAccountID = ""
	}
	if update.AnthropicModel != nil {
		settings.AnthropicModel = strings.TrimSpace(*update.AnthropicModel)
	}
	if update.ClearAnthropicAPIKey {
		settings.AnthropicAPIKey = ""
	} else if update.AnthropicAPIKey != nil {
		if key := strings.TrimSpace(*update.AnthropicAPIKey); key != "" {
			settings.AnthropicAPIKey = key
		}
	}
	if update.Timeout != nil {
		settings.Timeout = *update.Timeout
	}
	if update.Policies != nil {
		settings.Policies = update.Policies
	}
	return settings, nil
}

func integrationSettingsResponse(cfg config.IntegrationConfig) integrationSettingsView {
	return integrationSettingsView{
		Mode:              cfg.Mode,
		UnraidBaseURL:     cfg.UnraidBaseURL,
		UnraidAPIKeySet:   strings.TrimSpace(cfg.UnraidAPIKey) != "",
		UnraidAPIKeyFile:  cfg.UnraidAPIKeyFile,
		UnraidSSHFallback: cfg.UnraidSSHFallback,
		UnraidSSHHost:     cfg.UnraidSSHHost,
		UnraidSSHPort:     cfg.UnraidSSHPort,
		UnraidSSHUser:     cfg.UnraidSSHUser,
		UnraidSSHKeyFile:  cfg.UnraidSSHKeyFile,
		UnraidSSHCommand:  cfg.UnraidSSHCommand,
		UniFiBaseURL:      cfg.UniFiBaseURL,
		UniFiAPIKeySet:    strings.TrimSpace(cfg.UniFiAPIKey) != "",
		UniFiAPIKeyFile:   cfg.UniFiAPIKeyFile,
		UniFiSiteID:       cfg.UniFiSiteID,
		UniFiInsecureTLS:  cfg.UniFiInsecureTLS,
		InternetProbeURL:  cfg.InternetProbeURL,
		DNSProbeHost:      cfg.DNSProbeHost,
		RouterProbeTarget: cfg.RouterProbeTarget,
		NASProbeTarget:    cfg.NASProbeTarget,
	}
}

func decodeIntegrationSettingsUpdate(r *http.Request, current config.IntegrationConfig) (config.IntegrationConfig, error) {
	var update integrationSettingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		return config.IntegrationConfig{}, err
	}
	settings := current
	if update.Mode != nil {
		settings.Mode = strings.TrimSpace(*update.Mode)
	}
	if update.UnraidBaseURL != nil {
		settings.UnraidBaseURL = strings.TrimSpace(*update.UnraidBaseURL)
	}
	if update.ClearUnraidAPIKey {
		settings.UnraidAPIKey = ""
	} else if update.UnraidAPIKey != nil {
		if key := strings.TrimSpace(*update.UnraidAPIKey); key != "" {
			settings.UnraidAPIKey = key
		}
	}
	if update.UnraidAPIKeyFile != nil {
		settings.UnraidAPIKeyFile = strings.TrimSpace(*update.UnraidAPIKeyFile)
	}
	if update.UnraidSSHFallback != nil {
		settings.UnraidSSHFallback = *update.UnraidSSHFallback
	}
	if update.UnraidSSHHost != nil {
		settings.UnraidSSHHost = strings.TrimSpace(*update.UnraidSSHHost)
	}
	if update.UnraidSSHPort != nil {
		settings.UnraidSSHPort = *update.UnraidSSHPort
	}
	if update.UnraidSSHUser != nil {
		settings.UnraidSSHUser = strings.TrimSpace(*update.UnraidSSHUser)
	}
	if update.UnraidSSHKeyFile != nil {
		settings.UnraidSSHKeyFile = strings.TrimSpace(*update.UnraidSSHKeyFile)
	}
	if update.UnraidSSHCommand != nil {
		settings.UnraidSSHCommand = strings.TrimSpace(*update.UnraidSSHCommand)
	}
	if update.UniFiBaseURL != nil {
		settings.UniFiBaseURL = strings.TrimSpace(*update.UniFiBaseURL)
	}
	if update.ClearUniFiAPIKey {
		settings.UniFiAPIKey = ""
	} else if update.UniFiAPIKey != nil {
		if key := strings.TrimSpace(*update.UniFiAPIKey); key != "" {
			settings.UniFiAPIKey = key
		}
	}
	if update.UniFiAPIKeyFile != nil {
		settings.UniFiAPIKeyFile = strings.TrimSpace(*update.UniFiAPIKeyFile)
	}
	if update.UniFiSiteID != nil {
		settings.UniFiSiteID = strings.TrimSpace(*update.UniFiSiteID)
	}
	if update.UniFiInsecureTLS != nil {
		settings.UniFiInsecureTLS = *update.UniFiInsecureTLS
	}
	if update.InternetProbeURL != nil {
		settings.InternetProbeURL = strings.TrimRight(strings.TrimSpace(*update.InternetProbeURL), "/")
	}
	if update.DNSProbeHost != nil {
		settings.DNSProbeHost = strings.TrimSpace(*update.DNSProbeHost)
	}
	if update.RouterProbeTarget != nil {
		settings.RouterProbeTarget = strings.TrimRight(strings.TrimSpace(*update.RouterProbeTarget), "/")
	}
	if update.NASProbeTarget != nil {
		settings.NASProbeTarget = strings.TrimRight(strings.TrimSpace(*update.NASProbeTarget), "/")
	}
	return settings, nil
}

func normalizeIntegrationSettings(settings config.IntegrationConfig) (config.IntegrationConfig, error) {
	defaults := config.Defaults().Integrations
	settings.Mode = strings.TrimSpace(settings.Mode)
	if settings.Mode == "" {
		settings.Mode = defaults.Mode
	}
	if settings.UnraidSSHPort == 0 {
		settings.UnraidSSHPort = defaults.UnraidSSHPort
	}
	if strings.TrimSpace(settings.UnraidSSHCommand) == "" {
		settings.UnraidSSHCommand = defaults.UnraidSSHCommand
	}
	if strings.TrimSpace(settings.UniFiSiteID) == "" {
		settings.UniFiSiteID = defaults.UniFiSiteID
	}
	if settings.UnraidBaseURL != "" {
		normalized, err := config.NormalizeIntegrationBaseURL(settings.UnraidBaseURL, "http")
		if err != nil {
			return config.IntegrationConfig{}, fmt.Errorf("unraid_base_url: %w", err)
		}
		settings.UnraidBaseURL = normalized
	}
	if settings.UniFiBaseURL != "" {
		normalized, err := config.NormalizeIntegrationBaseURL(settings.UniFiBaseURL, "https")
		if err != nil {
			return config.IntegrationConfig{}, fmt.Errorf("unifi_base_url: %w", err)
		}
		settings.UniFiBaseURL = normalized
	}
	settings.UnraidAPIKey = strings.TrimSpace(settings.UnraidAPIKey)
	settings.UnraidAPIKeyFile = strings.TrimSpace(settings.UnraidAPIKeyFile)
	settings.UnraidSSHHost = strings.TrimSpace(settings.UnraidSSHHost)
	settings.UnraidSSHUser = strings.TrimSpace(settings.UnraidSSHUser)
	settings.UnraidSSHKeyFile = strings.TrimSpace(settings.UnraidSSHKeyFile)
	settings.UnraidSSHCommand = strings.TrimSpace(settings.UnraidSSHCommand)
	settings.UniFiAPIKey = strings.TrimSpace(settings.UniFiAPIKey)
	settings.UniFiAPIKeyFile = strings.TrimSpace(settings.UniFiAPIKeyFile)
	settings.UniFiSiteID = strings.TrimSpace(settings.UniFiSiteID)
	settings.InternetProbeURL = strings.TrimRight(strings.TrimSpace(settings.InternetProbeURL), "/")
	settings.DNSProbeHost = strings.TrimSpace(settings.DNSProbeHost)
	settings.RouterProbeTarget = strings.TrimRight(strings.TrimSpace(settings.RouterProbeTarget), "/")
	settings.NASProbeTarget = strings.TrimRight(strings.TrimSpace(settings.NASProbeTarget), "/")
	return settings, nil
}

func hydrateIntegrationSecretFiles(settings config.IntegrationConfig) (config.IntegrationConfig, error) {
	if settings.UnraidAPIKeyFile != "" {
		secret, err := config.ReadSecretFile(settings.UnraidAPIKeyFile)
		if err != nil {
			return config.IntegrationConfig{}, fmt.Errorf("unraid api key file: %w", err)
		}
		settings.UnraidAPIKey = secret
	}
	if settings.UniFiAPIKeyFile != "" {
		secret, err := config.ReadSecretFile(settings.UniFiAPIKeyFile)
		if err != nil {
			return config.IntegrationConfig{}, fmt.Errorf("unifi api key file: %w", err)
		}
		settings.UniFiAPIKey = secret
	}
	return settings, nil
}

func integrationSettingsPresent(settings config.IntegrationConfig) bool {
	return settings.Mode != "" ||
		settings.UnraidBaseURL != "" ||
		settings.UnraidAPIKey != "" ||
		settings.UnraidAPIKeyFile != "" ||
		settings.UnraidSSHHost != "" ||
		settings.UniFiBaseURL != "" ||
		settings.UniFiAPIKey != "" ||
		settings.UniFiAPIKeyFile != "" ||
		settings.InternetProbeURL != "" ||
		settings.DNSProbeHost != "" ||
		settings.RouterProbeTarget != "" ||
		settings.NASProbeTarget != ""
}

func (a *App) configSnapshot() config.Config {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.deps.Config
}

func (a *App) runtimeSnapshot() (config.Config, Collectors) {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.deps.Config, a.deps.Collectors
}

func (a *App) redactorSnapshot() *privacy.Redactor {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.deps.Redactor
}

func (a *App) llmRuntimeSnapshot() (config.Config, llm.Client) {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.deps.Config, a.deps.LLM
}

func (a *App) currentRuntimeSettingsLocked() db.RuntimeSettings {
	settings := db.RuntimeSettings{
		Visibility:    a.deps.Config.Visibility,
		Privacy:       a.deps.Config.Privacy,
		AppCatalog:    a.deps.Config.AppCatalog,
		LLM:           a.deps.Config.LLM,
		Notifications: a.deps.Config.Notifications,
	}
	if a.runtimeIntegrationsSet {
		settings.Integrations = runtimeIntegrationSettings(a.deps.Config.Integrations)
	}
	return settings
}

func runtimeIntegrationSettings(settings config.IntegrationConfig) config.IntegrationConfig {
	if settings.UnraidAPIKeyFile != "" {
		settings.UnraidAPIKey = ""
	}
	if settings.UniFiAPIKeyFile != "" {
		settings.UniFiAPIKey = ""
	}
	return settings
}

func (a *App) applyRuntimeSettings(settings db.RuntimeSettings) error {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.deps.Config.Visibility = normalizeVisibilitySettings(settings.Visibility)
	a.deps.Config.Privacy = settings.Privacy
	if settings.AppCatalog.IconOverrides == nil {
		settings.AppCatalog.IconOverrides = map[string]string{}
	}
	a.deps.Config.AppCatalog = settings.AppCatalog
	settings.LLM = normalizeLLMSettings(settings.LLM)
	a.deps.Config.LLM = settings.LLM
	if integrationSettingsPresent(settings.Integrations) {
		integrations, err := normalizeIntegrationSettings(settings.Integrations)
		if err != nil {
			return err
		}
		integrations, err = hydrateIntegrationSecretFiles(integrations)
		if err != nil {
			return err
		}
		a.deps.Config.Integrations = integrations
		a.deps.Collectors = collectorsForConfig(a.deps.Config)
		a.runtimeIntegrationsSet = true
	}
	a.deps.Config.Notifications = settings.Notifications
	a.deps.Redactor = privacy.NewRedactor(settings.Privacy)
	a.deps.LLM = llm.NewClient(settings.LLM, a.deps.Redactor)
	a.deps.Notifications.UpdateConfig(settings.Notifications)
	return nil
}

func collectorsForConfig(cfg config.Config) Collectors {
	collectors := unavailableCollectors(cfg)
	if cfg.Integrations.Mode == "fixture" || cfg.Integrations.Mode == "mixed" {
		collectors = Collectors{
			Unraid: unraid.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			Docker: docker.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			UniFi:  unifi.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			Probes: probes.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
		}
	}
	if cfg.Integrations.Mode == "live" || cfg.Integrations.Mode == "mixed" {
		if cfg.Integrations.UnraidBaseURL != "" && cfg.Integrations.UnraidAPIKey != "" {
			collectors.Unraid = unraid.NewLiveClient(cfg.Integrations.UnraidBaseURL, cfg.Integrations.UnraidAPIKey)
			collectors.Docker = docker.NewUnraidLiveClient(cfg.Integrations.UnraidBaseURL, cfg.Integrations.UnraidAPIKey)
		}
		if cfg.Integrations.UnraidSSHFallback {
			sshDocker := docker.NewSSHClient(docker.SSHOptions{
				Host:    cfg.Integrations.UnraidSSHHost,
				Port:    cfg.Integrations.UnraidSSHPort,
				User:    cfg.Integrations.UnraidSSHUser,
				KeyFile: cfg.Integrations.UnraidSSHKeyFile,
				Command: cfg.Integrations.UnraidSSHCommand,
			})
			if collectors.Docker != nil {
				collectors.Docker = docker.NewLargestListClient(collectors.Docker, sshDocker)
			} else {
				collectors.Docker = sshDocker
			}
		}
		if cfg.Integrations.UniFiBaseURL != "" && cfg.Integrations.UniFiAPIKey != "" {
			collectors.UniFi = unifi.NewLiveClient(cfg.Integrations.UniFiBaseURL, cfg.Integrations.UniFiAPIKey, cfg.Integrations.UniFiSiteID, cfg.Integrations.UniFiInsecureTLS)
		}
	}
	return collectors
}

func unavailableCollectors(cfg config.Config) Collectors {
	return Collectors{
		Unraid: unavailableUnraidClient("unraid live credentials are not configured"),
		Docker: unavailableDockerClient("unraid docker live credentials are not configured"),
		UniFi:  unavailableUniFiClient("unifi live credentials are not configured"),
		Probes: probes.NewLiveClient(probes.LiveConfig{
			InternetURL:  cfg.Integrations.InternetProbeURL,
			DNSHost:      cfg.Integrations.DNSProbeHost,
			RouterTarget: firstNonEmpty(cfg.Integrations.RouterProbeTarget, cfg.Integrations.UniFiBaseURL),
			NASTarget:    firstNonEmpty(cfg.Integrations.NASProbeTarget, cfg.Integrations.UnraidBaseURL),
		}),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type unavailableUnraidClient string

func (c unavailableUnraidClient) Status(context.Context) (models.InfrastructureStatus, []models.LogLine, error) {
	return models.InfrastructureStatus{}, nil, errors.New(string(c))
}

type unavailableDockerClient string

func (c unavailableDockerClient) Apps(context.Context) ([]models.AppStatus, error) {
	return nil, errors.New(string(c))
}

func (c unavailableDockerClient) ControlContainer(context.Context, models.AppStatus, docker.ContainerAction) (docker.ControlResult, error) {
	return docker.ControlResult{}, errors.New(string(c))
}

func (c unavailableDockerClient) Logs(context.Context, models.AppStatus, docker.LogOptions) ([]models.LogLine, error) {
	return nil, errors.New(string(c))
}

type unavailableUniFiClient string

func (c unavailableUniFiClient) Status(context.Context) (models.InfrastructureStatus, error) {
	return models.InfrastructureStatus{}, errors.New(string(c))
}

func (a *App) defaultRole() models.Role {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	if a.deps.Config.Visibility.DefaultRole != "" {
		return a.deps.Config.Visibility.DefaultRole
	}
	return models.RoleGeneralUser
}

func normalizeVisibilitySettings(settings models.VisibilitySettings) models.VisibilitySettings {
	if settings.DefaultRole == "" {
		settings.DefaultRole = models.RoleGeneralUser
	}
	settings.Roles = normalizeRoleVisibility(settings)
	return settings
}

func normalizeLLMSettings(settings config.LLMConfig) config.LLMConfig {
	defaults := config.Defaults().LLM
	settings.Provider = strings.TrimSpace(settings.Provider)
	if settings.Provider == "" || settings.Provider == "mock" {
		settings.Provider = "disabled"
	}
	settings.OpenAIAuthMethod = strings.TrimSpace(settings.OpenAIAuthMethod)
	if settings.OpenAIAuthMethod == "" {
		settings.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	}
	settings.OpenAIAPIKey = strings.TrimSpace(settings.OpenAIAPIKey)
	settings.OpenAIModel = strings.TrimSpace(settings.OpenAIModel)
	if settings.OpenAIModel == "" {
		settings.OpenAIModel = defaults.OpenAIModel
	}
	settings.ChatGPTRefreshToken = strings.TrimSpace(settings.ChatGPTRefreshToken)
	settings.ChatGPTAccessToken = strings.TrimSpace(settings.ChatGPTAccessToken)
	settings.ChatGPTAccountID = strings.TrimSpace(settings.ChatGPTAccountID)
	settings.AnthropicAPIKey = strings.TrimSpace(settings.AnthropicAPIKey)
	settings.AnthropicModel = strings.TrimSpace(settings.AnthropicModel)
	if settings.AnthropicModel == "" {
		settings.AnthropicModel = defaults.AnthropicModel
	}
	if settings.Timeout == 0 {
		settings.Timeout = defaults.Timeout
	}
	settings.Policies = normalizeLLMPolicies(settings.Policies, defaults.Policies)
	return settings
}

func normalizeLLMPolicies(policies, defaults map[string]models.LLMPolicy) map[string]models.LLMPolicy {
	if policies == nil {
		policies = map[string]models.LLMPolicy{}
	}
	out := make(map[string]models.LLMPolicy, len(policies)+len(defaults))
	for name, fallback := range defaults {
		policy, ok := policies[name]
		if !ok {
			out[name] = fallback
			continue
		}
		out[name] = normalizeLLMPolicy(policy, fallback)
	}
	for name, policy := range policies {
		if _, ok := out[name]; ok {
			continue
		}
		out[name] = normalizeLLMPolicy(policy, models.LLMPolicy{AgentMaxToolCalls: 3})
	}
	return out
}

func normalizeLLMPolicy(policy, fallback models.LLMPolicy) models.LLMPolicy {
	if policy.Name == "" {
		policy.Name = fallback.Name
	}
	if policy.MaxContextBytes <= 0 {
		policy.MaxContextBytes = fallback.MaxContextBytes
	}
	if policy.AgentMaxToolCalls <= 0 {
		policy.AgentMaxToolCalls = fallback.AgentMaxToolCalls
	}
	if len(policy.AgentToolRules) == 0 && len(fallback.AgentToolRules) > 0 {
		policy.AgentToolRules = append([]models.LLMAgentToolRule(nil), fallback.AgentToolRules...)
	}
	for i := range policy.AgentToolRules {
		policy.AgentToolRules[i].Tool = strings.TrimSpace(policy.AgentToolRules[i].Tool)
		policy.AgentToolRules[i].Action = strings.TrimSpace(policy.AgentToolRules[i].Action)
	}
	if policy.AgentToolsEnabled && policy.RecipientRole != models.RoleAdmin {
		policy.AgentToolsEnabled = false
	}
	return policy
}

func chatGPTAuthPresent(settings config.LLMConfig) bool {
	return strings.TrimSpace(settings.ChatGPTRefreshToken) != "" ||
		strings.TrimSpace(settings.ChatGPTAccessToken) != "" ||
		strings.TrimSpace(settings.ChatGPTAccountID) != ""
}

func normalizeRoleVisibility(settings models.VisibilitySettings) []models.RoleVisibility {
	roles := make([]models.RoleVisibility, 0, len(settings.Roles)+1)
	seen := map[models.Role]bool{}
	for _, role := range settings.Roles {
		role.Role = models.Role(strings.TrimSpace(string(role.Role)))
		if role.Role == "" || role.Role == models.RoleAdmin || seen[role.Role] {
			continue
		}
		if strings.TrimSpace(role.DisplayName) == "" {
			role.DisplayName = strings.ReplaceAll(string(role.Role), "_", " ")
		}
		role.HiddenAppIDs = compactStrings(role.HiddenAppIDs)
		role.HiddenContainerNames = compactStrings(role.HiddenContainerNames)
		roles = append(roles, role)
		seen[role.Role] = true
	}
	if !seen[models.RoleGeneralUser] {
		roles = append([]models.RoleVisibility{{
			Role:                   models.RoleGeneralUser,
			DisplayName:            "General User",
			CanUseLLM:              settings.GeneralUserCanUseLLM,
			ShowNASStatusToUsers:   settings.ShowNASStatusToUsers,
			ShowWANStatusToUsers:   settings.ShowWANStatusToUsers,
			ShowIncidentIDsToUsers: settings.ShowIncidentIDsToUsers,
			HiddenAppIDs:           compactStrings(settings.HiddenAppIDs),
			HiddenContainerNames:   compactStrings(settings.HiddenContainerNames),
		}}, roles...)
	}
	return roles
}

func roleCanUseLLM(settings models.VisibilitySettings, role models.Role) bool {
	if role == models.RoleAdmin {
		return true
	}
	for _, item := range normalizeRoleVisibility(settings) {
		if item.Role == role {
			return item.CanUseLLM
		}
	}
	return settings.GeneralUserCanUseLLM
}

func compactDiagnosisRole(actorRole, defaultRole models.Role) models.Role {
	if actorRole == "" {
		return models.RoleGeneralUser
	}
	if actorRole != models.RoleAdmin {
		return actorRole
	}
	if defaultRole == "" || defaultRole == models.RoleAdmin {
		return models.RoleGeneralUser
	}
	return defaultRole
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		out = append(out, value)
		seen[key] = true
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			if r.ContentLength > maxRequestBodyBytes {
				writeError(w, http.StatusRequestEntityTooLarge, errors.New("request body is too large"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler, cfg config.ServerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; connect-src 'self'; frame-ancestors 'none'; form-action 'self'; img-src 'self' http: https: data:; manifest-src 'self'; style-src 'self'; script-src 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.PublicURL)), "https://") {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if !originAllowed(r, cfg) {
			writeError(w, http.StatusForbidden, errors.New("request origin is not allowed"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(r *http.Request, cfg config.ServerConfig) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := normalizedOrigin(r.Header.Get("Origin"))
	if origin == "" {
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			return false
		}
		referer, valid := refererOrigin(r.Header.Get("Referer"))
		if !valid {
			return false
		}
		return referer == "" || originMatches(referer, r, cfg)
	}
	return originMatches(origin, r, cfg)
}

func originMatches(origin string, r *http.Request, cfg config.ServerConfig) bool {
	if normalizedOrigin(requestOrigin(r)) == origin {
		return true
	}
	if normalizedOrigin(cfg.PublicURL) == origin {
		return true
	}
	for _, allowed := range cfg.AllowedOrigins {
		if normalizedOrigin(allowed) == origin {
			return true
		}
	}
	return false
}

func refererOrigin(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return normalizedOrigin(parsed.Scheme + "://" + parsed.Host), true
}

func normalizedOrigin(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return value
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
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
			writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, session.User)
		ctx = context.WithValue(ctx, sessionContextKey, session)
		next(w, r.WithContext(ctx))
	}
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

func mustUser(r *http.Request) users.User {
	user, _ := r.Context().Value(userContextKey).(users.User)
	return user
}

func mustSession(r *http.Request) session {
	session, _ := r.Context().Value(sessionContextKey).(session)
	return session
}

type session struct {
	Token     string
	CSRFToken string
	User      users.User
	ExpiresAt time.Time
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
	now := time.Now().UTC()
	token, err := randomToken()
	if err != nil {
		return session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return session{}, err
	}
	entry := session{Token: token, CSRFToken: csrf, User: user, ExpiresAt: now.Add(s.ttl)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(now)
	s.enforceLimitLocked(maxSessionEntries - 1)
	s.entries[token] = entry
	return entry, nil
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
