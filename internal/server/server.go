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
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	statushistory "github.com/wellivea1/noobboard/internal/history"
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
	History       db.HistoryStore
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
	snapshotMu             sync.RWMutex
	cachedSnapshot         models.Snapshot
	cachedSnapshotSet      bool
	historyMu              sync.Mutex
	historyRecorder        *statushistory.Recorder
	lastHistoryPrune       time.Time
	runtimeIntegrationsSet bool
	openAIAuth             *openAIAuthStore
	agentApprovalSecret    []byte
	agentApprovalMu        sync.Mutex
	consumedAgentApprovals map[string]time.Time
	agentRepairMu          sync.Mutex
	agentRepairLastByApp   map[string]time.Time
	agentRepairGlobal      []time.Time
}

const maxRequestBodyBytes int64 = 1 << 20

type siteMode string

const (
	siteModeAdmin   siteMode = "admin"
	siteModeCompact siteMode = "compact"

	maxLoginFailures            = 5
	loginFailureWindow          = 5 * time.Minute
	loginLockoutTimeout         = 10 * time.Minute
	maxSessionEntries           = 512
	maxPersistentSessionEntries = 2048
	maxLoginFailureKeys         = 2048
	defaultLogLimit             = 80
	maxLogLimit                 = 200
	// Events handed to the agent history tool. Enough to show a repeating
	// pattern, small enough that it does not dominate the request.
	agentHistoryEventLimit = 60
	agentApprovalPlanID         = "current_recommendation"

	agentRepairPerAppCooldown      = time.Minute
	agentRepairGlobalWindow        = time.Hour
	agentRepairGlobalLimit         = 5
	actionReviewReferenceLimit     = 6
	actionReviewReferenceBytes     = 18 * 1024
	actionReviewReferenceFileBytes = 8 * 1024

	arrayStartActionID = "ask_admin_to_start_array"
	arrayTargetID      = "unraid_array"
)

var agentRepairVerificationDelay = 5 * time.Second
var agentRepairVerificationAttempts = 6

