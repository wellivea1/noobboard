package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
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
	Metrics       db.MetricStore
	Users         *users.Registry
	Audit         *audit.Auditor
	Notifications *notifications.Manager
	Redactor      *privacy.Redactor
	Diagnostics   diagnostics.RuleEngine
	LLM           llm.Client
	Version       string
}

// App is the whole service: both routers, the snapshot cache, and the state
// that has to outlive a single request.
//
// Each lock below is listed with exactly what it guards. That grouping is the
// point — this struct previously carried five bare mutexes over interleaved
// fields, and the only way to know which one covered a given map was to find a
// caller. Two clusters now own their own locks (probeTracker,
// agentRepairLimiter) and are not reachable from anywhere else.
type App struct {
	deps         Dependencies
	sessions     *sessionStore
	loginLimiter *loginLimiter
	openAIAuth   *openAIAuthStore

	// Rolling probe windows and the latency bucket being filled.
	probes *probeTracker
	// Per-app cooldown and the global hourly repair cap.
	agentRepairs *agentRepairLimiter

	// settingsMu guards deps.Config, deps.LLM, deps.Redactor and
	// deps.Collectors, all of which applyRuntimeSettings replaces at runtime.
	settingsMu             sync.RWMutex
	runtimeIntegrationsSet bool

	// snapshotMu guards the cached snapshot served to every read request.
	snapshotMu        sync.RWMutex
	cachedSnapshot    models.Snapshot
	cachedSnapshotSet bool

	// historyMu serialises status-history writes and their retention sweep.
	historyMu        sync.Mutex
	historyRecorder  *statushistory.Recorder
	lastHistoryPrune time.Time

	// agentApprovalMu guards the replay guard for approval tokens. The secret
	// is written once at construction and only read afterwards.
	agentApprovalSecret    []byte
	agentApprovalMu        sync.Mutex
	consumedAgentApprovals map[string]time.Time
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
	// Enough events to recognise a flap; the rule only needs to know whether the
	// count crosses a small threshold, not the exact number.
	restartLoopQueryLimit = 20
	agentApprovalPlanID   = "current_recommendation"
	// Rolling latency window. At the default poll interval this is roughly the
	// last hour, which is the right span for "is it slow right now" — a
	// week-long baseline would smooth away the outage being looked at.
	probeWindowSamples      = 120
	probeBaselineMinSamples = 20
	// How far back to seed the in-memory window from persisted buckets.
	probeSeedWindow  = 12 * time.Hour
	maxLatencyWindow = 14 * 24 * time.Hour
	// 14 days of 5-minute buckets for one subject, so a full-window request is
	// never truncated in a way that silently shortens the chart.
	maxLatencyBuckets = 4200

	agentRepairPerAppCooldown      = time.Minute
	agentRepairGlobalWindow        = time.Hour
	agentRepairGlobalLimit         = 5
	actionReviewReferenceLimit     = 6
	actionReviewReferenceBytes     = 18 * 1024
	actionReviewReferenceFileBytes = 8 * 1024

	arrayStartActionID   = "ask_admin_to_start_array"
	unifiRestartActionID = "ask_admin_to_restart_unifi_device"
	arrayTargetID        = "unraid_array"
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
		agentRepairs:           newAgentRepairLimiter(),
		probes:                 newProbeTracker(),
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
	mux.HandleFunc("GET /api/admin/data/summary", a.requireAdmin(a.adminDataSummary))
	mux.HandleFunc("POST /api/admin/data/clear", a.requireAdmin(a.adminDataClear))
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
	mux.HandleFunc("GET /api/admin/unifi/devices/restartable", a.requireAdmin(a.unifiRestartableDevices))
	mux.HandleFunc("POST /api/admin/unifi/devices/", a.requireAdmin(a.unifiRestartDevice))
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
	mux.HandleFunc("GET /api/infrastructure/latency", a.requireAuth(a.latencySeries))
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
	// Warm the latency baseline from persisted buckets before the first poll, so
	// a restart does not leave the latency rules blind while the window refills.
	a.seedProbeWindowFromMetrics()
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

// annotateRestartLoops counts recent status changes per app so the rule engine
// can tell a flapping container from one that is simply down. The count is
// computed here, where history lives, rather than inside the rules: keeping
// Evaluate a pure function of the snapshot is what makes it testable without a
// store.
//
// Best-effort by design. No history configured, or a query that fails, leaves
// the count at zero and the app is diagnosed exactly as it was before — a
// missing history must not turn into a missing incident.
func (a *App) annotateRestartLoops(apps []models.AppStatus) {
	if a.deps.History == nil || len(apps) == 0 {
		return
	}
	since := time.Now().UTC().Add(-diagnostics.RestartLoopWindow)
	for i := range apps {
		events, err := a.deps.History.Query(db.HistoryFilter{
			SubjectType: models.SubjectApp,
			SubjectID:   apps[i].AppID,
			Since:       since,
			Limit:       restartLoopQueryLimit,
		})
		if err != nil {
			continue
		}
		apps[i].RecentStatusChanges = len(events)
	}
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
	// Both annotations run before Evaluate so the rule engine stays a pure
	// function of the snapshot.
	a.annotateRestartLoops(apps)
	a.annotateProbeBaselines(&infra)
	a.recordLatencyBucket(infra)
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
	var reason string
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