func New(deps Dependencies) (*App, error) {
	if deps.Store == nil || deps.Users == nil || deps.Redactor == nil || deps.LLM == nil {
		return nil, errors.New("server dependencies are incomplete")
	}
	deps.Config.Visibility = normalizeVisibilitySettings(deps.Config.Visibility)
	approvalSecret := make([]byte, 32)
	if _, err := rand.Read(approvalSecret); err != nil {
		return nil, err
	}
	app := &App{
		deps:                   deps,
		sessions:               newSessionStore(deps.Config.Auth.SessionTimeout),
		loginLimiter:           newLoginLimiter(),
		openAIAuth:             newOpenAIAuthStore(),
		historyRecorder:        statushistory.NewRecorder(),
		agentApprovalSecret:    approvalSecret,
		consumedAgentApprovals: map[string]time.Time{},
		agentRepairLastByApp:   map[string]time.Time{},
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
	mux.HandleFunc("POST /api/admin/agent/approval", a.requireAdmin(a.recordAgentApproval))
	mux.HandleFunc("POST /api/admin/agent/arm", a.requireAdmin(a.setAgentArm))
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
	mux.HandleFunc("GET /api/admin/repair-requests", a.requireAdmin(a.adminRepairRequests))
	mux.HandleFunc("POST /api/admin/repair-requests/{id}/decision", a.requireAdmin(a.decideRepairRequest))
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
	mux.HandleFunc("POST /api/status/refresh", a.requireAuth(a.refreshStatus))
	mux.HandleFunc("GET /api/apps", a.requireAuth(a.apps))
	mux.HandleFunc("GET /api/apps/{id}/history", a.requireAuth(a.appHistory))
	mux.HandleFunc("GET /api/apps/", a.requireAuth(a.appByID))
	mux.HandleFunc("GET /api/infrastructure/history", a.requireAuth(a.infrastructureHistory))
	mux.HandleFunc("POST /api/user/diagnose", a.requireAuth(a.userDiagnose))
	mux.HandleFunc("POST /api/user/notify-admin", a.requireAuth(a.notifyAdmin))
	mux.HandleFunc("GET /api/user/repair-requests", a.requireAuth(a.userRepairRequests))
	mux.HandleFunc("POST /api/user/repair-request", a.requireAuth(a.createRepairRequest))
	mux.HandleFunc("POST /api/user/agent/action", a.requireAuth(a.executeUserAgentAction))
	mux.HandleFunc("POST /api/user/apps/{id}/restart", a.requireAuth(a.restartUserApp))
	mux.HandleFunc("POST /api/user/apps/{id}/action", a.requireAuth(a.controlUserApp))
	mux.HandleFunc("GET /api/user/notifications", a.requireAuth(a.userNotifications))
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

func (a *App) RunPoller(ctx context.Context, interval time.Duration) {
	if interval < time.Second {
		interval = time.Second
	}
	_, _ = a.refreshSnapshot(ctx, true)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = a.refreshSnapshot(ctx, true)
		}
	}
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

func (a *App) latestSnapshot(ctx context.Context, role models.Role) (models.Snapshot, error) {
	full, err := a.latestFullSnapshot(ctx)
	if err != nil {
		return models.Snapshot{}, err
	}
	if role == models.RoleAdmin {
		return full, nil
	}
	return privacy.FilterSnapshotForRole(full, role, a.redactorSnapshot()), nil
}

func (a *App) latestFullSnapshot(ctx context.Context) (models.Snapshot, error) {
	a.snapshotMu.RLock()
	if a.cachedSnapshotSet {
		snapshot := cloneSnapshot(a.cachedSnapshot)
		a.snapshotMu.RUnlock()
		return snapshot, nil
	}
	a.snapshotMu.RUnlock()
	return a.refreshSnapshot(ctx, false)
}

func (a *App) refreshSnapshot(ctx context.Context, processNotifications bool) (models.Snapshot, error) {
	snapshot, err := a.collectSnapshot(ctx, processNotifications)
	if err != nil {
		return models.Snapshot{}, err
	}
	if processNotifications {
		_ = a.recordSnapshotHistory(snapshot)
	}
	a.snapshotMu.Lock()
	a.cachedSnapshot = cloneSnapshot(snapshot)
	a.cachedSnapshotSet = true
	a.snapshotMu.Unlock()
	return cloneSnapshot(snapshot), nil
}

func (a *App) recordSnapshotHistory(snapshot models.Snapshot) error {
	if a.deps.History == nil || a.historyRecorder == nil {
		return nil
	}
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	events := a.historyRecorder.Record(snapshot)
	if len(events) > 0 {
		if err := a.deps.History.Append(events); err != nil {
			return err
		}
	}
	if a.lastHistoryPrune.IsZero() || time.Since(a.lastHistoryPrune) >= time.Hour {
		if err := a.deps.History.Prune(a.configSnapshot().Retention); err != nil {
			return err
		}
		a.lastHistoryPrune = time.Now().UTC()
	}
	return nil
}

func (a *App) invalidateSnapshot() {
	a.snapshotMu.Lock()
	a.cachedSnapshot = models.Snapshot{}
	a.cachedSnapshotSet = false
	a.snapshotMu.Unlock()
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
		RepairAutomation:     repairAutomationInfo(cfg),
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

func cloneSnapshot(snapshot models.Snapshot) models.Snapshot {
	snapshot.Infrastructure.StorageWarnings = append([]string(nil), snapshot.Infrastructure.StorageWarnings...)
	snapshot.Infrastructure.UniFiWarnings = append([]string(nil), snapshot.Infrastructure.UniFiWarnings...)
	snapshot.Infrastructure.UnraidVMNames = append([]string(nil), snapshot.Infrastructure.UnraidVMNames...)
	snapshot.Infrastructure.UnraidShareNames = append([]string(nil), snapshot.Infrastructure.UnraidShareNames...)
	snapshot.Infrastructure.DockerNetworkNames = append([]string(nil), snapshot.Infrastructure.DockerNetworkNames...)
	snapshot.Apps = cloneApps(snapshot.Apps)
	snapshot.Incidents = cloneIncidents(snapshot.Incidents)
	snapshot.Facts = cloneFacts(snapshot.Facts)
	snapshot.Visibility = cloneVisibility(snapshot.Visibility)
	snapshot.LLMPolicies = cloneLLMPolicies(snapshot.LLMPolicies)
	snapshot.AuditTail = append([]models.AuditEntry(nil), snapshot.AuditTail...)
	return snapshot
}

func cloneApps(apps []models.AppStatus) []models.AppStatus {
	out := make([]models.AppStatus, len(apps))
	for i, app := range apps {
		out[i] = app
		out[i].AllowedLogSources = append([]string(nil), app.AllowedLogSources...)
		out[i].RecentLogs = append([]models.LogLine(nil), app.RecentLogs...)
		out[i].LastIncidentIDs = append([]string(nil), app.LastIncidentIDs...)
	}
	return out
}

func cloneIncidents(incidents []models.Incident) []models.Incident {
	out := make([]models.Incident, len(incidents))
	for i, incident := range incidents {
		out[i] = incident
		out[i].AffectedServices = append([]string(nil), incident.AffectedServices...)
		out[i].Evidence = append([]string(nil), incident.Evidence...)
	}
	return out
}

func cloneFacts(facts []models.IncidentFact) []models.IncidentFact {
	out := make([]models.IncidentFact, len(facts))
	for i, fact := range facts {
		out[i] = fact
		out[i].Evidence = append([]string(nil), fact.Evidence...)
		out[i].AffectedServices = append([]string(nil), fact.AffectedServices...)
	}
	return out
}

func cloneVisibility(visibility models.VisibilitySettings) models.VisibilitySettings {
	visibility.HiddenAppIDs = append([]string(nil), visibility.HiddenAppIDs...)
	visibility.HiddenContainerNames = append([]string(nil), visibility.HiddenContainerNames...)
	visibility.Roles = append([]models.RoleVisibility(nil), visibility.Roles...)
	for i := range visibility.Roles {
		visibility.Roles[i].HiddenAppIDs = append([]string(nil), visibility.Roles[i].HiddenAppIDs...)
		visibility.Roles[i].HiddenContainerNames = append([]string(nil), visibility.Roles[i].HiddenContainerNames...)
	}
	return visibility
}

func cloneLLMPolicies(policies map[string]models.LLMPolicy) map[string]models.LLMPolicy {
	if policies == nil {
		return nil
	}
	out := make(map[string]models.LLMPolicy, len(policies))
	for key, policy := range policies {
		policy.AllowedLogSources = append([]string(nil), policy.AllowedLogSources...)
		policy.AgentToolRules = append([]models.LLMAgentToolRule(nil), policy.AgentToolRules...)
		out[key] = policy
	}
	return out
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
		apps[i].AgentRepairAllowed = appCatalogFlag(apps[i], catalog.AgentRepairAllowed)
		apps[i].RestartAllowedGeneralUser = catalog.GeneralUserRestartsEnabled && appCatalogFlag(apps[i], catalog.RestartAllowedGeneralUser)
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

func repairAutomationInfo(cfg config.Config) models.RepairAutomationInfo {
	adminAvailable := cfg.LLM.AgentControlEnabled && cfg.LLM.ActionAutoReviewEnabled
	userAvailable := cfg.AppCatalog.GeneralUserRestartsEnabled && cfg.AppCatalog.GeneralUserAutoRepairEnabled
	reason := ""
	switch {
	case adminAvailable && userAvailable:
		reason = "Auto-fix is available where the target app is opted in."
	case !adminAvailable && !userAvailable:
		reason = "Auto-fix is disabled until the admin enables the relevant app-fix settings."
	case !adminAvailable:
		reason = "Admin chat auto-fix needs admin app fixes and the safety reviewer enabled."
	case !userAvailable:
		reason = "Compact chat auto-fix needs standard-user app controls and auto-fix enabled."
	default:
		reason = "Auto-fix is available only on enabled surfaces and opted-in apps."
	}
	return models.RepairAutomationInfo{
		AdminAutoRepairAvailable: adminAvailable,
		UserAutoRepairAvailable:  userAvailable,
		Reason:                   reason,
	}
}

func appCatalogFlag(app models.AppStatus, flags map[string]bool) bool {
	for _, key := range []string{app.AppID, app.ContainerName, app.DisplayName} {
		if key == "" {
			continue
		}
		if flags[key] {
			return true
		}
		if flags[strings.ToLower(key)] {
			return true
		}
	}
	return false
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

func (a *App) statusSummary(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.latestSnapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) refreshStatus(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	full, err := a.refreshSnapshot(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if role := mustUser(r).Role; role != models.RoleAdmin {
		writeJSON(w, http.StatusOK, privacy.FilterSnapshotForRole(full, role, a.redactorSnapshot()))
		return
	}
	writeJSON(w, http.StatusOK, full)
}

func (a *App) apps(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.latestSnapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot.Apps)
}

func (a *App) appByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	app, ok, err := a.visibleAppByID(r.Context(), mustUser(r).Role, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if ok {
		writeJSON(w, http.StatusOK, app)
		return
	}
	writeError(w, http.StatusNotFound, db.ErrNotFound)
}

func (a *App) appHistory(w http.ResponseWriter, r *http.Request) {
	if a.deps.History == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("status history is not configured"))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	role := mustUser(r).Role
	app, ok, err := a.visibleAppByID(r.Context(), role, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	window := parseHistoryWindow(r.URL.Query().Get("window"))
	limit := parseHistoryLimit(r.URL.Query().Get("limit"))
	history, err := a.statusHistory(models.SubjectApp, app.AppID, app.DisplayName, app.CurrentStatus, app.LastSeenOnline, app.LastSeenOffline, window, limit, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (a *App) infrastructureHistory(w http.ResponseWriter, r *http.Request) {
	if a.deps.History == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("status history is not configured"))
		return
	}
	subject := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("subject")))
	if subject == "" {
		writeError(w, http.StatusBadRequest, errors.New("history subject is required"))
		return
	}
	role := mustUser(r).Role
	snapshot, err := a.latestSnapshot(r.Context(), role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	current, displayName, ok := visibleInfraHistorySubject(subject, snapshot, role)
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	window := parseHistoryWindow(r.URL.Query().Get("window"))
	limit := parseHistoryLimit(r.URL.Query().Get("limit"))
	history, err := a.statusHistory(models.SubjectInfra, subject, displayName, current, nil, nil, window, limit, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// agentAppLogs backs the noobboard_app_logs tool.
//
// Three things have to hold before a line reaches a provider, and each is
// checked here rather than trusted from the caller:
//   - the app must be visible to the requesting role (visibleAppByID),
//   - the redactor must exist — no redactor means no logs, not raw logs,
//   - the read is audited like the equivalent admin endpoint.
//
// Returns nil when logs cannot be served, which makes the tool unavailable
// rather than silently empty.
func (a *App) agentAppLogs(role models.Role) func(context.Context, string, int) ([]models.LogLine, error) {
	if a.deps.Redactor == nil || a.deps.Collectors.Docker == nil {
		return nil
	}
	return func(ctx context.Context, appID string, limit int) ([]models.LogLine, error) {
		app, ok, err := a.visibleAppByID(ctx, role, appID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, db.ErrNotFound
		}
		lines, err := a.deps.Collectors.Docker.Logs(ctx, app, docker.LogOptions{Limit: limit})
		if err != nil {
			return nil, err
		}
		redacted, changed := a.deps.Redactor.RedactLogs(lines)
		a.deps.Audit.Record("llm", "app.container.logs", map[string]interface{}{
			"app_id":         app.AppID,
			"container_name": app.ContainerName,
			"line_count":     len(redacted),
			"redacted":       changed,
			"via":            "agent_tool",
		})
		return redacted, nil
	}
}

// agentAppHistory backs the noobboard_app_history tool. Same visibility rule as
// the logs tool; history carries no free text from the container, so it needs
// no redaction beyond what statusHistory already applies for the role.
func (a *App) agentAppHistory(role models.Role) func(context.Context, string) (models.StatusHistory, error) {
	if a.deps.History == nil {
		return nil
	}
	return func(ctx context.Context, appID string) (models.StatusHistory, error) {
		app, ok, err := a.visibleAppByID(ctx, role, appID)
		if err != nil {
			return models.StatusHistory{}, err
		}
		if !ok {
			return models.StatusHistory{}, db.ErrNotFound
		}
		// A 24h window is what answers "is this a loop or a one-off"; a wider one
		// mostly adds noise the model has to pay for in tokens.
		return a.statusHistory(models.SubjectApp, app.AppID, app.DisplayName, app.CurrentStatus, app.LastSeenOnline, app.LastSeenOffline, 24*time.Hour, agentHistoryEventLimit, role)
	}
}

func (a *App) statusHistory(subjectType models.StatusSubjectType, subjectID, displayName string, current models.CurrentStatus, lastOnline, lastOffline *time.Time, window time.Duration, limit int, role models.Role) (models.StatusHistory, error) {
	now := time.Now().UTC()
	since := now.Add(-window)
	allEvents, err := a.deps.History.Query(db.HistoryFilter{SubjectType: subjectType, SubjectID: subjectID})
	if err != nil {
		return models.StatusHistory{}, err
	}
	responseEvents := make([]models.StatusEvent, 0, len(allEvents))
	for _, event := range allEvents {
		if event.At.Before(since) {
			continue
		}
		event.Note = a.deps.Redactor.RedactString(event.Note).Text
		if role != models.RoleAdmin && subjectType == models.SubjectInfra {
			event = plainGeneralInfraEvent(event)
		}
		responseEvents = append(responseEvents, event)
		if limit > 0 && len(responseEvents) >= limit {
			break
		}
	}
	uptime24h := uptimePct(allEvents, current, 24*time.Hour, now)
	uptime7d := uptimePct(allEvents, current, 7*24*time.Hour, now)
	return models.StatusHistory{
		SubjectType:     subjectType,
		SubjectID:       subjectID,
		DisplayName:     displayName,
		Current:         current,
		LastSeenOnline:  lastOnline,
		LastSeenOffline: lastOffline,
		UptimePct24h:    uptime24h,
		UptimePct7d:     uptime7d,
		Events:          responseEvents,
	}, nil
}

func (a *App) adminStatus(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.latestSnapshot(r.Context(), models.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) adminIncidents(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.latestSnapshot(r.Context(), models.RoleAdmin)
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
	a.invalidateSnapshot()
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
		Action       string `json:"action"`
		Confirmed    bool   `json:"confirmed"`
		ConfirmAppID string `json:"confirm_app_id"`
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
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, id)
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	if dockerActionRequiresConfirmation(action) && (!body.Confirmed || !sameAppIdentifier(body.ConfirmAppID, app.AppID)) {
		writeError(w, http.StatusBadRequest, errors.New("stop and restart require confirmed=true with a matching confirm_app_id"))
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

func dockerActionRequiresConfirmation(action docker.ContainerAction) bool {
	return action == docker.ActionStop || action == docker.ActionRestart
}

func sameAppIdentifier(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right)) && strings.TrimSpace(right) != ""
}

func (a *App) appLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/apps/"), "/logs")
	id = strings.Trim(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	limit := parseLogLimit(r.URL.Query().Get("limit"))
	snapshot, err := a.readOnlySnapshot(r.Context())
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

func parseHistoryLimit(value string) int {
	limit := 100
	if strings.TrimSpace(value) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func parseNotificationLimit(value string) int {
	limit := 20
	if strings.TrimSpace(value) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func parseHistoryWindow(value string) time.Duration {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return 7 * 24 * time.Hour
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(value, "d")))
		if err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	return 7 * 24 * time.Hour
}

func findAppByID(apps []models.AppStatus, id string) (models.AppStatus, bool) {
	id = normalizeAppIdentifier(id)
	if id == "" {
		return models.AppStatus{}, false
	}
	for _, app := range apps {
		for _, candidate := range appIdentityCandidates(app) {
			if normalizeAppIdentifier(candidate) == id {
				return app, true
			}
		}
	}
	return models.AppStatus{}, false
}

func (a *App) visibleAppByID(ctx context.Context, role models.Role, id string) (models.AppStatus, bool, error) {
	visibleSnapshot, err := a.latestSnapshot(ctx, role)
	if err != nil {
		return models.AppStatus{}, false, err
	}
	if app, ok := findAppByID(visibleSnapshot.Apps, id); ok {
		return app, true, nil
	}
	if role == models.RoleAdmin {
		return models.AppStatus{}, false, nil
	}
	fullSnapshot, err := a.latestFullSnapshot(ctx)
	if err != nil {
		return models.AppStatus{}, false, err
	}
	fullApp, ok := findAppByID(fullSnapshot.Apps, id)
	if !ok {
		return models.AppStatus{}, false, nil
	}
	app, ok := findAppBySameIdentity(visibleSnapshot.Apps, fullApp)
	return app, ok, nil
}

func findAppBySameIdentity(apps []models.AppStatus, target models.AppStatus) (models.AppStatus, bool) {
	for _, candidate := range appIdentityCandidates(target) {
		if app, ok := findAppByID(apps, candidate); ok {
			return app, true
		}
	}
	return models.AppStatus{}, false
}

func appIdentityCandidates(app models.AppStatus) []string {
	candidates := []string{
		app.AppID,
		app.ContainerID,
		app.ContainerName,
		app.DisplayName,
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		normalized := normalizeAppIdentifier(candidate)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func normalizeAppIdentifier(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "/")
	return strings.ToLower(value)
}

func visibleInfraHistorySubject(subject string, snapshot models.Snapshot, role models.Role) (models.CurrentStatus, string, bool) {
	infra := snapshot.Infrastructure
	if role != models.RoleAdmin {
		switch subject {
		case "internet":
			return boolHistoryStatus(infra.InternetReachable), "Internet", true
		case "nas":
			if !snapshot.Visibility.ShowNASStatusToUsers {
				return "", "", false
			}
			return boolHistoryStatus(infra.NASReachable), "Server", true
		default:
			return "", "", false
		}
	}
	switch subject {
	case "internet":
		return boolHistoryStatus(infra.InternetReachable), "Internet", true
	case "dns":
		return boolHistoryStatus(infra.DNSOK), "DNS", true
	case "wan":
		if role != models.RoleAdmin && !snapshot.Visibility.ShowWANStatusToUsers {
			return "", "", false
		}
		return boolHistoryStatus(infra.UniFiWANUp), "WAN", true
	case "nas":
		if role != models.RoleAdmin && !snapshot.Visibility.ShowNASStatusToUsers {
			return "", "", false
		}
		return boolHistoryStatus(infra.NASReachable), "NAS", true
	case "unraid_array":
		if role != models.RoleAdmin && !snapshot.Visibility.ShowNASStatusToUsers {
			return "", "", false
		}
		return arrayHistoryStatus(infra), "Unraid array", true
	default:
		return "", "", false
	}
}

func plainGeneralInfraEvent(event models.StatusEvent) models.StatusEvent {
	event.DisplayName = plainGeneralInfraDisplayName(event.SubjectID)
	event.Note = plainGeneralInfraNote(event.SubjectID, event.To)
	return event
}

func plainGeneralInfraDisplayName(subjectID string) string {
	switch strings.ToLower(strings.TrimSpace(subjectID)) {
	case "nas", "unraid_array":
		return "Server"
	default:
		return "Internet"
	}
}

func plainGeneralInfraNote(subjectID string, status models.CurrentStatus) string {
	name := plainGeneralInfraDisplayName(subjectID)
	switch status {
	case models.StatusOnline:
		return name + " is working."
	case models.StatusDegraded:
		return name + " has a problem."
	case models.StatusOffline:
		if name == "Server" {
			return "Server is not responding."
		}
		return "Internet is not working."
	default:
		return name + " status changed."
	}
}

func boolHistoryStatus(ok bool) models.CurrentStatus {
	if ok {
		return models.StatusOnline
	}
	return models.StatusOffline
}

func arrayHistoryStatus(infra models.InfrastructureStatus) models.CurrentStatus {
	if !infra.UnraidAPIReachable || strings.TrimSpace(infra.UnraidArrayState) == "" {
		return models.StatusUnknown
	}
	if infra.UnraidArrayHealthy {
		return models.StatusOnline
	}
	if strings.EqualFold(strings.TrimSpace(infra.UnraidArrayState), "started") {
		return models.StatusDegraded
	}
	return models.StatusOffline
}

func uptimePct(events []models.StatusEvent, current models.CurrentStatus, window time.Duration, now time.Time) *float64 {
	if window <= 0 {
		return nil
	}
	start := now.Add(-window)
	cursor := now
	status := current
	online := time.Duration(0)
	for _, event := range events {
		if event.At.After(now) {
			continue
		}
		if !event.At.After(start) {
			break
		}
		if status == models.StatusOnline {
			online += cursor.Sub(event.At)
		}
		status = event.From
		cursor = event.At
	}
	if cursor.After(start) && status == models.StatusOnline {
		online += cursor.Sub(start)
	}
	pct := float64(online) / float64(window) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct
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
		Question   string `json:"question"`
		AutoRepair bool   `json:"auto_repair"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	body.Question = strings.TrimSpace(body.Question)
	if len(body.Question) > 1000 {
		body.Question = body.Question[:1000]
	}
	full, err := a.readOnlySnapshot(r.Context())
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
		AppLogs:      a.agentAppLogs(role),
		AppHistory:   a.agentAppHistory(role),
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
		if llm.IsOpenAIUsageLimitError(err) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "OpenAI usage limit reached. Wait for the limit to reset or check the OpenAI plan in LLM settings.",
				"code":  llm.OpenAIUsageLimitCode,
			})
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "llm.diagnosis", map[string]interface{}{"mode": string(mode), "incident_type": string(diagnosis.IncidentType), "admin_message": diagnosis.AdminMessage})
	response := diagnosisResponse{Diagnosis: diagnosis}
	if mode == llm.ModeAdminRequested && role == models.RoleAdmin {
		planDiagnosis, arraySuggested := arrayStartBackstopDiagnosis(diagnosis, full)
		if arraySuggested {
			response.Diagnosis = planDiagnosis
		}
		planDiagnosis, suggested := adminRestartBackstopDiagnosis(planDiagnosis, full)
		response.AgentPlan = a.llmAgentPlanResponse(planDiagnosis, full, mustUser(r).ID)
		if arraySuggested {
			markSuggestedArrayStartPlan(response.AgentPlan)
			a.auditSuggestedArrayStartPlan(mustUser(r).ID, string(mode), response.AgentPlan)
		}
		if suggested {
			markSuggestedRestartPlan(response.AgentPlan, "NoobBoard found one repair-eligible app that is down, so this restart affordance is suggested even though the model did not request it. Existing approval and safety gates still apply.")
			a.auditSuggestedRestartPlan(mustUser(r).ID, string(mode), response.AgentPlan)
		}
		if body.AutoRepair {
			a.maybeExecuteAgentAutoRepair(r.Context(), mustUser(r), full, response.AgentPlan)
		}
	} else if mode == llm.ModeGeneralUserRequested {
		filtered := privacy.FilterSnapshotForRole(full, role, a.redactorSnapshot())
		planDiagnosis, arraySuggested := arrayStartBackstopDiagnosis(diagnosis, filtered)
		if arraySuggested {
			response.Diagnosis = planDiagnosis
		}
		planDiagnosis, suggested := generalUserRestartBackstopDiagnosis(planDiagnosis, filtered)
		response.AgentPlan = a.llmUserRepairPlanResponse(planDiagnosis, filtered, mustUser(r).ID)
		if arraySuggested {
			markSuggestedArrayStartPlan(response.AgentPlan)
			a.auditSuggestedArrayStartPlan(mustUser(r).ID, string(mode), response.AgentPlan)
		}
		if suggested {
			markSuggestedRestartPlan(response.AgentPlan, "NoobBoard found one visible app that is not working, so this fix option is suggested even though the model did not request it.")
			a.auditSuggestedRestartPlan(mustUser(r).ID, string(mode), response.AgentPlan)
		}
		if body.AutoRepair {
			a.maybeExecuteGeneralUserAutoRepair(r.Context(), mustUser(r), response.AgentPlan)
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) llmAgentPlanResponse(diagnosis llm.Diagnosis, snapshot models.Snapshot, actorID string) *llmAgentPlanView {
	action, known := agentActionDefinition(diagnosis.RecommendedActionID)
	target := resolveAgentPlanTarget(action, diagnosis, snapshot)
	requiresApproval := known && action.ApprovalEligible && (!action.RequiresAppTarget || target.Resolved)
	a.settingsMu.RLock()
	llmCfg := a.deps.Config.LLM
	redactor := a.deps.Redactor
	a.settingsMu.RUnlock()
	status, canExecute, allowReason := a.agentPlanExecutionState(action, target, snapshot, llmCfg, redactor)
	planAction := action
	var limit agentRepairLimitDecision
	if action.Executable && target.Resolved {
		if app, ok := findAppByID(snapshot.Apps, target.ID); ok {
			planAction = agentRepairActionForApp(action, app)
			limit = a.agentRepairLimitState(app.AppID, time.Now().UTC(), false)
		}
	}
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	approvalToken := ""
	if requiresApproval {
		nonce, err := randomToken()
		if err != nil {
			nonce = ""
		}
		approvalToken = a.signAgentApprovalToken(agentApprovalTokenPayload{
			PlanID:              agentApprovalPlanID,
			ActorID:             actorID,
			RecommendedActionID: action.ID,
			TargetKind:          target.Kind,
			TargetID:            target.ID,
			Nonce:               nonce,
			ExpiresAt:           expiresAt.Unix(),
		})
	}
	allowLabel := "Allow fix"
	allowDescription := "Permit this single fix attempt."
	if strings.TrimSpace(string(planAction.DockerAction)) != "" {
		actionText := strings.ToLower(dockerActionDisplayName(planAction.DockerAction))
		allowLabel = "Allow " + actionText
		allowDescription = "Permit this single " + actionText + " attempt."
	}
	response := &llmAgentPlanView{
		ID:                    agentApprovalPlanID,
		Title:                 planAction.Title,
		Summary:               planAction.Summary,
		RecommendedActionID:   action.ID,
		DirectAction:          string(planAction.DockerAction),
		ActionKnown:           known,
		ApprovalToken:         approvalToken,
		ApprovalExpiresAt:     expiresAt,
		RequiresAdminApproval: requiresApproval,
		CanExecute:            canExecute,
		Status:                status,
		RepairCooldownSeconds: int(agentRepairPerAppCooldown / time.Second),
		RetryAfterSeconds:     limit.RetryAfterSeconds,
		RateLimitReason:       limit.Reason,
		Target:                target,
		Options: []llmAgentPlanOptionView{
			{
				ID:          "deny",
				Label:       "Do not allow",
				Description: "Keep the diagnosis and do not permit an automatic fix.",
				Enabled:     true,
				Selected:    true,
			},
			{
				ID:          "allow_once",
				Label:       allowLabel,
				Description: allowDescription,
				Enabled:     canExecute,
				Reason:      allowReason,
			},
		},
	}
	if requiresApproval {
		if !limit.Allowed && limit.RetryAfterSeconds > 0 {
			retryAt := time.Now().UTC().Add(limit.RetryAfter)
			response.RetryAt = &retryAt
		}
		a.deps.Audit.Record(actorID, "llm.agent_plan.proposed", map[string]interface{}{
			"plan_id":               response.ID,
			"recommended_action_id": action.ID,
			"target_kind":           target.Kind,
			"target_id":             target.ID,
			"target_resolved":       target.Resolved,
			"status":                status,
			"can_execute":           canExecute,
		})
	}
	return response
}

func (a *App) maybeExecuteAgentAutoRepair(ctx context.Context, actor users.User, snapshot models.Snapshot, plan *llmAgentPlanView) {
	if plan == nil || !plan.RequiresAdminApproval || !plan.CanExecute {
		return
	}
	a.settingsMu.RLock()
	cfg := a.deps.Config.LLM
	redactor := a.deps.Redactor
	a.settingsMu.RUnlock()
	if !cfg.AgentControlEnabled || !cfg.ActionAutoReviewEnabled {
		return
	}
	action, ok := agentActionDefinition(plan.RecommendedActionID)
	if !ok || !action.Executable || action.DockerAction != docker.ActionRestart || plan.Target.Kind != "app" || !plan.Target.Resolved {
		return
	}
	status, canExecute, reason := a.agentPlanExecutionState(action, plan.Target, snapshot, cfg, redactor)
	if !canExecute {
		return
	}
	app, ok := findAppByID(snapshot.Apps, plan.Target.ID)
	if !ok {
		return
	}
	executionAction := agentRepairActionForApp(action, app)
	if currentStatusOrUnknown(app.CurrentStatus) == models.StatusOnline {
		return
	}
	details := map[string]interface{}{
		"plan_id":                 plan.ID,
		"recommended_action_id":   action.ID,
		"target_kind":             plan.Target.Kind,
		"target_id":               plan.Target.ID,
		"app_id":                  app.AppID,
		"container_name":          app.ContainerName,
		"docker_action":           string(executionAction.DockerAction),
		"can_execute":             canExecute,
		"pre_execution_status":    status,
		"pre_execution_reason":    reason,
		"current_status":          string(currentStatusOrUnknown(app.CurrentStatus)),
		"action_auto_review_used": true,
	}
	reviewDecision, reviewEnabled, err := a.reviewAgentAction(ctx, actor, snapshot, app, executionAction, "agent_auto_repair")
	if reviewEnabled {
		details["auto_review_allow"] = reviewDecision.Allow
		details["auto_review_confidence"] = reviewDecision.Confidence
		details["auto_review_summary"] = reviewDecision.Summary
	}
	if err != nil {
		details["reason"] = "auto_review_refused"
		details["error"] = err.Error()
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.auto_review_refused", auditDetailsCopy(details))
		markAgentPlanAutoRepairRefused(plan, "auto_review_refused", err.Error())
		return
	}
	limit := a.reserveAgentRepair(app.AppID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.rate_limited", auditDetailsCopy(details))
		markAgentPlanAutoRepairRefused(plan, "approval_rate_limited", limit.Message)
		return
	}
	a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.approved", auditDetailsCopy(details))
	result, err := a.deps.Collectors.Docker.ControlContainer(ctx, app, executionAction.DockerAction)
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.execute_failed", auditDetailsCopy(details))
		markAgentPlanAutoRepairRefused(plan, "auto_execute_failed", err.Error())
		return
	}
	details["via"] = "agent_auto_repair"
	a.invalidateSnapshot()
	a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.executed", auditDetailsCopy(details))
	a.deps.Audit.Record(actor.ID, "app.container.action", map[string]interface{}{"app_id": app.AppID, "action": string(executionAction.DockerAction), "container_name": app.ContainerName, "via": "agent_auto_repair", "plan_id": plan.ID, "recommended_action_id": action.ID})
	outcome := a.verifyAgentRepairOutcome(ctx, app, executionAction, result)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.verify_failed", verifyDetails)
	}
	plan.AutoRepairAttempted = true
	plan.AutoExecuted = true
	plan.AutoRepairMessage = outcome.Message
	plan.Outcome = &outcome
	plan.Status = "auto_executed"
	plan.CanExecute = false
	plan.RequiresAdminApproval = false
	plan.ApprovalToken = ""
	plan.ApprovalExpiresAt = time.Time{}
	disableAgentPlanAllowOption(plan, outcome.Message)
}

func markAgentPlanAutoRepairRefused(plan *llmAgentPlanView, status, message string) {
	plan.AutoRepairAttempted = true
	plan.AutoRepairMessage = strings.TrimSpace(message)
	plan.Status = status
	plan.CanExecute = false
	plan.RequiresAdminApproval = false
	plan.ApprovalToken = ""
	plan.ApprovalExpiresAt = time.Time{}
	disableAgentPlanAllowOption(plan, plan.AutoRepairMessage)
}

func disableAgentPlanAllowOption(plan *llmAgentPlanView, reason string) {
	for i := range plan.Options {
		if plan.Options[i].ID != "allow_once" {
			continue
		}
		plan.Options[i].Enabled = false
		plan.Options[i].Reason = strings.TrimSpace(reason)
	}
}

func (a *App) llmUserRepairPlanResponse(diagnosis llm.Diagnosis, snapshot models.Snapshot, actorID string) *llmAgentPlanView {
	action, known := agentActionDefinition(diagnosis.RecommendedActionID)
	target := resolveAgentPlanTarget(action, diagnosis, snapshot)
	if action.ID == arrayStartActionID {
		return a.llmUserArrayStartPlanResponse(action, known, target, snapshot, actorID)
	}
	canRequest := known && action.Executable && action.DockerAction == docker.ActionRestart && target.Resolved
	canExecute := false
	directAction := docker.ActionRestart
	status := "not_actionable"
	reason := ""
	if canRequest {
		status = "request_available"
		if app, ok := findAppByID(snapshot.Apps, target.ID); ok {
			switch {
			case app.RestartAllowedGeneralUser && currentStatusOrUnknown(app.CurrentStatus) != models.StatusOnline:
				directAction = preferredGeneralUserRepairAction(app)
				canExecute = true
				status = "direct_" + string(directAction) + "_available"
			case app.RestartAllowedGeneralUser:
				reason = "This app is currently working."
			default:
				reason = "Ask an admin to review this app."
			}
		}
	} else if known && action.RequiresAppTarget && !target.Resolved {
		status = "target_unresolved"
		reason = target.Reason
	}
	title := action.Title
	summary := action.Summary
	if canExecute {
		actionLabel := dockerActionDisplayName(directAction)
		title = actionLabel + " app"
		summary = fmt.Sprintf("NoobBoard can %s this opted-in app from the standard-user view.", strings.ToLower(actionLabel))
	}
	directActionValue := ""
	if canExecute {
		directActionValue = string(directAction)
	}
	return &llmAgentPlanView{
		ID:                    agentApprovalPlanID,
		Title:                 title,
		Summary:               summary,
		RecommendedActionID:   action.ID,
		DirectAction:          directActionValue,
		ActionKnown:           known,
		RequiresAdminApproval: false,
		CanExecute:            canExecute,
		CanRequestRepair:      canRequest,
		Status:                status,
		Target:                target,
		Options: []llmAgentPlanOptionView{
			{
				ID:          string(directAction) + "_now",
				Label:       dockerActionDisplayName(directAction) + " now",
				Description: dockerActionDisplayName(directAction) + " this app from NoobBoard.",
				Enabled:     canExecute,
				Selected:    canExecute,
				Reason:      reason,
			},
			{
				ID:          "request_admin",
				Label:       "Ask admin",
				Description: "Ask an admin to review and fix this app.",
				Enabled:     canRequest,
				Selected:    canRequest && !canExecute,
				Reason:      reason,
			},
		},
	}
}

func (a *App) llmUserArrayStartPlanResponse(action llmAgentActionDefinition, known bool, target llmAgentPlanTargetView, snapshot models.Snapshot, actorID string) *llmAgentPlanView {
	canExecute := known && arrayStartNeeded(snapshot.Infrastructure)
	status := "not_actionable"
	reason := ""
	if canExecute {
		status = "direct_array_start_available"
	} else {
		reason = "The array is not currently stopped."
	}
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	executionToken := ""
	if canExecute {
		nonce, err := randomToken()
		if err != nil {
			canExecute = false
			status = "approval_locked"
			reason = "NoobBoard could not create a safe one-use action token."
		} else {
			executionToken = a.signAgentApprovalToken(agentApprovalTokenPayload{
				PlanID:              agentApprovalPlanID,
				ActorID:             actorID,
				RecommendedActionID: action.ID,
				TargetKind:          target.Kind,
				TargetID:            target.ID,
				Nonce:               nonce,
				ExpiresAt:           expiresAt.Unix(),
			})
		}
	}
	return &llmAgentPlanView{
		ID:                  agentApprovalPlanID,
		Title:               action.Title,
		Summary:             action.Summary,
		RecommendedActionID: action.ID,
		DirectAction:        "start_array",
		ActionKnown:         known,
		CanExecute:          canExecute,
		Status:              status,
		ExecutionToken:      executionToken,
		ApprovalExpiresAt:   expiresAt,
		Target:              target,
		Options: []llmAgentPlanOptionView{
			{
				ID:          "start_array_now",
				Label:       "Start array",
				Description: "Start the server storage array after checking with the admin first when possible.",
				Enabled:     canExecute,
				Selected:    canExecute,
				Reason:      reason,
			},
		},
	}
}

func adminRestartBackstopDiagnosis(diagnosis llm.Diagnosis, snapshot models.Snapshot) (llm.Diagnosis, bool) {
	if !canBackstopRestartAction(diagnosis.RecommendedActionID) {
		return diagnosis, false
	}
	app, ok := exactlyOneRestartBackstopCandidate(snapshot.Apps, func(app models.AppStatus) bool {
		return app.AgentRepairAllowed
	})
	if !ok {
		return diagnosis, false
	}
	return restartBackstopDiagnosis(diagnosis, app), true
}

func arrayStartBackstopDiagnosis(diagnosis llm.Diagnosis, snapshot models.Snapshot) (llm.Diagnosis, bool) {
	if !arrayStartNeeded(snapshot.Infrastructure) {
		return diagnosis, false
	}
	return arrayStartGuidedDiagnosis(diagnosis, snapshot.Infrastructure.UnraidArrayState), true
}

func generalUserRestartBackstopDiagnosis(diagnosis llm.Diagnosis, snapshot models.Snapshot) (llm.Diagnosis, bool) {
	if !canBackstopRestartAction(diagnosis.RecommendedActionID) {
		return diagnosis, false
	}
	app, ok := exactlyOneRestartBackstopCandidate(snapshot.Apps, func(app models.AppStatus) bool {
		return true
	})
	if !ok {
		return diagnosis, false
	}
	return restartBackstopDiagnosis(diagnosis, app), true
}

func canBackstopRestartAction(actionID string) bool {
	switch strings.TrimSpace(actionID) {
	case "", "none", "unknown", "ask_admin_to_check":
		return true
	default:
		return false
	}
}

func arrayStartNeeded(infra models.InfrastructureStatus) bool {
	if !infra.UnraidAPIReachable {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(infra.UnraidArrayState)) {
	case "stopped", "offline", "off", "down":
		return true
	default:
		return false
	}
}

func exactlyOneRestartBackstopCandidate(apps []models.AppStatus, eligible func(models.AppStatus) bool) (models.AppStatus, bool) {
	var found models.AppStatus
	count := 0
	for _, app := range apps {
		if !models.IsAppRestartCandidate(app) || !eligible(app) {
			continue
		}
		found = app
		count++
		if count > 1 {
			return models.AppStatus{}, false
		}
	}
	return found, count == 1
}

func restartBackstopDiagnosis(diagnosis llm.Diagnosis, app models.AppStatus) llm.Diagnosis {
	diagnosis.RecommendedActionID = "ask_admin_to_restart_container"
	diagnosis.RecommendedTarget = llm.ActionTarget{Kind: "app", IDOrName: app.AppID}
	if len(diagnosis.AffectedServices) == 0 {
		diagnosis.AffectedServices = []string{firstNonEmpty(app.DisplayName, app.AppID)}
	}
	return diagnosis
}

func arrayStartGuidedDiagnosis(diagnosis llm.Diagnosis, state string) llm.Diagnosis {
	state = strings.TrimSpace(state)
	if state == "" {
		state = "stopped"
	}
	diagnosis.Severity = models.SeverityHigh
	if diagnosis.Confidence <= 0 {
		diagnosis.Confidence = 0.9
	}
	diagnosis.IncidentType = models.IncidentArrayStopped
	diagnosis.AffectedServices = []string{"Unraid array"}
	diagnosis.Diagnosis = "The Unraid array is " + state + ", so apps that depend on server storage may not be able to run."
	diagnosis.GeneralUserSummary = "The server storage is stopped. Contact the admin first to make sure it was not stopped on purpose. If the admin is unavailable or asleep and service needs to be restored, it is okay to start the array."
	diagnosis.AdminMessage = "The Unraid array is " + state + ". Confirm it was not intentionally stopped; if service needs to be restored, start the array."
	diagnosis.RecommendedActionID = arrayStartActionID
	diagnosis.RecommendedTarget = llm.ActionTarget{Kind: "storage", IDOrName: arrayTargetID}
	diagnosis.ShouldNotifyAdmin = true
	evidence := "Unraid API reports array state " + state
	for _, existing := range diagnosis.Evidence {
		if strings.EqualFold(strings.TrimSpace(existing), evidence) {
			return diagnosis
		}
	}
	diagnosis.Evidence = append(diagnosis.Evidence, evidence)
	return diagnosis
}

func markSuggestedRestartPlan(plan *llmAgentPlanView, summary string) {
	if plan == nil || plan.RecommendedActionID != "ask_admin_to_restart_container" {
		return
	}
	plan.Title = "Suggested restart"
	plan.Summary = summary
}

func markSuggestedArrayStartPlan(plan *llmAgentPlanView) {
	if plan == nil || plan.RecommendedActionID != arrayStartActionID {
		return
	}
	plan.Title = "Start server storage"
	plan.Summary = "Server storage is stopped. Contact the admin first to confirm it was not stopped intentionally; if the admin is unavailable or asleep and service needs to be restored, starting it is okay."
}

func (a *App) auditSuggestedRestartPlan(actorID, mode string, plan *llmAgentPlanView) {
	if plan == nil {
		return
	}
	a.deps.Audit.Record(actorID, "llm.agent_plan.suggested", map[string]interface{}{
		"mode":                  mode,
		"plan_id":               plan.ID,
		"recommended_action_id": plan.RecommendedActionID,
		"target_kind":           plan.Target.Kind,
		"target_id":             plan.Target.ID,
		"target_resolved":       plan.Target.Resolved,
		"status":                plan.Status,
		"can_execute":           plan.CanExecute,
		"can_request_repair":    plan.CanRequestRepair,
	})
}

func (a *App) auditSuggestedArrayStartPlan(actorID, mode string, plan *llmAgentPlanView) {
	if plan == nil {
		return
	}
	a.deps.Audit.Record(actorID, "llm.agent_plan.array_start_suggested", map[string]interface{}{
		"mode":                  mode,
		"plan_id":               plan.ID,
		"recommended_action_id": plan.RecommendedActionID,
		"target_kind":           plan.Target.Kind,
		"target_id":             plan.Target.ID,
		"status":                plan.Status,
		"can_execute":           plan.CanExecute,
	})
}

func (a *App) maybeExecuteGeneralUserAutoRepair(ctx context.Context, actor users.User, plan *llmAgentPlanView) {
	if plan == nil || !plan.CanExecute || !plan.CanRequestRepair || strings.TrimSpace(plan.Target.ID) == "" {
		return
	}
	a.settingsMu.RLock()
	enabled := a.deps.Config.AppCatalog.GeneralUserAutoRepairEnabled
	a.settingsMu.RUnlock()
	if !enabled {
		return
	}
	action := docker.ContainerAction(strings.TrimSpace(plan.DirectAction))
	if action == "" {
		action = docker.ActionRestart
	}
	execution, failure := a.executeGeneralUserAppAction(ctx, actor, plan.Target.ID, action, "general_user_auto_repair", "Auto-fix")
	plan.AutoRepairAttempted = true
	plan.CanExecute = false
	plan.RequiresAdminApproval = false
	plan.ApprovalToken = ""
	plan.ApprovalExpiresAt = time.Time{}
	if failure != nil {
		status := strings.TrimSpace(failure.PlanStatus)
		if status == "" {
			status = "auto_execute_failed"
		}
		plan.Status = status
		plan.AutoRepairMessage = failure.Error()
		disableAgentPlanAllowOption(plan, plan.AutoRepairMessage)
		return
	}
	plan.AutoExecuted = true
	plan.Status = "auto_executed"
	plan.AutoRepairMessage = execution.Outcome.Message
	plan.Outcome = &execution.Outcome
	disableAgentPlanAllowOption(plan, execution.Outcome.Message)
}

type llmAgentActionDefinition struct {
	ID                string
	Title             string
	Summary           string
	ApprovalEligible  bool
	RequiresAppTarget bool
	Executable        bool
	DockerAction      docker.ContainerAction
}

var llmAgentActionRegistry = map[string]llmAgentActionDefinition{
	"none": {
		ID:      "none",
		Title:   "No action recommended",
		Summary: "The model did not recommend an admin action.",
	},
	"unknown": {
		ID:      "unknown",
		Title:   "Unclear recommendation",
		Summary: "The model did not return a specific action that NoobBoard can place behind an approval popup.",
	},
	"ask_admin_to_check": {
		ID:      "ask_admin_to_check",
		Title:   "Manual check recommendation",
		Summary: "The model suggested an admin check. NoobBoard will not run a mutating action for this recommendation.",
	},
	"ask_admin_to_restart_container": {
		ID:                "ask_admin_to_restart_container",
		Title:             "App fix recommendation",
		Summary:           "The model suggested repairing one app. NoobBoard can start a stopped app or restart a running app only after admin approval, safety review, and per-app opt-in.",
		ApprovalEligible:  true,
		RequiresAppTarget: true,
		Executable:        true,
		DockerAction:      docker.ActionRestart,
	},
	arrayStartActionID: {
		ID:         arrayStartActionID,
		Title:      "Start array",
		Summary:    "The model identified that the Unraid array is stopped. NoobBoard can start the array from compact chat only after the signed LLM plan is used.",
		Executable: true,
	},
	"ask_admin_to_check_unifi": {
		ID:      "ask_admin_to_check_unifi",
		Title:   "Network check recommendation",
		Summary: "The model suggested checking router or network status. NoobBoard does not execute network repair actions.",
	},
	"ask_admin_to_check_storage": {
		ID:      "ask_admin_to_check_storage",
		Title:   "Storage check recommendation",
		Summary: "The model suggested checking server storage. Chat cannot run Unraid storage actions.",
	},
}

func agentActionDefinition(id string) (llmAgentActionDefinition, bool) {
	actionID := strings.TrimSpace(id)
	if actionID == "" {
		actionID = "unknown"
	}
	action, ok := llmAgentActionRegistry[actionID]
	if ok {
		return action, true
	}
	return llmAgentActionRegistry["unknown"], false
}

func userAppControlActionDefinition(action docker.ContainerAction) (llmAgentActionDefinition, bool) {
	switch action {
	case docker.ActionStart:
		return llmAgentActionDefinition{
			ID:                "general_user_start_container",
			Title:             "Start app",
			Summary:           "A standard user requested that NoobBoard start one opted-in app.",
			RequiresAppTarget: true,
			Executable:        true,
			DockerAction:      docker.ActionStart,
		}, true
	case docker.ActionStop:
		return llmAgentActionDefinition{
			ID:                "general_user_stop_container",
			Title:             "Stop app",
			Summary:           "A standard user requested that NoobBoard stop one opted-in app.",
			RequiresAppTarget: true,
			Executable:        true,
			DockerAction:      docker.ActionStop,
		}, true
	case docker.ActionRestart:
		action, _ := agentActionDefinition("ask_admin_to_restart_container")
		return action, true
	default:
		return llmAgentActionDefinition{}, false
	}
}

func agentRepairActionForApp(action llmAgentActionDefinition, app models.AppStatus) llmAgentActionDefinition {
	if action.ID != "ask_admin_to_restart_container" || !action.Executable {
		return action
	}
	if app.DockerState == models.DockerExited {
		action.Title = "Start recommendation"
		action.Summary = "The target app is stopped. NoobBoard can start it after admin approval, safety review, and per-app opt-in."
		action.DockerAction = docker.ActionStart
		return action
	}
	action.Title = "Restart recommendation"
	action.Summary = "The model suggested repairing one app. NoobBoard can restart it after admin approval, safety review, and per-app opt-in."
	action.DockerAction = docker.ActionRestart
	return action
}

func resolveAgentPlanTarget(action llmAgentActionDefinition, diagnosis llm.Diagnosis, snapshot models.Snapshot) llmAgentPlanTargetView {
	target := llmAgentPlanTargetView{
		Kind:   firstNonEmpty(strings.TrimSpace(diagnosis.RecommendedTarget.Kind), "none"),
		Query:  strings.TrimSpace(diagnosis.RecommendedTarget.IDOrName),
		Reason: "No specific target is needed for this recommendation.",
	}
	if action.ID == arrayStartActionID {
		target.Kind = "storage"
		target.ID = arrayTargetID
		target.Label = "Unraid array"
		target.Query = firstNonEmpty(target.Query, arrayTargetID)
		target.Resolved = true
		target.Reason = ""
		return target
	}
	if !action.RequiresAppTarget {
		if target.Kind == "none" || target.Query == "" {
			return target
		}
		target.Reason = "Target was provided by the model but this action does not require an app target."
		return target
	}
	target.Kind = "app"
	candidates := make([]string, 0, len(diagnosis.AffectedServices)+1)
	if strings.TrimSpace(diagnosis.RecommendedTarget.IDOrName) != "" {
		candidates = append(candidates, diagnosis.RecommendedTarget.IDOrName)
	}
	candidates = append(candidates, diagnosis.AffectedServices...)
	for _, candidate := range candidates {
		app, ok := findAppByID(snapshot.Apps, candidate)
		if !ok {
			continue
		}
		target.ID = app.AppID
		target.Label = firstNonEmpty(app.DisplayName, app.AppID)
		target.Query = strings.TrimSpace(candidate)
		target.Resolved = true
		target.Reason = ""
		return target
	}
	target.Reason = "No exact app target from the model recommendation matched the current admin app snapshot."
	return target
}

func (a *App) agentPlanExecutionState(action llmAgentActionDefinition, target llmAgentPlanTargetView, snapshot models.Snapshot, cfg config.LLMConfig, redactor *privacy.Redactor) (string, bool, string) {
	if !action.ApprovalEligible {
		return "not_actionable", false, ""
	}
	if action.RequiresAppTarget && !target.Resolved {
		return "target_unresolved", false, target.Reason
	}
	if !action.Executable {
		return "approval_locked", false, "This recommendation is informational; NoobBoard only executes app start/restart repairs in this version."
	}
	if !cfg.AgentControlEnabled {
		return "approval_locked", false, "Enable the action approval gate in LLM settings before a fix can run."
	}
	app, ok := findAppByID(snapshot.Apps, target.ID)
	if !ok {
		return "target_unresolved", false, "The target app is no longer present in the current app snapshot."
	}
	if redactor != nil && redactor.IsBlacklistedApp(app) {
		return "approval_locked", false, "This app is privacy-blacklisted, so app fixes are unavailable."
	}
	if !app.AgentRepairAllowed {
		return "approval_locked", false, "Turn on admin/AI app fix for this app in app settings before a fix can run."
	}
	if limit := a.agentRepairLimitState(app.AppID, time.Now().UTC(), false); !limit.Allowed {
		return "approval_rate_limited", false, limit.Message
	}
	return "approval_ready", true, ""
}

func (a *App) reviewAgentAction(ctx context.Context, actor users.User, snapshot models.Snapshot, app models.AppStatus, action llmAgentActionDefinition, via string) (llm.ActionReviewDecision, bool, error) {
	a.settingsMu.RLock()
	cfg := a.deps.Config.LLM
	redactor := a.deps.Redactor
	sameClient := a.deps.LLM
	a.settingsMu.RUnlock()
	if !cfg.ActionAutoReviewEnabled {
		return llm.ActionReviewDecision{}, false, nil
	}
	reviewClient, model, err := actionAutoReviewClient(cfg, redactor, sameClient)
	if err != nil {
		return llm.ActionReviewDecision{}, true, err
	}
	filtered := privacy.FilterSnapshotForRole(snapshot, models.RoleAdmin, redactor)
	refs := loadActionReviewReferences(cfg.ActionAutoReviewReferencePaths)
	request := llm.ActionReviewRequest{
		ActionID:      action.ID,
		ActionTitle:   action.Title,
		TargetID:      app.AppID,
		TargetLabel:   firstNonEmpty(app.DisplayName, app.ContainerName, app.AppID),
		CurrentStatus: currentStatusOrUnknown(app.CurrentStatus),
		ActorRole:     actor.Role,
		Via:           via,
		Reasoning:     cfg.ActionAutoReviewReasoning,
		References:    refs,
		Snapshot:      filtered,
	}
	decision, err := reviewClient.ReviewAction(ctx, request)
	if err != nil {
		return llm.ActionReviewDecision{}, true, err
	}
	a.deps.Audit.Record(actor.ID, "llm.agent_plan.auto_reviewed", map[string]interface{}{
		"app_id":                app.AppID,
		"recommended_action_id": action.ID,
		"via":                   via,
		"review_model":          model,
		"allow":                 decision.Allow,
		"confidence":            decision.Confidence,
		"summary":               decision.Summary,
		"issues":                decision.Issues,
		"reference_count":       len(refs),
	})
	if !decision.Allow {
		return decision, true, errors.New("auto-review did not allow this repair: " + decision.Summary)
	}
	return decision, true, nil
}

func actionAutoReviewClient(cfg config.LLMConfig, redactor *privacy.Redactor, sameClient llm.Client) (llm.Client, string, error) {
	reviewCfg := cfg
	model := strings.TrimSpace(cfg.ActionAutoReviewModel)
	if model == "" || model == "same" {
		if sameClient != nil {
			return sameClient, "same", nil
		}
		return llm.NewClient(reviewCfg, redactor), "same", nil
	}
	provider, modelID, ok := strings.Cut(model, "/")
	if !ok || strings.TrimSpace(provider) == "" || strings.TrimSpace(modelID) == "" {
		return nil, "", fmt.Errorf("invalid action auto-review model %q", model)
	}
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	switch provider {
	case "openai":
		reviewCfg.Provider = "openai"
		reviewCfg.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
		reviewCfg.OpenAIModel = modelID
	case "chatgpt":
		reviewCfg.Provider = "openai"
		if reviewCfg.OpenAIAuthMethod == config.OpenAIAuthMethodAPIKey {
			reviewCfg.OpenAIAuthMethod = config.OpenAIAuthMethodChatGPTHeadless
		}
		reviewCfg.OpenAIModel = modelID
	case "anthropic":
		reviewCfg.Provider = "anthropic"
		reviewCfg.AnthropicModel = modelID
	default:
		return nil, "", fmt.Errorf("unsupported action auto-review provider %q", provider)
	}
	if !llm.ProviderAvailable(reviewCfg) {
		return nil, "", fmt.Errorf("action auto-review provider %s is not available", model)
	}
	return llm.NewClient(reviewCfg, redactor), model, nil
}

func loadActionReviewReferences(paths []string) []llm.ActionReviewReference {
	var refs []llm.ActionReviewReference
	total := 0
	for _, rawPath := range compactStrings(paths) {
		if len(refs) >= actionReviewReferenceLimit || total >= actionReviewReferenceBytes {
			break
		}
		resolved, ok := safeActionReviewReferencePath(rawPath)
		if !ok {
			continue
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			continue
		}
		if len(data) > actionReviewReferenceFileBytes {
			data = data[:actionReviewReferenceFileBytes]
		}
		remaining := actionReviewReferenceBytes - total
		if remaining <= 0 {
			break
		}
		if len(data) > remaining {
			data = data[:remaining]
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		refs = append(refs, llm.ActionReviewReference{Path: filepath.ToSlash(filepath.Clean(rawPath)), Content: content})
		total += len(data)
	}
	return refs
}

func safeActionReviewReferencePath(rawPath string) (string, bool) {
	path := strings.TrimSpace(rawPath)
	if path == "" || filepath.IsAbs(path) {
		return "", false
	}
	clean := filepath.Clean(path)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", false
	}
	base := filepath.Base(clean)
	if clean == "README.md" || clean == "AGENTS.md" || strings.HasPrefix(clean, "docs"+string(filepath.Separator)) {
		return filepath.Join(".", clean), true
	}
	if strings.EqualFold(base, "README.md") || strings.EqualFold(base, "AGENTS.md") {
		return filepath.Join(".", clean), true
	}
	return "", false
}

type agentRepairLimitDecision struct {
	Allowed           bool
	Reason            string
	Message           string
	RetryAfter        time.Duration
	RetryAfterSeconds int
}

func (a *App) reserveAgentRepair(appID string, now time.Time) agentRepairLimitDecision {
	return a.agentRepairLimitState(appID, now, true)
}

func (a *App) agentRepairLimitState(appID string, now time.Time, reserve bool) agentRepairLimitDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	key := strings.ToLower(strings.TrimSpace(appID))
	if key == "" {
		key = "unknown"
	}
	a.agentRepairMu.Lock()
	defer a.agentRepairMu.Unlock()
	if a.agentRepairLastByApp == nil {
		a.agentRepairLastByApp = map[string]time.Time{}
	}
	for appKey, at := range a.agentRepairLastByApp {
		if !at.IsZero() && !at.Add(agentRepairPerAppCooldown).After(now) {
			delete(a.agentRepairLastByApp, appKey)
		}
	}
	global := a.agentRepairGlobal[:0]
	for _, at := range a.agentRepairGlobal {
		if at.IsZero() {
			continue
		}
		if at.Add(agentRepairGlobalWindow).After(now) {
			global = append(global, at)
		}
	}
	a.agentRepairGlobal = global
	if last := a.agentRepairLastByApp[key]; !last.IsZero() {
		if retryAfter := last.Add(agentRepairPerAppCooldown).Sub(now); retryAfter > 0 {
			return agentRepairLimitDecision{
				Allowed:           false,
				Reason:            "per_app_cooldown",
				Message:           "Automatic repair is cooling down for this app. Try again in " + shortDurationText(retryAfter) + ".",
				RetryAfter:        retryAfter,
				RetryAfterSeconds: int((retryAfter + time.Second - 1) / time.Second),
			}
		}
	}
	if len(a.agentRepairGlobal) >= agentRepairGlobalLimit {
		retryAfter := a.agentRepairGlobal[0].Add(agentRepairGlobalWindow).Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return agentRepairLimitDecision{
			Allowed:           false,
			Reason:            "global_rate_limit",
			Message:           "The repair rate limit has been reached. Try again in " + shortDurationText(retryAfter) + ".",
			RetryAfter:        retryAfter,
			RetryAfterSeconds: int((retryAfter + time.Second - 1) / time.Second),
		}
	}
	if reserve {
		a.agentRepairLastByApp[key] = now
		a.agentRepairGlobal = append(a.agentRepairGlobal, now)
	}
	return agentRepairLimitDecision{Allowed: true}
}

func shortDurationText(duration time.Duration) string {
	if duration <= 0 {
		return "a moment"
	}
	if duration < time.Minute {
		seconds := int((duration + time.Second - 1) / time.Second)
		if seconds == 1 {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", seconds)
	}
	minutes := int((duration + time.Minute - 1) / time.Minute)
	if minutes == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}

func (a *App) recordAgentApproval(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		ApprovalToken string `json:"approval_token"`
		Choice        string `json:"choice"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	choice := strings.TrimSpace(body.Choice)
	payload, err := a.verifyAgentApprovalToken(strings.TrimSpace(body.ApprovalToken), mustUser(r).ID)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	action, ok := agentActionDefinition(payload.RecommendedActionID)
	if !ok || !action.ApprovalEligible {
		writeError(w, http.StatusBadRequest, errors.New("approval plan is not eligible for approval"))
		return
	}
	if action.RequiresAppTarget && (payload.TargetKind != "app" || strings.TrimSpace(payload.TargetID) == "") {
		writeError(w, http.StatusBadRequest, errors.New("approval plan target is missing or invalid"))
		return
	}
	if !validAgentApprovalChoice(choice) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported approval choice"))
		return
	}
	a.settingsMu.RLock()
	llmCfg := a.deps.Config.LLM
	a.settingsMu.RUnlock()
	details := map[string]interface{}{
		"plan_id":               payload.PlanID,
		"choice":                choice,
		"recommended_action_id": action.ID,
		"target_kind":           payload.TargetKind,
		"target_id":             payload.TargetID,
		"can_execute":           false,
	}
	if choice == "deny" {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.denied", details)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "denied"})
		return
	}
	if !llmCfg.AgentControlEnabled {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.control_disabled", details)
		writeError(w, http.StatusConflict, errors.New("admin-approved app fixes are disabled in LLM settings"))
		return
	}
	if !action.Executable || action.DockerAction != docker.ActionRestart {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.non_executable", details)
		writeError(w, http.StatusConflict, errors.New("this recommendation does not have an executable repair action"))
		return
	}
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, payload.TargetID)
	if !ok {
		details["reason"] = "target_unresolved"
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.refused", details)
		writeError(w, http.StatusConflict, errors.New("approval target is no longer present in the current app snapshot"))
		return
	}
	details["app_id"] = app.AppID
	details["container_name"] = app.ContainerName
	executionAction := agentRepairActionForApp(action, app)
	details["docker_action"] = string(executionAction.DockerAction)
	if a.redactorSnapshot().IsBlacklistedApp(app) {
		details["reason"] = "privacy_blacklisted"
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.refused", details)
		writeError(w, http.StatusConflict, errors.New("app fixes are unavailable for privacy-blacklisted apps"))
		return
	}
	if !app.AgentRepairAllowed {
		details["reason"] = "app_not_opted_in"
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.refused", details)
		writeError(w, http.StatusConflict, errors.New("admin/AI app fix is not enabled for this app"))
		return
	}
	if strings.TrimSpace(payload.Nonce) == "" {
		writeError(w, http.StatusForbidden, errors.New("approval token is missing a replay nonce"))
		return
	}
	reviewDecision, reviewEnabled, err := a.reviewAgentAction(r.Context(), mustUser(r), snapshot, app, executionAction, "agent_plan")
	if reviewEnabled {
		details["auto_review_allow"] = reviewDecision.Allow
		details["auto_review_confidence"] = reviewDecision.Confidence
		details["auto_review_summary"] = reviewDecision.Summary
	}
	if err != nil {
		details["reason"] = "auto_review_refused"
		details["error"] = err.Error()
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.auto_review_refused", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, err)
		return
	}
	if !a.consumeAgentApproval(payload) {
		details["reason"] = "approval_replay"
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.replay_blocked", details)
		writeError(w, http.StatusConflict, errors.New("approval token has already been used"))
		return
	}
	limit := a.reserveAgentRepair(app.AppID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.rate_limited", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, errors.New(limit.Message))
		return
	}
	details["can_execute"] = true
	a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.approved", auditDetailsCopy(details))
	result, err := a.deps.Collectors.Docker.ControlContainer(r.Context(), app, executionAction.DockerAction)
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.execute_failed", auditDetailsCopy(details))
		writeError(w, http.StatusBadGateway, err)
		return
	}
	details["via"] = "agent_plan"
	a.invalidateSnapshot()
	a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.executed", auditDetailsCopy(details))
	a.deps.Audit.Record(mustUser(r).ID, "app.container.action", map[string]interface{}{"app_id": app.AppID, "action": string(executionAction.DockerAction), "container_name": app.ContainerName, "via": "agent_plan", "plan_id": payload.PlanID, "recommended_action_id": action.ID})
	outcome := a.verifyAgentRepairOutcome(r.Context(), app, executionAction, result)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.verify_failed", verifyDetails)
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "executed",
		"result":  result,
		"outcome": outcome,
	})
}

func (a *App) verifyAgentRepairOutcome(ctx context.Context, before models.AppStatus, action llmAgentActionDefinition, result docker.ControlResult) llmAgentRepairOutcomeView {
	return a.verifyRepairOutcome(ctx, before, action, result, "Auto-repair")
}

func (a *App) verifyRepairOutcome(ctx context.Context, before models.AppStatus, action llmAgentActionDefinition, result docker.ControlResult, label string) llmAgentRepairOutcomeView {
	beforeStatus := currentStatusOrUnknown(before.CurrentStatus)
	targetLabel := firstNonEmpty(before.DisplayName, before.ContainerName, before.AppID)
	messagePrefix := strings.TrimSpace(label)
	if messagePrefix == "" {
		messagePrefix = "Auto-repair"
	}
	outcome := llmAgentRepairOutcomeView{
		Action:       string(action.DockerAction),
		TargetID:     before.AppID,
		TargetLabel:  targetLabel,
		BeforeStatus: beforeStatus,
		AfterStatus:  models.StatusUnknown,
		CheckedAt:    time.Now().UTC(),
		Message:      dockerActionDisplayName(action.DockerAction) + " was sent, but NoobBoard could not verify the app status yet.",
		ResultStatus: strings.TrimSpace(result.Status),
	}
	var afterSnapshot models.Snapshot
	var afterSnapshotSet bool
	attempts := agentRepairVerificationAttempts
	if attempts <= 0 || agentRepairVerificationDelay <= 0 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if delay := agentRepairVerificationDelay; delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				outcome.Message = dockerActionDisplayName(action.DockerAction) + " was sent, but verification was cancelled before NoobBoard could refresh status."
				return outcome
			case <-timer.C:
			}
		}
		a.invalidateSnapshot()
		refreshed, err := a.refreshSnapshot(ctx, false)
		outcome.CheckedAt = time.Now().UTC()
		if err != nil {
			outcome.Message = dockerActionDisplayName(action.DockerAction) + " was sent, but status verification failed: " + err.Error()
			return outcome
		}
		afterSnapshot = refreshed
		afterSnapshotSet = true
		afterApp, ok := findAppBySameIdentity(afterSnapshot.Apps, before)
		if !ok {
			outcome.Verified = true
			outcome.AfterStatus = models.StatusUnknown
			outcome.Recovered = action.DockerAction == docker.ActionStop
			outcome.Message = messagePrefix + ": " + dockerActionPastTense(action.DockerAction) + " - target app was not present after refresh."
			break
		}
		outcome.Verified = true
		outcome.TargetID = firstNonEmpty(afterApp.AppID, outcome.TargetID)
		outcome.AfterStatus = currentStatusOrUnknown(afterApp.CurrentStatus)
		outcome.TargetLabel = firstNonEmpty(afterApp.DisplayName, afterApp.ContainerName, afterApp.AppID, targetLabel)
		outcome.Recovered = dockerActionReachedExpectedState(action.DockerAction, afterApp)
		if outcome.Recovered {
			outcome.Message = messagePrefix + ": " + dockerActionSuccessPhrase(action.DockerAction) + "."
			break
		}
		if attempt == attempts-1 {
			outcome.Message = messagePrefix + ": " + dockerActionFinalWaitingPhrase(action.DockerAction) + "."
		} else {
			outcome.Message = messagePrefix + ": " + dockerActionWaitingPhrase(action.DockerAction) + "."
		}
	}
	if historyEventID, err := a.appendAgentRepairHistoryEvent(outcome); err == nil {
		outcome.HistoryEventID = historyEventID
		if a.historyRecorder != nil && afterSnapshotSet {
			a.historyRecorder.Observe(afterSnapshot)
		}
	} else {
		outcome.HistoryError = err.Error()
	}
	return outcome
}

func (a *App) verifyArrayStartOutcome(ctx context.Context, before models.InfrastructureStatus, result unraid.ArrayControlResult) llmAgentRepairOutcomeView {
	outcome := llmAgentRepairOutcomeView{
		Action:       "start_array",
		TargetID:     arrayTargetID,
		TargetLabel:  "Unraid array",
		BeforeStatus: arrayHistoryStatus(before),
		AfterStatus:  models.StatusUnknown,
		CheckedAt:    time.Now().UTC(),
		Message:      "Start array was sent, but NoobBoard could not verify the array status yet.",
		ResultStatus: strings.TrimSpace(result.Status),
	}
	var afterSnapshot models.Snapshot
	var afterSnapshotSet bool
	attempts := agentRepairVerificationAttempts
	if attempts <= 0 || agentRepairVerificationDelay <= 0 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if delay := agentRepairVerificationDelay; delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				outcome.Message = "Start array was sent, but verification was cancelled before NoobBoard could refresh status."
				return outcome
			case <-timer.C:
			}
		}
		a.invalidateSnapshot()
		refreshed, err := a.refreshSnapshot(ctx, false)
		outcome.CheckedAt = time.Now().UTC()
		if err != nil {
			outcome.Message = "Start array was sent, but status verification failed: " + err.Error()
			return outcome
		}
		afterSnapshot = refreshed
		afterSnapshotSet = true
		after := refreshed.Infrastructure
		outcome.Verified = true
		outcome.AfterStatus = arrayHistoryStatus(after)
		outcome.Recovered = strings.EqualFold(strings.TrimSpace(after.UnraidArrayState), "started")
		if outcome.Recovered {
			outcome.Message = "Unraid array started successfully. Send another message or try again if you continue to have issues."
			break
		}
		if attempt == attempts-1 {
			outcome.Message = "Start array was sent, but the array still does not report started."
		} else {
			outcome.Message = "Start array was sent. Waiting for the array to report started."
		}
	}
	if historyEventID, err := a.appendArrayActionHistoryEvent(outcome); err == nil {
		outcome.HistoryEventID = historyEventID
		if a.historyRecorder != nil && afterSnapshotSet {
			a.historyRecorder.Observe(afterSnapshot)
		}
	} else {
		outcome.HistoryError = err.Error()
	}
	return outcome
}

func (a *App) appendArrayActionHistoryEvent(outcome llmAgentRepairOutcomeView) (string, error) {
	if a.deps.History == nil {
		return "", nil
	}
	eventID := agentRepairHistoryEventID(outcome)
	event := models.StatusEvent{
		ID:          eventID,
		SubjectType: models.SubjectInfra,
		SubjectID:   arrayTargetID,
		DisplayName: "Unraid array",
		From:        outcome.BeforeStatus,
		To:          outcome.AfterStatus,
		At:          outcome.CheckedAt,
		Note:        outcome.Message,
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if err := a.deps.History.Append([]models.StatusEvent{event}); err != nil {
		return "", err
	}
	return eventID, nil
}

func (a *App) appendAgentRepairHistoryEvent(outcome llmAgentRepairOutcomeView) (string, error) {
	if a.deps.History == nil || strings.TrimSpace(outcome.TargetID) == "" {
		return "", nil
	}
	eventID := agentRepairHistoryEventID(outcome)
	event := models.StatusEvent{
		ID:          eventID,
		SubjectType: models.SubjectApp,
		SubjectID:   outcome.TargetID,
		DisplayName: outcome.TargetLabel,
		From:        outcome.BeforeStatus,
		To:          outcome.AfterStatus,
		At:          outcome.CheckedAt,
		Note:        outcome.Message,
	}
	if event.DisplayName == "" {
		event.DisplayName = outcome.TargetID
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if err := a.deps.History.Append([]models.StatusEvent{event}); err != nil {
		return "", err
	}
	return eventID, nil
}

func agentRepairHistoryEventID(outcome llmAgentRepairOutcomeView) string {
	return fmt.Sprintf("agent-repair-%s-%d-%s", sanitizeAgentRepairIDPart(outcome.TargetID), outcome.CheckedAt.UnixNano(), outcome.AfterStatus)
}

func sanitizeAgentRepairIDPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func currentStatusOrUnknown(status models.CurrentStatus) models.CurrentStatus {
	if status == "" {
		return models.StatusUnknown
	}
	return status
}

func auditDetailsCopy(details map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(details))
	for key, value := range details {
		out[key] = value
	}
	return out
}

func (a *App) setAgentArm(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Armed           bool `json:"armed"`
		DurationSeconds int  `json:"duration_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.RLock()
	cfg := a.deps.Config.LLM
	a.settingsMu.RUnlock()
	duration := cfg.AgentArmDuration
	if body.DurationSeconds > 0 {
		duration = time.Duration(body.DurationSeconds) * time.Second
	}
	if duration <= 0 {
		duration = config.Defaults().LLM.AgentArmDuration
	}
	if duration > time.Hour {
		duration = time.Hour
	}
	var until time.Time
	action := "llm.agent.disarmed"
	if body.Armed {
		if !cfg.AgentControlEnabled {
			writeError(w, http.StatusConflict, errors.New("agent action approval gate is disabled in LLM settings"))
			return
		}
		until = time.Now().UTC().Add(duration)
		action = "llm.agent.armed"
	}
	updated, ok := a.sessions.setAgentArmed(mustSession(r).Token, until)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, action, map[string]interface{}{
		"armed":            body.Armed,
		"armed_until":      until,
		"duration_seconds": int(duration / time.Second),
	})
	writeJSON(w, http.StatusOK, llmAgentReadinessResponse(cfg, updated))
}

func (a *App) signAgentApprovalToken(payload agentApprovalTokenPayload) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, a.agentApprovalSecret)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature
}

func (a *App) verifyAgentApprovalToken(token, actorID string) (agentApprovalTokenPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return agentApprovalTokenPayload{}, errors.New("valid approval token is required")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return agentApprovalTokenPayload{}, errors.New("valid approval token is required")
	}
	mac := hmac.New(sha256.New, a.agentApprovalSecret)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	var payload agentApprovalTokenPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	if payload.PlanID != agentApprovalPlanID || strings.TrimSpace(payload.RecommendedActionID) == "" {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	if _, ok := agentActionDefinition(payload.RecommendedActionID); !ok {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	if payload.ActorID != actorID {
		return agentApprovalTokenPayload{}, errors.New("approval token is not valid for this user")
	}
	if payload.ExpiresAt <= 0 || time.Now().UTC().After(time.Unix(payload.ExpiresAt, 0)) {
		return agentApprovalTokenPayload{}, errors.New("approval token has expired")
	}
	return payload, nil
}

func validAgentApprovalChoice(choice string) bool {
	switch choice {
	case "deny", "allow_once":
		return true
	default:
		return false
	}
}

func (a *App) consumeAgentApproval(payload agentApprovalTokenPayload) bool {
	nonce := strings.TrimSpace(payload.Nonce)
	if nonce == "" {
		return false
	}
	expiresAt := time.Unix(payload.ExpiresAt, 0).UTC()
	now := time.Now().UTC()
	a.agentApprovalMu.Lock()
	defer a.agentApprovalMu.Unlock()
	for key, expiry := range a.consumedAgentApprovals {
		if !expiry.After(now) {
			delete(a.consumedAgentApprovals, key)
		}
	}
	key := payload.ActorID + "|" + payload.PlanID + "|" + payload.RecommendedActionID + "|" + payload.TargetKind + "|" + payload.TargetID + "|" + nonce
	if _, exists := a.consumedAgentApprovals[key]; exists {
		return false
	}
	a.consumedAgentApprovals[key] = expiresAt
	return true
}

func (a *App) notifyAdmin(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Message string `json:"message"`
		AppID   string `json:"app_id"`
		Context string `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		message = "A standard user reported a problem."
	}
	if contextText := strings.TrimSpace(body.Context); contextText != "" {
		message += " " + contextText
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	appID := strings.TrimSpace(body.AppID)
	dedupe := "notify-admin:" + mustUser(r).ID + ":" + appID
	sent, err := a.deps.Notifications.NotifyAdmins(r.Context(), "NoobBoard user report", message, appID, dedupe)
	if err != nil {
		a.deps.Audit.Record(mustUser(r).ID, "user.notify_admin.failed", map[string]interface{}{"app_id": appID, "error": err.Error()})
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "user.notify_admin", map[string]interface{}{"message": message, "app_id": appID, "admin_notifications": sent})
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"status": "queued", "admin_notifications": sent})
}

func (a *App) createRepairRequest(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		AppID            string `json:"app_id"`
		ActionID         string `json:"action_id"`
		DiagnosisSummary string `json:"diagnosis_summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	appID := strings.TrimSpace(body.AppID)
	if appID == "" {
		writeError(w, http.StatusBadRequest, errors.New("app_id is required"))
		return
	}
	actionID := strings.TrimSpace(body.ActionID)
	if actionID == "" {
		actionID = "ask_admin_to_restart_container"
	}
	action, ok := agentActionDefinition(actionID)
	if !ok || !action.Executable || action.DockerAction != docker.ActionRestart {
		writeError(w, http.StatusBadRequest, errors.New("requested repair action is not supported"))
		return
	}
	visibleSnapshot, err := a.Snapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	visibleApp, ok := findAppByID(visibleSnapshot.Apps, appID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("app is not visible to this user"))
		return
	}
	fullSnapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	fullApp, ok := findAppByID(fullSnapshot.Apps, appID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("app is not available in the current snapshot"))
		return
	}
	if !isDockerRepairTarget(fullApp) {
		writeError(w, http.StatusBadRequest, errors.New("this app cannot be restarted by NoobBoard"))
		return
	}
	if existing, ok, err := a.pendingRepairRequestFor(mustUser(r).ID, fullApp.AppID, action.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	} else if ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "pending", "request": existing, "duplicate": true})
		return
	}
	requestID, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	user := mustUser(r)
	now := time.Now().UTC()
	request := models.RepairRequest{
		ID:               "repair-" + sanitizeAgentRepairIDPart(requestID),
		RequesterID:      user.ID,
		RequesterName:    firstNonEmpty(user.DisplayName, user.Username, user.ID),
		RequesterRole:    user.Role,
		AppID:            fullApp.AppID,
		AppLabel:         firstNonEmpty(visibleApp.DisplayName, fullApp.DisplayName, fullApp.ContainerName, fullApp.AppID),
		ActionID:         action.ID,
		DiagnosisSummary: strings.TrimSpace(body.DiagnosisSummary),
		Status:           models.RepairRequestPending,
		CreatedAt:        now,
	}
	if request.DiagnosisSummary == "" {
		request.DiagnosisSummary = compactAppSummaryForRequest(visibleApp)
	}
	if err := a.deps.Store.UpsertRepairRequest(request); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	message := fmt.Sprintf("%s asked for help with %s. %s", request.RequesterName, request.AppLabel, request.DiagnosisSummary)
	sent, err := a.deps.Notifications.NotifyAdmins(r.Context(), "Repair requested: "+request.AppLabel, message, request.AppID, "repair-request:"+request.ID)
	if err != nil {
		a.deps.Audit.Record(user.ID, "user.repair_request.notify_failed", map[string]interface{}{"request_id": request.ID, "app_id": request.AppID, "error": err.Error()})
	}
	a.deps.Audit.Record(user.ID, "user.repair_request.created", map[string]interface{}{"request_id": request.ID, "app_id": request.AppID, "action_id": request.ActionID, "admin_notifications": sent})
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"status": "pending", "request": request, "admin_notifications": sent})
}

func (a *App) userRepairRequests(w http.ResponseWriter, r *http.Request) {
	requests, err := a.deps.Store.RepairRequestsForUser(mustUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, repairRequestsNewestFirst(requests))
}

func (a *App) restartUserApp(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Confirmed    bool   `json:"confirmed"`
		ConfirmAppID string `json:"confirm_app_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.executeUserAppAction(w, r, docker.ActionRestart, body.Confirmed, body.ConfirmAppID)
}

func (a *App) controlUserApp(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Action       string `json:"action"`
		Confirmed    bool   `json:"confirmed"`
		ConfirmAppID string `json:"confirm_app_id"`
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
	a.executeUserAppAction(w, r, action, body.Confirmed, body.ConfirmAppID)
}

type userAppActionExecution struct {
	Result  docker.ControlResult
	Outcome llmAgentRepairOutcomeView
}

type userAppActionFailure struct {
	HTTPStatus int
	PlanStatus string
	Err        error
}

func (f *userAppActionFailure) Error() string {
	if f == nil || f.Err == nil {
		return ""
	}
	return f.Err.Error()
}

func newUserAppActionFailure(httpStatus int, planStatus string, err error) *userAppActionFailure {
	if err == nil {
		err = errors.New("app action failed")
	}
	return &userAppActionFailure{HTTPStatus: httpStatus, PlanStatus: strings.TrimSpace(planStatus), Err: err}
}

func (a *App) executeUserAppAction(w http.ResponseWriter, r *http.Request, action docker.ContainerAction, confirmed bool, confirmAppID string) {
	appID := strings.TrimSpace(r.PathValue("id"))
	if appID == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	if _, ok := userAppControlActionDefinition(action); !ok {
		writeError(w, http.StatusBadRequest, errors.New("app action is not supported"))
		return
	}
	user := mustUser(r)
	visibleSnapshot, err := a.Snapshot(r.Context(), user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	visibleApp, ok := findAppByID(visibleSnapshot.Apps, appID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("app is not visible to this user"))
		return
	}
	if !sameAppIdentifier(confirmAppID, visibleApp.AppID) || !confirmed {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s requires confirmed=true with a matching confirm_app_id", action))
		return
	}
	execution, failure := a.executeGeneralUserAppAction(r.Context(), user, visibleApp.AppID, action, "general_user_direct", dockerActionDisplayName(action))
	if failure != nil {
		writeError(w, failure.HTTPStatus, failure.Err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "executed",
		"result":  execution.Result,
		"outcome": execution.Outcome,
	})
}

func (a *App) executeGeneralUserAppAction(ctx context.Context, user users.User, appID string, action docker.ContainerAction, via string, outcomeLabel string) (userAppActionExecution, *userAppActionFailure) {
	var empty userAppActionExecution
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return empty, newUserAppActionFailure(http.StatusBadRequest, "target_unresolved", errors.New("app id is required"))
	}
	requestedAction := action
	actionDef, ok := userAppControlActionDefinition(action)
	if !ok {
		return empty, newUserAppActionFailure(http.StatusBadRequest, "not_actionable", errors.New("app action is not supported"))
	}
	via = strings.TrimSpace(via)
	if via == "" {
		via = "general_user_direct"
	}
	visibleSnapshot, err := a.Snapshot(ctx, user.Role)
	if err != nil {
		return empty, newUserAppActionFailure(http.StatusInternalServerError, "snapshot_failed", err)
	}
	visibleApp, ok := findAppByID(visibleSnapshot.Apps, appID)
	if !ok {
		return empty, newUserAppActionFailure(http.StatusNotFound, "target_unresolved", errors.New("app is not visible to this user"))
	}
	if !visibleApp.RestartAllowedGeneralUser {
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), map[string]interface{}{"app_id": visibleApp.AppID, "reason": "app_not_opted_in", "action": string(action), "via": via})
		return empty, newUserAppActionFailure(http.StatusConflict, "not_opted_in", errors.New("user app controls are not enabled for this app"))
	}
	snapshot, err := a.readOnlySnapshot(ctx)
	if err != nil {
		return empty, newUserAppActionFailure(http.StatusInternalServerError, "snapshot_failed", err)
	}
	app, ok := findAppByID(snapshot.Apps, visibleApp.AppID)
	if !ok {
		return empty, newUserAppActionFailure(http.StatusNotFound, "target_unresolved", errors.New("app is not available in the current snapshot"))
	}
	if action == docker.ActionRestart && app.DockerState == models.DockerExited {
		action = docker.ActionStart
		actionDef, _ = userAppControlActionDefinition(action)
	}
	details := map[string]interface{}{
		"app_id":           app.AppID,
		"requester_id":     user.ID,
		"via":              via,
		"action":           string(action),
		"requested_action": string(requestedAction),
	}
	if !isDockerRepairTarget(app) {
		details["reason"] = "not_docker_target"
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), details)
		return empty, newUserAppActionFailure(http.StatusConflict, "not_actionable", errors.New("this app cannot be controlled by NoobBoard"))
	}
	if a.redactorSnapshot().IsBlacklistedApp(app) {
		details["reason"] = "privacy_blacklisted"
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), details)
		return empty, newUserAppActionFailure(http.StatusConflict, "not_actionable", errors.New("app controls are unavailable for this app"))
	}
	if !app.RestartAllowedGeneralUser {
		details["reason"] = "app_not_opted_in"
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), details)
		return empty, newUserAppActionFailure(http.StatusConflict, "not_opted_in", errors.New("user app controls are not enabled for this app"))
	}
	if err := validateGeneralUserAppActionState(action, app); err != nil {
		details["reason"] = "action_state_refused"
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), details)
		return empty, newUserAppActionFailure(http.StatusConflict, "not_actionable", err)
	}
	reviewDecision, reviewEnabled, err := a.reviewAgentAction(ctx, user, snapshot, app, actionDef, via)
	if reviewEnabled {
		details["auto_review_allow"] = reviewDecision.Allow
		details["auto_review_confidence"] = reviewDecision.Confidence
		details["auto_review_summary"] = reviewDecision.Summary
	}
	if err != nil {
		details["reason"] = "auto_review_refused"
		details["error"] = err.Error()
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "auto_review_refused"), auditDetailsCopy(details))
		return empty, newUserAppActionFailure(http.StatusConflict, "auto_review_refused", err)
	}
	limit := a.reserveAgentRepair(app.AppID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "rate_limited"), auditDetailsCopy(details))
		return empty, newUserAppActionFailure(http.StatusConflict, "approval_rate_limited", errors.New(limit.Message))
	}
	result, err := a.deps.Collectors.Docker.ControlContainer(ctx, app, action)
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "execute_failed"), auditDetailsCopy(details))
		return empty, newUserAppActionFailure(http.StatusBadGateway, "auto_execute_failed", err)
	}
	details["container_name"] = app.ContainerName
	a.invalidateSnapshot()
	a.deps.Audit.Record(user.ID, userAppActionAudit(action, "executed"), auditDetailsCopy(details))
	a.deps.Audit.Record(user.ID, "app.container.action", map[string]interface{}{"app_id": app.AppID, "action": string(action), "container_name": app.ContainerName, "via": via})
	outcome := a.verifyRepairOutcome(ctx, app, actionDef, result, outcomeLabel)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "verified"), verifyDetails)
	} else {
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "verify_failed"), verifyDetails)
	}
	return userAppActionExecution{Result: result, Outcome: outcome}, nil
}

func (a *App) executeUserAgentAction(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		ExecutionToken string `json:"execution_token"`
		Choice         string `json:"choice"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	choice := strings.TrimSpace(body.Choice)
	if choice != "start_array" {
		writeError(w, http.StatusBadRequest, errors.New("unsupported LLM action choice"))
		return
	}
	user := mustUser(r)
	payload, err := a.verifyAgentApprovalToken(strings.TrimSpace(body.ExecutionToken), user.ID)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if payload.RecommendedActionID != arrayStartActionID || payload.TargetKind != "storage" || payload.TargetID != arrayTargetID {
		writeError(w, http.StatusBadRequest, errors.New("execution token is not valid for starting the array"))
		return
	}
	if strings.TrimSpace(payload.Nonce) == "" {
		writeError(w, http.StatusForbidden, errors.New("execution token is missing a replay nonce"))
		return
	}
	a.settingsMu.RLock()
	visibility := a.deps.Config.Visibility
	role := compactDiagnosisRole(user.Role, visibility.DefaultRole)
	allowed := roleCanUseLLM(visibility, role)
	a.settingsMu.RUnlock()
	if !allowed {
		a.deps.Audit.Record(user.ID, "llm.array_start.refused", map[string]interface{}{"reason": "llm_disabled_for_role", "via": "general_user_llm"})
		writeError(w, http.StatusForbidden, errors.New("status chat is disabled for this role"))
		return
	}
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	details := map[string]interface{}{
		"recommended_action_id": payload.RecommendedActionID,
		"target_kind":           payload.TargetKind,
		"target_id":             payload.TargetID,
		"requester_id":          user.ID,
		"via":                   "general_user_llm",
		"array_state":           snapshot.Infrastructure.UnraidArrayState,
		"can_execute":           false,
	}
	if !arrayStartNeeded(snapshot.Infrastructure) {
		details["reason"] = "array_not_stopped"
		a.deps.Audit.Record(user.ID, "llm.array_start.refused", details)
		writeError(w, http.StatusConflict, errors.New("the array is not currently stopped"))
		return
	}
	if !a.consumeAgentApproval(payload) {
		details["reason"] = "execution_replay"
		a.deps.Audit.Record(user.ID, "llm.array_start.replay_blocked", details)
		writeError(w, http.StatusConflict, errors.New("execution token has already been used"))
		return
	}
	limit := a.reserveAgentRepair(arrayTargetID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(user.ID, "llm.array_start.rate_limited", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, errors.New(limit.Message))
		return
	}
	details["can_execute"] = true
	a.deps.Audit.Record(user.ID, "llm.array_start.approved", auditDetailsCopy(details))
	result, err := a.deps.Collectors.Unraid.StartArray(r.Context())
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(user.ID, "llm.array_start.execute_failed", auditDetailsCopy(details))
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.invalidateSnapshot()
	a.deps.Audit.Record(user.ID, "llm.array_start.executed", auditDetailsCopy(details))
	a.deps.Audit.Record(user.ID, "infra.unraid_array.action", map[string]interface{}{"action": "start_array", "via": "general_user_llm", "recommended_action_id": payload.RecommendedActionID})
	outcome := a.verifyArrayStartOutcome(r.Context(), snapshot.Infrastructure, result)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(user.ID, "llm.array_start.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(user.ID, "llm.array_start.verify_failed", verifyDetails)
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "executed",
		"result":  result,
		"outcome": outcome,
	})
}

func validateGeneralUserAppActionState(action docker.ContainerAction, app models.AppStatus) error {
	status := currentStatusOrUnknown(app.CurrentStatus)
	switch action {
	case docker.ActionStart:
		if app.DockerState == models.DockerRunning || status == models.StatusOnline || status == models.StatusDegraded {
			return errors.New("this app is already running")
		}
	case docker.ActionStop:
		if app.DockerState == models.DockerExited || (status == models.StatusOffline && app.DockerState != models.DockerRunning) {
			return errors.New("this app is already stopped")
		}
	case docker.ActionRestart:
	default:
		return errors.New("app action is not supported")
	}
	return nil
}

func preferredGeneralUserRepairAction(app models.AppStatus) docker.ContainerAction {
	if app.DockerState == models.DockerExited {
		return docker.ActionStart
	}
	return docker.ActionRestart
}

func userAppActionAudit(action docker.ContainerAction, suffix string) string {
	actionName := strings.TrimSpace(string(action))
	if actionName == "" {
		actionName = "unknown"
	}
	return "user.app." + actionName + "." + suffix
}

func dockerActionDisplayName(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "Start"
	case docker.ActionStop:
		return "Stop"
	case docker.ActionRestart:
		return "Restart"
	default:
		return "Action"
	}
}

func dockerActionPastTense(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "started"
	case docker.ActionStop:
		return "stopped"
	case docker.ActionRestart:
		return "restarted"
	default:
		return "sent"
	}
}

func dockerActionSuccessPhrase(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "started - running"
	case docker.ActionStop:
		return "stopped - stopped"
	case docker.ActionRestart:
		return "restarted - recovered"
	default:
		return "completed"
	}
}

func dockerActionWaitingPhrase(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "started - waiting for the app to report online"
	case docker.ActionStop:
		return "stopped - waiting for the app to stop"
	case docker.ActionRestart:
		return "restarted - waiting for recovery"
	default:
		return "sent - waiting for status"
	}
}

func dockerActionFinalWaitingPhrase(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "started - still coming up or not responding"
	case docker.ActionStop:
		return "stopped - still appears to be running"
	case docker.ActionRestart:
		return "restarted - still coming up or not responding"
	default:
		return "sent - status did not settle"
	}
}

func dockerActionReachedExpectedState(action docker.ContainerAction, app models.AppStatus) bool {
	status := currentStatusOrUnknown(app.CurrentStatus)
	switch action {
	case docker.ActionStart, docker.ActionRestart:
		return status == models.StatusOnline
	case docker.ActionStop:
		return app.DockerState == models.DockerExited || status == models.StatusOffline
	default:
		return false
	}
}

func (a *App) adminRepairRequests(w http.ResponseWriter, _ *http.Request) {
	requests, err := a.deps.Store.RepairRequests()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, repairRequestsNewestFirst(requests))
}

func (a *App) decideRepairRequest(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	requestID := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Choice string `json:"choice"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	choice := strings.TrimSpace(body.Choice)
	if choice != "approve" && choice != "deny" {
		writeError(w, http.StatusBadRequest, errors.New("choice must be approve or deny"))
		return
	}
	request, err := a.deps.Store.RepairRequestByID(requestID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, db.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	if request.Status != models.RepairRequestPending {
		writeError(w, http.StatusConflict, errors.New("repair request is no longer pending"))
		return
	}
	if choice == "deny" {
		resolved, err := a.resolveRepairRequest(r.Context(), request, models.RepairRequestDenied, mustUser(r).ID, strings.TrimSpace(body.Note), nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.denied", map[string]interface{}{"request_id": request.ID, "app_id": request.AppID, "requester_id": request.RequesterID})
		writeJSON(w, http.StatusAccepted, map[string]interface{}{"status": "denied", "request": resolved})
		return
	}
	a.approveRepairRequest(w, r, request)
}

func (a *App) approveRepairRequest(w http.ResponseWriter, r *http.Request, request models.RepairRequest) {
	action, ok := agentActionDefinition(request.ActionID)
	if !ok || !action.Executable || action.DockerAction != docker.ActionRestart {
		if _, err := a.resolveRepairRequest(r.Context(), request, models.RepairRequestFailed, mustUser(r).ID, "Unsupported repair action.", nil); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeError(w, http.StatusConflict, errors.New("repair request action is not executable"))
		return
	}
	a.settingsMu.RLock()
	llmCfg := a.deps.Config.LLM
	a.settingsMu.RUnlock()
	details := map[string]interface{}{
		"request_id":            request.ID,
		"requester_id":          request.RequesterID,
		"choice":                "approve",
		"recommended_action_id": action.ID,
		"target_kind":           "app",
		"target_id":             request.AppID,
		"can_execute":           false,
	}
	if !llmCfg.AgentControlEnabled {
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.control_disabled", details)
		writeError(w, http.StatusConflict, errors.New("admin-approved app fixes are disabled in LLM settings"))
		return
	}
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, request.AppID)
	if !ok {
		details["reason"] = "target_unresolved"
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.refused", details)
		writeError(w, http.StatusConflict, errors.New("repair request target is no longer present in the current app snapshot"))
		return
	}
	details["app_id"] = app.AppID
	details["container_name"] = app.ContainerName
	executionAction := agentRepairActionForApp(action, app)
	details["docker_action"] = string(executionAction.DockerAction)
	if a.redactorSnapshot().IsBlacklistedApp(app) {
		details["reason"] = "privacy_blacklisted"
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.refused", details)
		writeError(w, http.StatusConflict, errors.New("app fixes are unavailable for privacy-blacklisted apps"))
		return
	}
	if !app.AgentRepairAllowed {
		details["reason"] = "app_not_opted_in"
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.refused", details)
		writeError(w, http.StatusConflict, errors.New("admin/AI app fix is not enabled for this app"))
		return
	}
	reviewDecision, reviewEnabled, err := a.reviewAgentAction(r.Context(), mustUser(r), snapshot, app, executionAction, "repair_request")
	if reviewEnabled {
		details["auto_review_allow"] = reviewDecision.Allow
		details["auto_review_confidence"] = reviewDecision.Confidence
		details["auto_review_summary"] = reviewDecision.Summary
	}
	if err != nil {
		details["reason"] = "auto_review_refused"
		details["error"] = err.Error()
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.auto_review_refused", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, err)
		return
	}
	limit := a.reserveAgentRepair(app.AppID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.rate_limited", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, errors.New(limit.Message))
		return
	}
	details["can_execute"] = true
	a.deps.Audit.Record(mustUser(r).ID, "repair_request.approved", auditDetailsCopy(details))
	result, err := a.deps.Collectors.Docker.ControlContainer(r.Context(), app, executionAction.DockerAction)
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.execute_failed", auditDetailsCopy(details))
		if _, resolveErr := a.resolveRepairRequest(r.Context(), request, models.RepairRequestFailed, mustUser(r).ID, err.Error(), nil); resolveErr != nil {
			writeError(w, http.StatusInternalServerError, resolveErr)
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	details["via"] = "repair_request"
	a.invalidateSnapshot()
	a.deps.Audit.Record(mustUser(r).ID, "repair_request.executed", auditDetailsCopy(details))
	a.deps.Audit.Record(mustUser(r).ID, "app.container.action", map[string]interface{}{"app_id": app.AppID, "action": string(executionAction.DockerAction), "container_name": app.ContainerName, "via": "repair_request", "request_id": request.ID, "recommended_action_id": action.ID})
	outcome := a.verifyAgentRepairOutcome(r.Context(), app, executionAction, result)
	repairOutcome := repairRequestOutcomeFromAgent(outcome)
	resolved, err := a.resolveRepairRequest(r.Context(), request, models.RepairRequestExecuted, mustUser(r).ID, outcome.Message, repairOutcome)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.verify_failed", verifyDetails)
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "executed",
		"request": resolved,
		"result":  result,
		"outcome": outcome,
	})
}

func (a *App) pendingRepairRequestFor(userID, appID, actionID string) (models.RepairRequest, bool, error) {
	requests, err := a.deps.Store.RepairRequestsForUser(userID)
	if err != nil {
		return models.RepairRequest{}, false, err
	}
	for _, request := range requests {
		if request.Status == models.RepairRequestPending && request.AppID == appID && request.ActionID == actionID {
			return request, true, nil
		}
	}
	return models.RepairRequest{}, false, nil
}

func (a *App) resolveRepairRequest(ctx context.Context, request models.RepairRequest, status models.RepairRequestStatus, actorID, note string, outcome *models.RepairRequestOutcome) (models.RepairRequest, error) {
	now := time.Now().UTC()
	request.Status = status
	request.ResolvedAt = &now
	request.ResolvedBy = actorID
	request.ResolutionNote = strings.TrimSpace(note)
	request.Outcome = outcome
	if err := a.deps.Store.UpsertRepairRequest(request); err != nil {
		return request, err
	}
	subject := "Repair request updated"
	body := request.ResolutionNote
	if body == "" && outcome != nil {
		body = outcome.Message
	}
	if body == "" {
		body = "An admin reviewed your request."
	}
	if err := a.deps.Notifications.NotifyUser(ctx, request.RequesterID, subject, body, request.AppID, "repair-request:"+request.ID+":resolved"); err != nil {
		a.deps.Audit.Record(actorID, "repair_request.notify_user_failed", map[string]interface{}{"request_id": request.ID, "requester_id": request.RequesterID, "app_id": request.AppID, "error": err.Error()})
	}
	return request, nil
}

func repairRequestOutcomeFromAgent(outcome llmAgentRepairOutcomeView) *models.RepairRequestOutcome {
	return &models.RepairRequestOutcome{
		Verified:       outcome.Verified,
		Recovered:      outcome.Recovered,
		BeforeStatus:   outcome.BeforeStatus,
		AfterStatus:    outcome.AfterStatus,
		Message:        outcome.Message,
		HistoryEventID: outcome.HistoryEventID,
		CheckedAt:      outcome.CheckedAt,
	}
}

func repairRequestsNewestFirst(requests []models.RepairRequest) []models.RepairRequest {
	out := append([]models.RepairRequest(nil), requests...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func isDockerRepairTarget(app models.AppStatus) bool {
	return strings.TrimSpace(app.ContainerID) != "" || strings.TrimSpace(app.ContainerName) != "" || app.DockerState != models.DockerUnknown || app.DataSource == "unraid-docker"
}

func compactAppSummaryForRequest(app models.AppStatus) string {
	summary := strings.TrimSpace(app.ServerSummary)
	if summary != "" {
		return summary
	}
	status := currentStatusOrUnknown(app.CurrentStatus)
	label := firstNonEmpty(app.DisplayName, app.AppID, "This app")
	if status == models.StatusOnline {
		return label + " was reported as working, but a user requested help."
	}
	return fmt.Sprintf("%s is %s.", label, status)
}

func (a *App) getNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := a.deps.Notifications.Preferences(mustUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (a *App) userNotifications(w http.ResponseWriter, r *http.Request) {
	records, err := a.deps.Store.NotificationsForUser(mustUser(r).ID, parseNotificationLimit(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for i := range records {
		records[i].Message = a.deps.Redactor.RedactString(records[i].Message).Text
	}
	writeJSON(w, http.StatusOK, records)
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
	snapshot, err := a.latestSnapshot(r.Context(), models.RoleAdmin)
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

func (a *App) getLLMSettings(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.RLock()
	cfg := a.deps.Config.LLM
	a.settingsMu.RUnlock()
	writeJSON(w, http.StatusOK, llmSettingsResponse(cfg, mustSession(r)))
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
	a.invalidateSnapshot()
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
	a.invalidateSnapshot()
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
	a.invalidateSnapshot()
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
	var err error
	settings, err = normalizeAppCatalogSettings(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.Lock()
	a.deps.Config.AppCatalog = settings
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.invalidateSnapshot()
	a.deps.Audit.Record(mustUser(r).ID, "settings.apps.saved", map[string]interface{}{"path": r.URL.Path, "icon_overrides": len(settings.IconOverrides), "agent_repair_allowed": len(settings.AgentRepairAllowed), "general_user_restarts_enabled": settings.GeneralUserRestartsEnabled, "general_user_auto_repair_enabled": settings.GeneralUserAutoRepairEnabled, "restart_allowed_general_user": len(settings.RestartAllowedGeneralUser)})
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
	a.invalidateSnapshot()
	a.deps.Audit.Record(mustUser(r).ID, "settings.llm.saved", map[string]interface{}{"path": r.URL.Path, "provider": settings.Provider})
	if chatGPTAuthPresent(current) && !chatGPTAuthPresent(settings) {
		a.deps.Audit.Record(mustUser(r).ID, "settings.llm.chatgpt.cleared", map[string]interface{}{"path": r.URL.Path})
	}
	writeJSON(w, http.StatusOK, llmSettingsResponse(settings, mustSession(r)))
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
	a.invalidateSnapshot()
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
	a.invalidateSnapshot()
	a.deps.Audit.Record(mustUser(r).ID, "settings.notifications.saved", map[string]interface{}{"path": r.URL.Path, "enabled": settings.Enabled})
	writeJSON(w, http.StatusOK, settings)
}

type llmSettingsView struct {
	Enabled                        bool                        `json:"enabled"`
	Provider                       string                      `json:"provider"`
	OpenAIAuthMethod               string                      `json:"openai_auth_method"`
	OpenAIModel                    string                      `json:"openai_model"`
	OpenAIAPIKeySet                bool                        `json:"openai_api_key_set"`
	ChatGPTConnected               bool                        `json:"chatgpt_connected"`
	ChatGPTAccessTokenSet          bool                        `json:"chatgpt_access_token_set"`
	ChatGPTAccountIDSet            bool                        `json:"chatgpt_account_id_set"`
	AnthropicModel                 string                      `json:"anthropic_model"`
	AnthropicAPIKeySet             bool                        `json:"anthropic_api_key_set"`
	Timeout                        time.Duration               `json:"timeout"`
	AgentControlEnabled            bool                        `json:"agent_control_enabled"`
	AgentAutoRepairEnabled         bool                        `json:"agent_auto_repair_enabled"`
	AgentArmDuration               time.Duration               `json:"agent_arm_duration"`
	ActionAutoReviewEnabled        bool                        `json:"action_auto_review_enabled"`
	ActionAutoReviewModel          string                      `json:"action_auto_review_model"`
	ActionAutoReviewReasoning      string                      `json:"action_auto_review_reasoning"`
	ActionAutoReviewReferencePaths []string                    `json:"action_auto_review_reference_paths"`
	Policies                       map[string]models.LLMPolicy `json:"policies"`
	AgentReadiness                 llmAgentReadinessView       `json:"agent_readiness"`
}

type llmAgentReadinessView struct {
	ReadOnlyToolsAvailable bool                         `json:"read_only_tools_available"`
	MutatingToolsAvailable bool                         `json:"mutating_tools_available"`
	AgentControlEnabled    bool                         `json:"agent_control_enabled"`
	AgentAutoRepairEnabled bool                         `json:"agent_auto_repair_enabled"`
	AgentArmed             bool                         `json:"agent_armed"`
	AgentArmedUntil        time.Time                    `json:"agent_armed_until,omitempty"`
	AgentArmDuration       time.Duration                `json:"agent_arm_duration"`
	RepairCooldown         time.Duration                `json:"repair_cooldown"`
	RepairRateLimitWindow  time.Duration                `json:"repair_rate_limit_window"`
	RepairRateLimitMax     int                          `json:"repair_rate_limit_max"`
	AdminToolsEnabled      bool                         `json:"admin_tools_enabled"`
	AdminToolCallLimit     int                          `json:"admin_tool_call_limit"`
	ReadOnlyTools          []llmAgentToolView           `json:"read_only_tools"`
	ReviewModes            []llmAgentReviewModeView     `json:"review_modes"`
	OpenCodeAutoReview     llmOpenCodeAutoReviewSummary `json:"opencode_auto_review"`
}

type llmAgentToolView struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Access      string `json:"access"`
	Mutating    bool   `json:"mutating"`
	Description string `json:"description"`
}

type llmAgentReviewModeView struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

type llmOpenCodeAutoReviewSummary struct {
	ReferenceReviewed   bool   `json:"reference_reviewed"`
	SufficientReference bool   `json:"sufficient_reference"`
	Enabled             bool   `json:"enabled"`
	Model               string `json:"model"`
	Reasoning           string `json:"reasoning,omitempty"`
	ReferenceCount      int    `json:"reference_count"`
	ModelFinding        string `json:"model_finding"`
	DesignFinding       string `json:"design_finding"`
}

type diagnosisResponse struct {
	llm.Diagnosis
	AgentPlan *llmAgentPlanView `json:"agent_plan,omitempty"`
}

type llmAgentPlanView struct {
	ID                    string                     `json:"id"`
	Title                 string                     `json:"title"`
	Summary               string                     `json:"summary"`
	RecommendedActionID   string                     `json:"recommended_action_id"`
	DirectAction          string                     `json:"direct_action,omitempty"`
	ActionKnown           bool                       `json:"action_known"`
	ApprovalToken         string                     `json:"approval_token"`
	ExecutionToken        string                     `json:"execution_token,omitempty"`
	ApprovalExpiresAt     time.Time                  `json:"approval_expires_at"`
	RequiresAdminApproval bool                       `json:"requires_admin_approval"`
	CanExecute            bool                       `json:"can_execute"`
	CanRequestRepair      bool                       `json:"can_request_repair"`
	RepairCooldownSeconds int                        `json:"repair_cooldown_seconds,omitempty"`
	RetryAfterSeconds     int                        `json:"retry_after_seconds,omitempty"`
	RetryAt               *time.Time                 `json:"retry_at,omitempty"`
	RateLimitReason       string                     `json:"rate_limit_reason,omitempty"`
	AutoRepairAttempted   bool                       `json:"auto_repair_attempted,omitempty"`
	AutoExecuted          bool                       `json:"auto_executed,omitempty"`
	AutoRepairMessage     string                     `json:"auto_repair_message,omitempty"`
	Status                string                     `json:"status"`
	Target                llmAgentPlanTargetView     `json:"target"`
	Options               []llmAgentPlanOptionView   `json:"options"`
	Outcome               *llmAgentRepairOutcomeView `json:"outcome,omitempty"`
}

type llmAgentPlanTargetView struct {
	Kind     string `json:"kind"`
	ID       string `json:"id,omitempty"`
	Label    string `json:"label,omitempty"`
	Query    string `json:"query,omitempty"`
	Resolved bool   `json:"resolved"`
	Reason   string `json:"reason,omitempty"`
}

type llmAgentPlanOptionView struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Selected    bool   `json:"selected,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type llmAgentRepairOutcomeView struct {
	Action         string               `json:"action"`
	TargetID       string               `json:"target_id"`
	TargetLabel    string               `json:"target_label"`
	BeforeStatus   models.CurrentStatus `json:"before_status"`
	AfterStatus    models.CurrentStatus `json:"after_status"`
	Recovered      bool                 `json:"recovered"`
	Verified       bool                 `json:"verified"`
	CheckedAt      time.Time            `json:"checked_at"`
	Message        string               `json:"message"`
	ResultStatus   string               `json:"result_status,omitempty"`
	HistoryEventID string               `json:"history_event_id,omitempty"`
	HistoryError   string               `json:"history_error,omitempty"`
}

type agentApprovalTokenPayload struct {
	PlanID              string `json:"plan_id"`
	ActorID             string `json:"actor_id"`
	RecommendedActionID string `json:"recommended_action_id"`
	TargetKind          string `json:"target_kind,omitempty"`
	TargetID            string `json:"target_id,omitempty"`
	Nonce               string `json:"nonce,omitempty"`
	ExpiresAt           int64  `json:"expires_at"`
}

type llmSettingsUpdate struct {
	Enabled                        *bool                       `json:"enabled"`
	Provider                       *string                     `json:"provider"`
	OpenAIAuthMethod               *string                     `json:"openai_auth_method"`
	OpenAIModel                    *string                     `json:"openai_model"`
	OpenAIAPIKey                   *string                     `json:"openai_api_key"`
	ClearOpenAIAPIKey              bool                        `json:"clear_openai_api_key"`
	ClearChatGPTAuth               bool                        `json:"clear_chatgpt_auth"`
	AnthropicModel                 *string                     `json:"anthropic_model"`
	AnthropicAPIKey                *string                     `json:"anthropic_api_key"`
	ClearAnthropicAPIKey           bool                        `json:"clear_anthropic_api_key"`
	Timeout                        *time.Duration              `json:"timeout"`
	AgentControlEnabled            *bool                       `json:"agent_control_enabled"`
	AgentAutoRepairEnabled         *bool                       `json:"agent_auto_repair_enabled"`
	AgentArmDuration               *time.Duration              `json:"agent_arm_duration"`
	ActionAutoReviewEnabled        *bool                       `json:"action_auto_review_enabled"`
	ActionAutoReviewModel          *string                     `json:"action_auto_review_model"`
	ActionAutoReviewReasoning      *string                     `json:"action_auto_review_reasoning"`
	ActionAutoReviewReferencePaths []string                    `json:"action_auto_review_reference_paths"`
	Policies                       map[string]models.LLMPolicy `json:"policies"`
}

type integrationSettingsView struct {
	Mode                string `json:"mode"`
	UnraidBaseURL       string `json:"unraid_base_url"`
	UnraidAPIKeySet     bool   `json:"unraid_api_key_set"`
	UnraidAPIKeyFile    string `json:"unraid_api_key_file,omitempty"`
	UnraidSSHFallback   bool   `json:"unraid_ssh_fallback"`
	UnraidSSHHost       string `json:"unraid_ssh_host,omitempty"`
	UnraidSSHPort       int    `json:"unraid_ssh_port"`
	UnraidSSHUser       string `json:"unraid_ssh_user,omitempty"`
	UnraidSSHKeyFile    string `json:"unraid_ssh_key_file,omitempty"`
	UnraidSSHCommand    string `json:"unraid_ssh_command,omitempty"`
	UniFiBaseURL        string `json:"unifi_base_url"`
	UniFiAPIKeySet      bool   `json:"unifi_api_key_set"`
	UniFiAPIKeyFile     string `json:"unifi_api_key_file,omitempty"`
	UniFiSiteID         string `json:"unifi_site_id"`
	UniFiInsecureTLS    bool   `json:"unifi_insecure_tls"`
	UniFiNASClientHint  string `json:"unifi_nas_client_hint,omitempty"`
	ExpectedNASLinkMbps int    `json:"expected_nas_link_mbps,omitempty"`
	InternetProbeURL    string `json:"internet_probe_url"`
	DNSProbeHost        string `json:"dns_probe_host"`
	RouterProbeTarget   string `json:"router_probe_target"`
	NASProbeTarget      string `json:"nas_probe_target"`
}

type integrationSettingsUpdate struct {
	Mode                *string `json:"mode"`
	UnraidBaseURL       *string `json:"unraid_base_url"`
	UnraidAPIKey        *string `json:"unraid_api_key"`
	ClearUnraidAPIKey   bool    `json:"clear_unraid_api_key"`
	UnraidAPIKeyFile    *string `json:"unraid_api_key_file"`
	UnraidSSHFallback   *bool   `json:"unraid_ssh_fallback"`
	UnraidSSHHost       *string `json:"unraid_ssh_host"`
	UnraidSSHPort       *int    `json:"unraid_ssh_port"`
	UnraidSSHUser       *string `json:"unraid_ssh_user"`
	UnraidSSHKeyFile    *string `json:"unraid_ssh_key_file"`
	UnraidSSHCommand    *string `json:"unraid_ssh_command"`
	UniFiBaseURL        *string `json:"unifi_base_url"`
	UniFiAPIKey         *string `json:"unifi_api_key"`
	ClearUniFiAPIKey    bool    `json:"clear_unifi_api_key"`
	UniFiAPIKeyFile     *string `json:"unifi_api_key_file"`
	UniFiSiteID         *string `json:"unifi_site_id"`
	UniFiInsecureTLS    *bool   `json:"unifi_insecure_tls"`
	UniFiNASClientHint  *string `json:"unifi_nas_client_hint"`
	ExpectedNASLinkMbps *int    `json:"expected_nas_link_mbps"`
	InternetProbeURL    *string `json:"internet_probe_url"`
	DNSProbeHost        *string `json:"dns_probe_host"`
	RouterProbeTarget   *string `json:"router_probe_target"`
	NASProbeTarget      *string `json:"nas_probe_target"`
}

func llmSettingsResponse(cfg config.LLMConfig, sess session) llmSettingsView {
	return llmSettingsView{
		Enabled:                        cfg.Enabled,
		Provider:                       cfg.Provider,
		OpenAIAuthMethod:               firstNonEmpty(strings.TrimSpace(cfg.OpenAIAuthMethod), config.OpenAIAuthMethodAPIKey),
		OpenAIModel:                    cfg.OpenAIModel,
		OpenAIAPIKeySet:                strings.TrimSpace(cfg.OpenAIAPIKey) != "",
		ChatGPTConnected:               strings.TrimSpace(cfg.ChatGPTRefreshToken) != "" && strings.TrimSpace(cfg.ChatGPTAccountID) != "",
		ChatGPTAccessTokenSet:          strings.TrimSpace(cfg.ChatGPTAccessToken) != "",
		ChatGPTAccountIDSet:            strings.TrimSpace(cfg.ChatGPTAccountID) != "",
		AnthropicModel:                 cfg.AnthropicModel,
		AnthropicAPIKeySet:             strings.TrimSpace(cfg.AnthropicAPIKey) != "",
		Timeout:                        cfg.Timeout,
		AgentControlEnabled:            cfg.AgentControlEnabled,
		AgentAutoRepairEnabled:         cfg.AgentAutoRepairEnabled,
		AgentArmDuration:               cfg.AgentArmDuration,
		ActionAutoReviewEnabled:        cfg.ActionAutoReviewEnabled,
		ActionAutoReviewModel:          firstNonEmpty(strings.TrimSpace(cfg.ActionAutoReviewModel), "same"),
		ActionAutoReviewReasoning:      cfg.ActionAutoReviewReasoning,
		ActionAutoReviewReferencePaths: append([]string(nil), cfg.ActionAutoReviewReferencePaths...),
		Policies:                       cfg.Policies,
		AgentReadiness:                 llmAgentReadinessResponse(cfg, sess),
	}
}

func llmAgentReadinessResponse(cfg config.LLMConfig, sess session) llmAgentReadinessView {
	adminPolicy := cfg.Policies["admin_requested"]
	armed, armedUntil := agentSessionArmed(cfg, sess)
	tools := make([]llmAgentToolView, 0, len(llm.ReadOnlyAgentToolNames()))
	for _, name := range llm.ReadOnlyAgentToolNames() {
		tools = append(tools, llmAgentToolView{
			Name:        name,
			Label:       llmAgentToolLabel(name),
			Access:      "admin",
			Mutating:    false,
			Description: "Refreshes sanitized NoobBoard status through the normal collectors.",
		})
	}
	return llmAgentReadinessView{
		ReadOnlyToolsAvailable: true,
		MutatingToolsAvailable: true,
		AgentControlEnabled:    cfg.AgentControlEnabled,
		AgentAutoRepairEnabled: cfg.ActionAutoReviewEnabled,
		AgentArmed:             armed,
		AgentArmedUntil:        armedUntil,
		AgentArmDuration:       cfg.AgentArmDuration,
		RepairCooldown:         agentRepairPerAppCooldown,
		RepairRateLimitWindow:  agentRepairGlobalWindow,
		RepairRateLimitMax:     agentRepairGlobalLimit,
		AdminToolsEnabled:      adminPolicy.AgentToolsEnabled && adminPolicy.RecipientRole == models.RoleAdmin,
		AdminToolCallLimit:     adminPolicy.AgentMaxToolCalls,
		ReadOnlyTools:          tools,
		ReviewModes: []llmAgentReviewModeView{
			{
				ID:          "read_only",
				Label:       "Read-only diagnosis",
				Status:      "available",
				Enabled:     adminPolicy.AgentToolsEnabled && adminPolicy.RecipientRole == models.RoleAdmin,
				Description: "The model may refresh sanitized live status but cannot execute repairs.",
			},
			{
				ID:          "propose",
				Label:       "Approval popup",
				Status:      agentProposeModeStatus(cfg.AgentControlEnabled),
				Enabled:     cfg.AgentControlEnabled,
				Description: "The model can propose one allowlisted app fix; NoobBoard starts stopped apps or restarts non-stopped apps only after per-app opt-in and admin approval.",
			},
			{
				ID:          "auto_review",
				Label:       "Auto-review",
				Status:      actionAutoReviewStatus(cfg),
				Enabled:     cfg.ActionAutoReviewEnabled,
				Description: "A separate reviewer model can validate proposed actions against configured references before execution.",
			},
			{
				ID:          "auto_action",
				Label:       "Auto action",
				Status:      agentAutoActionStatus(cfg),
				Enabled:     cfg.AgentControlEnabled && cfg.ActionAutoReviewEnabled,
				Description: "When a chat auto-fix toggle is enabled, NoobBoard may run one reviewer-approved app start/restart for a non-online opted-in app without opening the approval popup.",
			},
		},
		OpenCodeAutoReview: llmOpenCodeAutoReviewSummary{
			ReferenceReviewed:   true,
			SufficientReference: true,
			Enabled:             cfg.ActionAutoReviewEnabled,
			Model:               firstNonEmpty(strings.TrimSpace(cfg.ActionAutoReviewModel), "same"),
			Reasoning:           strings.TrimSpace(cfg.ActionAutoReviewReasoning),
			ReferenceCount:      len(cfg.ActionAutoReviewReferencePaths),
			ModelFinding:        "The OpenCode reference uses a configurable reviewer model and prefers cross-model review when auto-selecting.",
			DesignFinding:       "NoobBoard uses the same idea as a fail-closed action gate for approval and explicitly requested autonomous app repair.",
		},
	}
}

func agentAutoActionStatus(cfg config.LLMConfig) string {
	if !cfg.AgentControlEnabled {
		return "locked"
	}
	if !cfg.ActionAutoReviewEnabled {
		return "review_required"
	}
	return "available"
}

func actionAutoReviewStatus(cfg config.LLMConfig) string {
	if cfg.ActionAutoReviewEnabled {
		return "available"
	}
	return "planned"
}

func agentSessionArmed(cfg config.LLMConfig, sess session) (bool, time.Time) {
	armedUntil := sess.AgentArmedUntil.UTC()
	armed := cfg.AgentControlEnabled && !armedUntil.IsZero() && time.Now().UTC().Before(armedUntil)
	if !armed {
		return false, time.Time{}
	}
	return true, armedUntil
}

func agentProposeModeStatus(controlEnabled bool) string {
	if !controlEnabled {
		return "locked"
	}
	return "available"
}

func llmAgentToolLabel(name string) string {
	switch name {
	case "noobboard_current_status":
		return "Current status"
	case "noobboard_server_status":
		return "Server status"
	case "noobboard_network_status":
		return "Network status"
	case "noobboard_app_status":
		return "App status"
	default:
		return strings.ReplaceAll(name, "_", " ")
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
	if update.AgentControlEnabled != nil {
		settings.AgentControlEnabled = *update.AgentControlEnabled
	}
	if update.AgentAutoRepairEnabled != nil {
		settings.AgentAutoRepairEnabled = *update.AgentAutoRepairEnabled
	}
	if update.AgentArmDuration != nil {
		settings.AgentArmDuration = *update.AgentArmDuration
	}
	if update.ActionAutoReviewEnabled != nil {
		settings.ActionAutoReviewEnabled = *update.ActionAutoReviewEnabled
		if update.AgentAutoRepairEnabled == nil {
			settings.AgentAutoRepairEnabled = *update.ActionAutoReviewEnabled
		}
	}
	if update.ActionAutoReviewModel != nil {
		settings.ActionAutoReviewModel = *update.ActionAutoReviewModel
	}
	if update.ActionAutoReviewReasoning != nil {
		settings.ActionAutoReviewReasoning = *update.ActionAutoReviewReasoning
	}
	if update.ActionAutoReviewReferencePaths != nil {
		settings.ActionAutoReviewReferencePaths = append([]string(nil), update.ActionAutoReviewReferencePaths...)
	}
	if update.Policies != nil {
		settings.Policies = update.Policies
	}
	return settings, nil
}

func integrationSettingsResponse(cfg config.IntegrationConfig) integrationSettingsView {
	return integrationSettingsView{
		Mode:                cfg.Mode,
		UnraidBaseURL:       cfg.UnraidBaseURL,
		UnraidAPIKeySet:     strings.TrimSpace(cfg.UnraidAPIKey) != "",
		UnraidAPIKeyFile:    cfg.UnraidAPIKeyFile,
		UnraidSSHFallback:   cfg.UnraidSSHFallback,
		UnraidSSHHost:       cfg.UnraidSSHHost,
		UnraidSSHPort:       cfg.UnraidSSHPort,
		UnraidSSHUser:       cfg.UnraidSSHUser,
		UnraidSSHKeyFile:    cfg.UnraidSSHKeyFile,
		UnraidSSHCommand:    cfg.UnraidSSHCommand,
		UniFiBaseURL:        cfg.UniFiBaseURL,
		UniFiAPIKeySet:      strings.TrimSpace(cfg.UniFiAPIKey) != "",
		UniFiAPIKeyFile:     cfg.UniFiAPIKeyFile,
		UniFiSiteID:         cfg.UniFiSiteID,
		UniFiInsecureTLS:    cfg.UniFiInsecureTLS,
		UniFiNASClientHint:  cfg.UniFiNASClientHint,
		ExpectedNASLinkMbps: cfg.ExpectedNASLinkMbps,
		InternetProbeURL:    cfg.InternetProbeURL,
		DNSProbeHost:        cfg.DNSProbeHost,
		RouterProbeTarget:   cfg.RouterProbeTarget,
		NASProbeTarget:      cfg.NASProbeTarget,
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
	if update.UniFiNASClientHint != nil {
		settings.UniFiNASClientHint = strings.TrimSpace(*update.UniFiNASClientHint)
	}
	if update.ExpectedNASLinkMbps != nil {
		settings.ExpectedNASLinkMbps = *update.ExpectedNASLinkMbps
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
	settings.UniFiNASClientHint = strings.TrimSpace(settings.UniFiNASClientHint)
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
		settings.UniFiNASClientHint != "" ||
		settings.ExpectedNASLinkMbps != 0 ||
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
	appCatalog, err := normalizeAppCatalogSettings(settings.AppCatalog)
	if err != nil {
		return err
	}
	a.deps.Config.AppCatalog = appCatalog
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

func normalizeAppCatalogSettings(settings config.AppCatalogConfig) (config.AppCatalogConfig, error) {
	iconOverrides := map[string]string{}
	for key, iconURL := range settings.IconOverrides {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		normalized, err := config.NormalizeIconURL(iconURL)
		if err != nil {
			return config.AppCatalogConfig{}, fmt.Errorf("icon override %s: %w", key, err)
		}
		if normalized != "" {
			iconOverrides[trimmedKey] = normalized
		}
	}
	agentRepairAllowed := map[string]bool{}
	for key, allowed := range settings.AgentRepairAllowed {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey != "" && allowed {
			agentRepairAllowed[trimmedKey] = true
		}
	}
	restartAllowedGeneralUser := map[string]bool{}
	for key, allowed := range settings.RestartAllowedGeneralUser {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey != "" && allowed {
			restartAllowedGeneralUser[trimmedKey] = true
		}
	}
	settings.IconOverrides = iconOverrides
	settings.AgentRepairAllowed = agentRepairAllowed
	settings.RestartAllowedGeneralUser = restartAllowedGeneralUser
	return settings, nil
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
			nasHint := firstNonEmpty(cfg.Integrations.UniFiNASClientHint, cfg.Integrations.NASProbeTarget, cfg.Integrations.UnraidBaseURL)
			collectors.UniFi = unifi.NewLiveClient(cfg.Integrations.UniFiBaseURL, cfg.Integrations.UniFiAPIKey, cfg.Integrations.UniFiSiteID, cfg.Integrations.UniFiInsecureTLS, unifi.WithNASLinkMonitoring(nasHint, cfg.Integrations.ExpectedNASLinkMbps))
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

func (c unavailableUnraidClient) StartArray(context.Context) (unraid.ArrayControlResult, error) {
	return unraid.ArrayControlResult{}, errors.New(string(c))
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
	if settings.AgentArmDuration <= 0 {
		settings.AgentArmDuration = defaults.AgentArmDuration
	}
	if settings.AgentArmDuration > time.Hour {
		settings.AgentArmDuration = time.Hour
	}
	settings.ActionAutoReviewModel = strings.TrimSpace(settings.ActionAutoReviewModel)
	if settings.ActionAutoReviewModel == "" {
		settings.ActionAutoReviewModel = defaults.ActionAutoReviewModel
	}
	settings.ActionAutoReviewReasoning = strings.TrimSpace(settings.ActionAutoReviewReasoning)
	settings.ActionAutoReviewReferencePaths = compactStrings(settings.ActionAutoReviewReferencePaths)
	settings.Policies = normalizeLLMPolicies(settings.Policies, defaults.Policies)
	if settings.AgentControlEnabled {
		settings.Policies = enableAdminReadOnlyTools(settings.Policies, defaults.Policies)
	}
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

func enableAdminReadOnlyTools(policies, defaults map[string]models.LLMPolicy) map[string]models.LLMPolicy {
	if policies == nil {
		policies = map[string]models.LLMPolicy{}
	}
	fallback := defaults["admin_requested"]
	policy := policies["admin_requested"]
	if policy.Name == "" {
		policy.Name = fallback.Name
	}
	if policy.RecipientRole == "" {
		policy.RecipientRole = models.RoleAdmin
	}
	if policy.RecipientRole != models.RoleAdmin {
		return policies
	}
	policy.AgentToolsEnabled = true
	if policy.AgentMaxToolCalls <= 0 {
		policy.AgentMaxToolCalls = fallback.AgentMaxToolCalls
	}
	if len(policy.AgentToolRules) == 0 && len(fallback.AgentToolRules) > 0 {
		policy.AgentToolRules = append([]models.LLMAgentToolRule(nil), fallback.AgentToolRules...)
	}
	policies["admin_requested"] = policy
	return policies
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
