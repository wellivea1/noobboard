package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
)

type Mode string

const (
	ModeAdminRequested       Mode = "admin_requested"
	ModeGeneralUserRequested Mode = "general_user_requested"
	ModeAutomaticIncident    Mode = "automatic_incident"
	ModeNotificationMessage  Mode = "notification_message"
)

type Request struct {
	Mode         Mode
	Policy       models.LLMPolicy
	Snapshot     models.Snapshot
	Question     string
	ActorID      string
	LiveSnapshot func(context.Context) (models.Snapshot, error)
	ToolAudit    func(name string, ok bool, err string)
}

type Client interface {
	Diagnose(context.Context, Request) (Diagnosis, error)
}

type ContextBuilder struct {
	redactor *privacy.Redactor
}

func NewContextBuilder(redactor *privacy.Redactor) ContextBuilder {
	return ContextBuilder{redactor: redactor}
}

func (b ContextBuilder) Build(req Request) (string, error) {
	if !req.Policy.Enabled {
		return "", errors.New("llm policy disabled")
	}
	snapshot := privacy.FilterSnapshotForRole(req.Snapshot, req.Policy.RecipientRole, b.redactor)
	if !req.Policy.IncludeLogs {
		for i := range snapshot.Apps {
			snapshot.Apps[i].RecentLogs = nil
		}
	}
	if !req.Policy.AllowHiddenAppNames {
		snapshot.Visibility.HiddenAppIDs = nil
		snapshot.Visibility.HiddenContainerNames = nil
	}
	for i := range snapshot.Apps {
		if b.redactor.IsBlacklistedApp(snapshot.Apps[i]) && !req.Policy.AllowBlacklistedNames {
			if req.Policy.RecipientRole != models.RoleAdmin {
				return "", errors.New("blacklisted app reached llm context")
			}
			snapshot.Apps[i].DisplayName = "[REDACTED]"
			snapshot.Apps[i].ContainerName = "[REDACTED]"
		}
		logs, changed := b.redactor.RedactLogs(snapshot.Apps[i].RecentLogs)
		if changed && req.Policy.FailClosedOnRedaction {
			snapshot.Apps[i].RecentLogs = logs
		} else {
			snapshot.Apps[i].RecentLogs = logs
		}
		if req.Policy.MaxLogLines >= 0 && len(snapshot.Apps[i].RecentLogs) > req.Policy.MaxLogLines {
			snapshot.Apps[i].RecentLogs = snapshot.Apps[i].RecentLogs[:req.Policy.MaxLogLines]
		}
	}

	instruction := "Diagnose the server status data. Do not request tools, do not execute actions, and recommend only one allowlisted next action."
	if req.Policy.AgentToolsEnabled && req.Policy.RecipientRole == models.RoleAdmin {
		instruction = "Diagnose the server status data. You may call the provided read-only NoobBoard status tools to refresh live status. Do not request or execute mutations, repairs, shell commands, filesystem access, Docker control, Unraid mutations, or UniFi configuration changes. Recommend only one allowlisted next action."
	}
	if req.Policy.RecipientRole != models.RoleAdmin {
		return b.buildGeneralUserContext(snapshot, req)
	}
	return b.buildAdminContext(snapshot, req, instruction)
}

type adminContextLimits struct {
	IncludeSnapshot  bool
	IncludeProbeRows bool
	AppLimit         int
	IncidentLimit    int
	FactLimit        int
	WarningLimit     int
	LogLineLimit     int
	SummaryChars     int
	LogLineChars     int
}

func (b ContextBuilder) buildAdminContext(snapshot models.Snapshot, req Request, instruction string) (string, error) {
	profiles := []adminContextLimits{
		{IncludeSnapshot: true, IncludeProbeRows: true, AppLimit: -1, IncidentLimit: -1, FactLimit: -1, WarningLimit: -1, LogLineLimit: -1},
		{IncludeSnapshot: false, IncludeProbeRows: true, AppLimit: -1, IncidentLimit: -1, FactLimit: -1, WarningLimit: -1, LogLineLimit: -1},
		{IncludeSnapshot: false, IncludeProbeRows: true, AppLimit: 80, IncidentLimit: 20, FactLimit: 20, WarningLimit: 20, LogLineLimit: 5, SummaryChars: 600, LogLineChars: 600},
		{IncludeSnapshot: false, IncludeProbeRows: true, AppLimit: 40, IncidentLimit: 12, FactLimit: 12, WarningLimit: 12, LogLineLimit: 3, SummaryChars: 400, LogLineChars: 400},
		{IncludeSnapshot: false, IncludeProbeRows: true, AppLimit: 24, IncidentLimit: 8, FactLimit: 8, WarningLimit: 8, LogLineLimit: 1, SummaryChars: 300, LogLineChars: 300},
		{IncludeSnapshot: false, IncludeProbeRows: true, AppLimit: 16, IncidentLimit: 6, FactLimit: 6, WarningLimit: 6, LogLineLimit: 0, SummaryChars: 220},
		{IncludeSnapshot: false, IncludeProbeRows: false, AppLimit: 8, IncidentLimit: 4, FactLimit: 4, WarningLimit: 4, LogLineLimit: 0, SummaryChars: 180},
		{IncludeSnapshot: false, IncludeProbeRows: false, AppLimit: 3, IncidentLimit: 2, FactLimit: 2, WarningLimit: 2, LogLineLimit: 0, SummaryChars: 140},
		{IncludeSnapshot: false, IncludeProbeRows: false, AppLimit: 0, IncidentLimit: 0, FactLimit: 0, WarningLimit: 0, LogLineLimit: 0, SummaryChars: 100},
	}
	lastSize := 0
	for _, limits := range profiles {
		compacted := compactAdminSnapshot(snapshot, req.Question, limits)
		apiReport := buildAPIReport(compacted)
		setDockerReportCounts(&apiReport.Docker, snapshot.Apps)
		if !limits.IncludeProbeRows {
			apiReport.Probes.AppProbeResults = nil
		}
		payload := map[string]interface{}{
			"mode":        req.Mode,
			"question":    compactText(req.Question, 1000),
			"api_report":  apiReport,
			"instruction": instruction,
		}
		if limits.IncludeSnapshot {
			payload["snapshot"] = compacted
		}
		data, err := b.encodeContextPayload(payload, req.Policy.FailClosedOnRedaction)
		if err != nil {
			return "", err
		}
		lastSize = len(data)
		if lastSize > req.Policy.MaxContextBytes {
			continue
		}
		return string(data), nil
	}
	return "", fmt.Errorf("llm context exceeds max_context_bytes: %d > %d", lastSize, req.Policy.MaxContextBytes)
}

func setDockerReportCounts(report *dockerAPIReport, apps []models.AppStatus) {
	report.AppCount = len(apps)
	report.OnlineAppCount = 0
	report.DegradedAppCount = 0
	report.OfflineAppCount = 0
	report.UnknownAppCount = 0
	for _, app := range apps {
		switch app.CurrentStatus {
		case models.StatusOnline:
			report.OnlineAppCount++
		case models.StatusDegraded:
			report.DegradedAppCount++
		case models.StatusOffline:
			report.OfflineAppCount++
		default:
			report.UnknownAppCount++
		}
	}
}

func (b ContextBuilder) encodeContextPayload(payload map[string]interface{}, failClosed bool) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	result := b.redactor.RedactString(string(data))
	if result.Changed && failClosed {
		data = []byte(result.Text)
	}
	if strings.Contains(string(data), "replace_me") {
		return nil, errors.New("placeholder secret reached llm context")
	}
	return data, nil
}

func compactAdminSnapshot(snapshot models.Snapshot, question string, limits adminContextLimits) models.Snapshot {
	out := snapshot
	out.ServerSummary = compactText(out.ServerSummary, limits.SummaryChars)
	out.AdminSummary = compactText(out.AdminSummary, limits.SummaryChars)
	out.AuditTail = nil
	out.LLMPolicies = nil
	out.Infrastructure.StorageWarnings = compactStringSlice(out.Infrastructure.StorageWarnings, limits.WarningLimit)
	out.Infrastructure.UniFiWarnings = compactStringSlice(out.Infrastructure.UniFiWarnings, limits.WarningLimit)
	out.Apps = prioritizedAdminApps(snapshot.Apps, question, limits.AppLimit)
	for i := range out.Apps {
		out.Apps[i].ServerSummary = compactText(out.Apps[i].ServerSummary, limits.SummaryChars)
		out.Apps[i].AdminSummary = compactText(out.Apps[i].AdminSummary, limits.SummaryChars)
		out.Apps[i].CurrentProbeResult.Message = compactText(out.Apps[i].CurrentProbeResult.Message, limits.SummaryChars)
		out.Apps[i].RecentLogs = compactLogLines(out.Apps[i].RecentLogs, limits.LogLineLimit, limits.LogLineChars)
	}
	out.Incidents = compactIncidents(out.Incidents, limits.IncidentLimit)
	for i := range out.Incidents {
		out.Incidents[i].Summary = compactText(out.Incidents[i].Summary, limits.SummaryChars)
		out.Incidents[i].AdminSummary = compactText(out.Incidents[i].AdminSummary, limits.SummaryChars)
		out.Incidents[i].AffectedServices = compactStringSlice(out.Incidents[i].AffectedServices, limits.WarningLimit)
		out.Incidents[i].Evidence = compactStringSlice(out.Incidents[i].Evidence, limits.WarningLimit)
	}
	out.Facts = compactFacts(out.Facts, limits.FactLimit)
	for i := range out.Facts {
		out.Facts[i].Summary = compactText(out.Facts[i].Summary, limits.SummaryChars)
		out.Facts[i].Evidence = compactStringSlice(out.Facts[i].Evidence, limits.WarningLimit)
		out.Facts[i].AffectedServices = compactStringSlice(out.Facts[i].AffectedServices, limits.WarningLimit)
	}
	return out
}

func prioritizedAdminApps(apps []models.AppStatus, question string, limit int) []models.AppStatus {
	if limit < 0 || len(apps) <= limit {
		return append([]models.AppStatus(nil), apps...)
	}
	if limit == 0 {
		return nil
	}
	type scoredApp struct {
		app   models.AppStatus
		score int
		index int
	}
	lowerQuestion := strings.ToLower(question)
	scored := make([]scoredApp, 0, len(apps))
	for index, app := range apps {
		score := 0
		if app.CurrentStatus != models.StatusOnline {
			score += 100
		}
		if app.Severity != "" && app.Severity != models.SeverityNone {
			score += 20
		}
		for _, name := range []string{app.DisplayName, app.AppID, app.ContainerName} {
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "" && strings.Contains(lowerQuestion, name) {
				score += 200
			}
		}
		scored = append(scored, scoredApp{app: app, score: score, index: index})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})
	out := make([]models.AppStatus, 0, limit)
	for i := 0; i < limit && i < len(scored); i++ {
		out = append(out, scored[i].app)
	}
	return out
}

func compactLogLines(values []models.LogLine, lineLimit, charLimit int) []models.LogLine {
	if lineLimit < 0 && charLimit <= 0 {
		return append([]models.LogLine(nil), values...)
	}
	if lineLimit == 0 {
		return nil
	}
	if lineLimit > 0 && len(values) > lineLimit {
		values = values[:lineLimit]
	}
	out := append([]models.LogLine(nil), values...)
	if charLimit > 0 {
		for i := range out {
			out[i].Line = compactText(out[i].Line, charLimit)
		}
	}
	return out
}

type generalUserContextLimits struct {
	AppLimit          int
	IncidentLimit     int
	FactLimit         int
	WarningLimit      int
	AffectedLimit     int
	SummaryChars      int
	ProbeMessageChars int
	IncludeProbeRows  bool
}

func (b ContextBuilder) buildGeneralUserContext(snapshot models.Snapshot, req Request) (string, error) {
	profiles := []generalUserContextLimits{
		{AppLimit: 24, IncidentLimit: 8, FactLimit: 8, WarningLimit: 8, AffectedLimit: 8, SummaryChars: 220, ProbeMessageChars: 140, IncludeProbeRows: true},
		{AppLimit: 16, IncidentLimit: 6, FactLimit: 6, WarningLimit: 6, AffectedLimit: 6, SummaryChars: 180, ProbeMessageChars: 100, IncludeProbeRows: true},
		{AppLimit: 10, IncidentLimit: 4, FactLimit: 4, WarningLimit: 4, AffectedLimit: 4, SummaryChars: 150, ProbeMessageChars: 80, IncludeProbeRows: true},
		{AppLimit: 8, IncidentLimit: 3, FactLimit: 3, WarningLimit: 3, AffectedLimit: 3, SummaryChars: 140, ProbeMessageChars: 0, IncludeProbeRows: false},
		{AppLimit: 5, IncidentLimit: 2, FactLimit: 2, WarningLimit: 2, AffectedLimit: 2, SummaryChars: 120, ProbeMessageChars: 0, IncludeProbeRows: false},
		{AppLimit: 3, IncidentLimit: 1, FactLimit: 1, WarningLimit: 1, AffectedLimit: 1, SummaryChars: 100, ProbeMessageChars: 0, IncludeProbeRows: false},
		{AppLimit: 1, IncidentLimit: 0, FactLimit: 0, WarningLimit: 0, AffectedLimit: 0, SummaryChars: 80, ProbeMessageChars: 0, IncludeProbeRows: false},
		{AppLimit: 0, IncidentLimit: 0, FactLimit: 0, WarningLimit: 0, AffectedLimit: 0, SummaryChars: 60, ProbeMessageChars: 0, IncludeProbeRows: false},
	}
	lastSize := 0
	for _, limits := range profiles {
		payload := map[string]interface{}{
			"mode":        req.Mode,
			"question":    compactText(req.Question, 500),
			"api_report":  buildGeneralUserAPIReport(snapshot, req.Question, limits),
			"instruction": "Explain the visible home status in plain English. Do not use technical vocabulary, request tools, execute actions, or recommend anything beyond telling the admin.",
		}
		generic, err := genericPayload(payload)
		if err != nil {
			return "", err
		}
		stripped, ok := stripGeneralUserOnlyFields(generic).(map[string]interface{})
		if !ok {
			return "", errors.New("general user llm context was not an object")
		}
		data, err := json.Marshal(stripped)
		if err != nil {
			return "", err
		}
		result := b.redactor.RedactString(string(data))
		if result.Changed && req.Policy.FailClosedOnRedaction {
			data = []byte(result.Text)
		}
		lastSize = len(data)
		if lastSize > req.Policy.MaxContextBytes {
			continue
		}
		if strings.Contains(string(data), "replace_me") {
			return "", errors.New("placeholder secret reached llm context")
		}
		return string(data), nil
	}
	return "", fmt.Errorf("llm context exceeds max_context_bytes: %d > %d", lastSize, req.Policy.MaxContextBytes)
}

type apiReport struct {
	GeneratedAt     time.Time             `json:"generated_at"`
	OverallStatus   models.CurrentStatus  `json:"overall_status"`
	ServerSummary   string                `json:"server_summary"`
	AdminSummary    string                `json:"admin_summary,omitempty"`
	IntegrationMode string                `json:"integration_mode,omitempty"`
	FixtureScenario string                `json:"fixture_scenario,omitempty"`
	SourceHealth    models.SourceHealth   `json:"source_health"`
	Unraid          unraidAPIReport       `json:"unraid"`
	Docker          dockerAPIReport       `json:"docker"`
	UniFi           unifiAPIReport        `json:"unifi"`
	Probes          probesAPIReport       `json:"probes"`
	Incidents       []models.Incident     `json:"incidents"`
	Facts           []models.IncidentFact `json:"facts"`
}

type unraidAPIReport struct {
	NASReachable            bool      `json:"nas_reachable"`
	NASLinkSpeedMbps        int       `json:"nas_link_speed_mbps"`
	ExpectedNASLinkMbps     int       `json:"expected_nas_link_mbps"`
	UnraidAPIReachable      bool      `json:"unraid_api_reachable"`
	UnraidVersion           string    `json:"unraid_version,omitempty"`
	UnraidUptimeSeconds     int64     `json:"unraid_uptime_seconds,omitempty"`
	UnraidCPUBrand          string    `json:"unraid_cpu_brand,omitempty"`
	UnraidCPUCores          int       `json:"unraid_cpu_cores,omitempty"`
	UnraidCPUThreads        int       `json:"unraid_cpu_threads,omitempty"`
	UnraidMemoryTotalBytes  int64     `json:"unraid_memory_total_bytes,omitempty"`
	UnraidMemoryUsedBytes   int64     `json:"unraid_memory_used_bytes,omitempty"`
	UnraidMemoryUsedPct     float64   `json:"unraid_memory_used_pct,omitempty"`
	UnraidNotificationCount int       `json:"unraid_notification_count,omitempty"`
	UnraidAlertCount        int       `json:"unraid_alert_count,omitempty"`
	UnraidWarningCount      int       `json:"unraid_warning_count,omitempty"`
	UnraidVMCount           int       `json:"unraid_vm_count,omitempty"`
	UnraidVMRunningCount    int       `json:"unraid_vm_running_count,omitempty"`
	UnraidVMStoppedCount    int       `json:"unraid_vm_stopped_count,omitempty"`
	UnraidVMNames           []string  `json:"unraid_vm_names,omitempty"`
	UnraidShareCount        int       `json:"unraid_share_count,omitempty"`
	UnraidShareNames        []string  `json:"unraid_share_names,omitempty"`
	UnraidArrayState        string    `json:"unraid_array_state"`
	UnraidArrayHealthy      bool      `json:"unraid_array_healthy"`
	ArrayDiskCount          int       `json:"array_disk_count,omitempty"`
	ArrayDiskWarningCount   int       `json:"array_disk_warning_count,omitempty"`
	ArrayCapacityTotalBytes int64     `json:"array_capacity_total_bytes,omitempty"`
	ArrayCapacityUsedBytes  int64     `json:"array_capacity_used_bytes,omitempty"`
	ArrayCapacityFreeBytes  int64     `json:"array_capacity_free_bytes,omitempty"`
	ArrayCapacityUsedPct    float64   `json:"array_capacity_used_pct,omitempty"`
	StorageWarnings         []string  `json:"storage_warnings,omitempty"`
	ParityCheckState        string    `json:"parity_check_state,omitempty"`
	LastCheckedAt           time.Time `json:"last_checked_at"`
	SourceHealth            string    `json:"source_health"`
}

type dockerAPIReport struct {
	ServiceAvailable bool               `json:"docker_service_available"`
	SourceHealth     string             `json:"source_health"`
	AppCount         int                `json:"app_count"`
	NetworkCount     int                `json:"network_count,omitempty"`
	NetworkNames     []string           `json:"network_names,omitempty"`
	OnlineAppCount   int                `json:"online_app_count"`
	DegradedAppCount int                `json:"degraded_app_count"`
	OfflineAppCount  int                `json:"offline_app_count"`
	UnknownAppCount  int                `json:"unknown_app_count"`
	Apps             []models.AppStatus `json:"apps"`
}

type unifiAPIReport struct {
	InternetReachable       bool     `json:"internet_reachable"`
	DNSOK                   bool     `json:"dns_ok"`
	RouterReachable         bool     `json:"router_reachable"`
	UniFiWANUp              bool     `json:"unifi_wan_up"`
	UniFiGatewayReachable   bool     `json:"unifi_gateway_reachable"`
	UniFiSiteID             string   `json:"unifi_site_id,omitempty"`
	UniFiSiteName           string   `json:"unifi_site_name,omitempty"`
	UniFiDeviceCount        int      `json:"unifi_device_count,omitempty"`
	UniFiOfflineDeviceCount int      `json:"unifi_offline_device_count,omitempty"`
	UniFiClientCount        int      `json:"unifi_client_count,omitempty"`
	UniFiFirmwareUpdates    int      `json:"unifi_firmware_updates,omitempty"`
	UniFiWANCount           int      `json:"unifi_wan_count,omitempty"`
	UniFiWarnings           []string `json:"unifi_warnings,omitempty"`
	SourceHealth            string   `json:"source_health"`
}

type probesAPIReport struct {
	InternetReachable   bool             `json:"internet_reachable"`
	DNSOK               bool             `json:"dns_ok"`
	RouterReachable     bool             `json:"router_reachable"`
	NASReachable        bool             `json:"nas_reachable"`
	NASLinkSpeedMbps    int              `json:"nas_link_speed_mbps"`
	ExpectedNASLinkMbps int              `json:"expected_nas_link_mbps"`
	SourceHealth        string           `json:"source_health"`
	AppProbeResults     []appProbeReport `json:"app_probe_results"`
}

type appProbeReport struct {
	AppID              string                `json:"app_id"`
	DisplayName        string                `json:"display_name"`
	CurrentStatus      models.CurrentStatus  `json:"current_status"`
	Severity           models.Severity       `json:"severity"`
	ProbeType          models.ProbeType      `json:"probe_type"`
	EndpointStatus     models.EndpointStatus `json:"endpoint_status"`
	CurrentProbeResult models.ProbeResult    `json:"current_probe_result"`
}

type generalUserAPIReport struct {
	GeneratedAt     time.Time             `json:"generated_at"`
	OverallStatus   models.CurrentStatus  `json:"overall_status"`
	ServerSummary   string                `json:"server_summary"`
	IntegrationMode string                `json:"integration_mode,omitempty"`
	SourceHealth    models.SourceHealth   `json:"source_health"`
	Unraid          generalUnraidReport   `json:"unraid"`
	Docker          generalDockerReport   `json:"docker"`
	UniFi           generalUniFiReport    `json:"unifi"`
	Probes          generalProbesReport   `json:"probes"`
	Incidents       []generalIncident     `json:"incidents,omitempty"`
	Facts           []generalIncidentFact `json:"facts,omitempty"`
}

type generalUnraidReport struct {
	NASReachable       bool   `json:"nas_reachable"`
	UnraidAPIReachable bool   `json:"unraid_api_reachable"`
	UnraidArrayState   string `json:"unraid_array_state"`
	UnraidArrayHealthy bool   `json:"unraid_array_healthy"`
	StorageWarning     bool   `json:"storage_warning"`
	SourceHealth       string `json:"source_health,omitempty"`
}

type generalDockerReport struct {
	ServiceAvailable bool               `json:"docker_service_available"`
	AppCount         int                `json:"app_count"`
	IncludedAppCount int                `json:"included_app_count"`
	OmittedAppCount  int                `json:"omitted_app_count,omitempty"`
	OnlineAppCount   int                `json:"online_app_count"`
	DegradedAppCount int                `json:"degraded_app_count"`
	OfflineAppCount  int                `json:"offline_app_count"`
	UnknownAppCount  int                `json:"unknown_app_count"`
	Apps             []generalAppReport `json:"apps"`
	SourceHealth     string             `json:"source_health,omitempty"`
}

type generalUniFiReport struct {
	InternetReachable       bool     `json:"internet_reachable"`
	DNSOK                   bool     `json:"dns_ok"`
	RouterReachable         bool     `json:"router_reachable"`
	UniFiWANUp              bool     `json:"unifi_wan_up"`
	UniFiGatewayReachable   bool     `json:"unifi_gateway_reachable"`
	UniFiOfflineDeviceCount int      `json:"unifi_offline_device_count,omitempty"`
	UniFiFirmwareUpdates    int      `json:"unifi_firmware_updates,omitempty"`
	UniFiWarnings           []string `json:"unifi_warnings,omitempty"`
	SourceHealth            string   `json:"source_health,omitempty"`
}

type generalProbesReport struct {
	InternetReachable bool                    `json:"internet_reachable"`
	DNSOK             bool                    `json:"dns_ok"`
	RouterReachable   bool                    `json:"router_reachable"`
	NASReachable      bool                    `json:"nas_reachable"`
	AppProbeResults   []generalAppProbeReport `json:"app_probe_results,omitempty"`
	SourceHealth      string                  `json:"source_health,omitempty"`
}

type generalAppReport struct {
	AppID          string                `json:"app_id"`
	DisplayName    string                `json:"display_name"`
	CurrentStatus  models.CurrentStatus  `json:"current_status"`
	Severity       models.Severity       `json:"severity"`
	EndpointStatus models.EndpointStatus `json:"endpoint_status,omitempty"`
	DockerState    models.DockerState    `json:"docker_state,omitempty"`
	Summary        string                `json:"summary,omitempty"`
}

type generalAppProbeReport struct {
	AppID              string               `json:"app_id"`
	DisplayName        string               `json:"display_name"`
	CurrentStatus      models.CurrentStatus `json:"current_status"`
	Severity           models.Severity      `json:"severity"`
	CurrentProbeResult generalProbeResult   `json:"current_probe_result"`
}

type generalProbeResult struct {
	Type      models.ProbeType `json:"type,omitempty"`
	OK        bool             `json:"ok"`
	Message   string           `json:"message,omitempty"`
	LatencyMS int64            `json:"latency_ms,omitempty"`
}

type generalIncident struct {
	Type             models.IncidentType  `json:"type"`
	Severity         models.Severity      `json:"severity"`
	Status           models.CurrentStatus `json:"status"`
	Summary          string               `json:"summary"`
	AffectedServices []string             `json:"affected_services,omitempty"`
}

type generalIncidentFact struct {
	Type             models.IncidentType `json:"type"`
	Severity         models.Severity     `json:"severity"`
	Summary          string              `json:"summary"`
	AffectedServices []string            `json:"affected_services,omitempty"`
}

func buildAPIReport(snapshot models.Snapshot) apiReport {
	infra := snapshot.Infrastructure
	report := apiReport{
		GeneratedAt:     snapshot.GeneratedAt,
		OverallStatus:   snapshot.OverallStatus,
		ServerSummary:   snapshot.ServerSummary,
		AdminSummary:    snapshot.AdminSummary,
		IntegrationMode: snapshot.IntegrationMode,
		FixtureScenario: snapshot.FixtureScenario,
		SourceHealth:    infra.SourceHealth,
		Unraid: unraidAPIReport{
			NASReachable:            infra.NASReachable,
			NASLinkSpeedMbps:        infra.NASLinkSpeedMbps,
			ExpectedNASLinkMbps:     infra.ExpectedNASLinkMbps,
			UnraidAPIReachable:      infra.UnraidAPIReachable,
			UnraidVersion:           infra.UnraidVersion,
			UnraidUptimeSeconds:     infra.UnraidUptimeSeconds,
			UnraidCPUBrand:          infra.UnraidCPUBrand,
			UnraidCPUCores:          infra.UnraidCPUCores,
			UnraidCPUThreads:        infra.UnraidCPUThreads,
			UnraidMemoryTotalBytes:  infra.UnraidMemoryTotalBytes,
			UnraidMemoryUsedBytes:   infra.UnraidMemoryUsedBytes,
			UnraidMemoryUsedPct:     infra.UnraidMemoryUsedPct,
			UnraidNotificationCount: infra.UnraidNotificationCount,
			UnraidAlertCount:        infra.UnraidAlertCount,
			UnraidWarningCount:      infra.UnraidWarningCount,
			UnraidVMCount:           infra.UnraidVMCount,
			UnraidVMRunningCount:    infra.UnraidVMRunningCount,
			UnraidVMStoppedCount:    infra.UnraidVMStoppedCount,
			UnraidVMNames:           append([]string(nil), infra.UnraidVMNames...),
			UnraidShareCount:        infra.UnraidShareCount,
			UnraidShareNames:        append([]string(nil), infra.UnraidShareNames...),
			UnraidArrayState:        infra.UnraidArrayState,
			UnraidArrayHealthy:      infra.UnraidArrayHealthy,
			ArrayDiskCount:          infra.ArrayDiskCount,
			ArrayDiskWarningCount:   infra.ArrayDiskWarningCount,
			ArrayCapacityTotalBytes: infra.ArrayCapacityTotalBytes,
			ArrayCapacityUsedBytes:  infra.ArrayCapacityUsedBytes,
			ArrayCapacityFreeBytes:  infra.ArrayCapacityFreeBytes,
			ArrayCapacityUsedPct:    infra.ArrayCapacityUsedPct,
			StorageWarnings:         append([]string(nil), infra.StorageWarnings...),
			ParityCheckState:        infra.ParityCheckState,
			LastCheckedAt:           infra.LastCheckedAt,
			SourceHealth:            infra.SourceHealth.Unraid,
		},
		Docker: dockerAPIReport{
			ServiceAvailable: infra.DockerServiceAvailable,
			SourceHealth:     infra.SourceHealth.Docker,
			AppCount:         len(snapshot.Apps),
			NetworkCount:     infra.DockerNetworkCount,
			NetworkNames:     append([]string(nil), infra.DockerNetworkNames...),
			Apps:             append([]models.AppStatus(nil), snapshot.Apps...),
		},
		UniFi: unifiAPIReport{
			InternetReachable:       infra.InternetReachable,
			DNSOK:                   infra.DNSOK,
			RouterReachable:         infra.RouterReachable,
			UniFiWANUp:              infra.UniFiWANUp,
			UniFiGatewayReachable:   infra.UniFiGatewayReachable,
			UniFiSiteID:             infra.UniFiSiteID,
			UniFiSiteName:           infra.UniFiSiteName,
			UniFiDeviceCount:        infra.UniFiDeviceCount,
			UniFiOfflineDeviceCount: infra.UniFiOfflineDeviceCount,
			UniFiClientCount:        infra.UniFiClientCount,
			UniFiFirmwareUpdates:    infra.UniFiFirmwareUpdates,
			UniFiWANCount:           infra.UniFiWANCount,
			UniFiWarnings:           append([]string(nil), infra.UniFiWarnings...),
			SourceHealth:            infra.SourceHealth.UniFi,
		},
		Probes: probesAPIReport{
			InternetReachable:   infra.InternetReachable,
			DNSOK:               infra.DNSOK,
			RouterReachable:     infra.RouterReachable,
			NASReachable:        infra.NASReachable,
			NASLinkSpeedMbps:    infra.NASLinkSpeedMbps,
			ExpectedNASLinkMbps: infra.ExpectedNASLinkMbps,
			SourceHealth:        infra.SourceHealth.Probes,
			AppProbeResults:     make([]appProbeReport, 0, len(snapshot.Apps)),
		},
		Incidents: append([]models.Incident(nil), snapshot.Incidents...),
		Facts:     append([]models.IncidentFact(nil), snapshot.Facts...),
	}
	for _, app := range snapshot.Apps {
		switch app.CurrentStatus {
		case models.StatusOnline:
			report.Docker.OnlineAppCount++
		case models.StatusDegraded:
			report.Docker.DegradedAppCount++
		case models.StatusOffline:
			report.Docker.OfflineAppCount++
		default:
			report.Docker.UnknownAppCount++
		}
		report.Probes.AppProbeResults = append(report.Probes.AppProbeResults, appProbeReport{
			AppID:              app.AppID,
			DisplayName:        app.DisplayName,
			CurrentStatus:      app.CurrentStatus,
			Severity:           app.Severity,
			ProbeType:          app.ProbeType,
			EndpointStatus:     app.EndpointStatus,
			CurrentProbeResult: app.CurrentProbeResult,
		})
	}
	return report
}

func buildGeneralUserAPIReport(snapshot models.Snapshot, question string, limits generalUserContextLimits) generalUserAPIReport {
	infra := snapshot.Infrastructure
	apps := prioritizedGeneralUserApps(snapshot.Apps, question, limits.AppLimit)
	report := generalUserAPIReport{
		GeneratedAt:     snapshot.GeneratedAt,
		OverallStatus:   snapshot.OverallStatus,
		ServerSummary:   compactText(snapshot.ServerSummary, limits.SummaryChars),
		IntegrationMode: snapshot.IntegrationMode,
		SourceHealth:    infra.SourceHealth,
		Unraid: generalUnraidReport{
			NASReachable:       infra.NASReachable,
			UnraidAPIReachable: infra.UnraidAPIReachable,
			UnraidArrayState:   infra.UnraidArrayState,
			UnraidArrayHealthy: infra.UnraidArrayHealthy,
			StorageWarning:     len(infra.StorageWarnings) > 0 || infra.ArrayDiskWarningCount > 0,
			SourceHealth:       infra.SourceHealth.Unraid,
		},
		Docker: generalDockerReport{
			ServiceAvailable: infra.DockerServiceAvailable,
			AppCount:         len(snapshot.Apps),
			IncludedAppCount: len(apps),
			OmittedAppCount:  maxInt(0, len(snapshot.Apps)-len(apps)),
			Apps:             make([]generalAppReport, 0, len(apps)),
			SourceHealth:     infra.SourceHealth.Docker,
		},
		UniFi: generalUniFiReport{
			InternetReachable:       infra.InternetReachable,
			DNSOK:                   infra.DNSOK,
			RouterReachable:         infra.RouterReachable,
			UniFiWANUp:              infra.UniFiWANUp,
			UniFiGatewayReachable:   infra.UniFiGatewayReachable,
			UniFiOfflineDeviceCount: infra.UniFiOfflineDeviceCount,
			UniFiFirmwareUpdates:    infra.UniFiFirmwareUpdates,
			UniFiWarnings:           compactStringSlice(infra.UniFiWarnings, limits.WarningLimit),
			SourceHealth:            infra.SourceHealth.UniFi,
		},
		Probes: generalProbesReport{
			InternetReachable: infra.InternetReachable,
			DNSOK:             infra.DNSOK,
			RouterReachable:   infra.RouterReachable,
			NASReachable:      infra.NASReachable,
			SourceHealth:      infra.SourceHealth.Probes,
		},
		Incidents: make([]generalIncident, 0, len(snapshot.Incidents)),
		Facts:     make([]generalIncidentFact, 0, len(snapshot.Facts)),
	}
	for _, app := range snapshot.Apps {
		switch app.CurrentStatus {
		case models.StatusOnline:
			report.Docker.OnlineAppCount++
		case models.StatusDegraded:
			report.Docker.DegradedAppCount++
		case models.StatusOffline:
			report.Docker.OfflineAppCount++
		default:
			report.Docker.UnknownAppCount++
		}
	}
	for _, app := range apps {
		report.Docker.Apps = append(report.Docker.Apps, generalAppReport{
			AppID:          app.AppID,
			DisplayName:    app.DisplayName,
			CurrentStatus:  app.CurrentStatus,
			Severity:       app.Severity,
			EndpointStatus: app.EndpointStatus,
			DockerState:    app.DockerState,
			Summary:        compactText(app.ServerSummary, limits.SummaryChars),
		})
		if limits.IncludeProbeRows {
			report.Probes.AppProbeResults = append(report.Probes.AppProbeResults, generalAppProbeReport{
				AppID:         app.AppID,
				DisplayName:   app.DisplayName,
				CurrentStatus: app.CurrentStatus,
				Severity:      app.Severity,
				CurrentProbeResult: generalProbeResult{
					Type:      app.CurrentProbeResult.Type,
					OK:        app.CurrentProbeResult.OK,
					Message:   compactText(app.CurrentProbeResult.Message, limits.ProbeMessageChars),
					LatencyMS: app.CurrentProbeResult.LatencyMS,
				},
			})
		}
	}
	for _, incident := range compactIncidents(snapshot.Incidents, limits.IncidentLimit) {
		report.Incidents = append(report.Incidents, generalIncident{
			Type:             incident.Type,
			Severity:         incident.Severity,
			Status:           incident.Status,
			Summary:          compactText(incident.Summary, limits.SummaryChars),
			AffectedServices: compactStringSlice(incident.AffectedServices, limits.AffectedLimit),
		})
	}
	for _, fact := range compactFacts(snapshot.Facts, limits.FactLimit) {
		report.Facts = append(report.Facts, generalIncidentFact{
			Type:             fact.Type,
			Severity:         fact.Severity,
			Summary:          compactText(fact.Summary, limits.SummaryChars),
			AffectedServices: compactStringSlice(fact.AffectedServices, limits.AffectedLimit),
		})
	}
	return report
}

func prioritizedGeneralUserApps(apps []models.AppStatus, question string, limit int) []models.AppStatus {
	if limit <= 0 {
		return nil
	}
	if len(apps) <= limit {
		return append([]models.AppStatus(nil), apps...)
	}
	type scoredApp struct {
		app   models.AppStatus
		score int
		index int
	}
	lowerQuestion := strings.ToLower(question)
	scored := make([]scoredApp, 0, len(apps))
	for index, app := range apps {
		score := 0
		if app.CurrentStatus != models.StatusOnline {
			score += 100
		}
		if app.Severity != "" && app.Severity != models.SeverityNone {
			score += 20
		}
		for _, name := range []string{app.DisplayName, app.AppID} {
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "" && strings.Contains(lowerQuestion, name) {
				score += 200
			}
		}
		scored = append(scored, scoredApp{app: app, score: score, index: index})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})
	out := make([]models.AppStatus, 0, limit)
	for i := 0; i < limit && i < len(scored); i++ {
		out = append(out, scored[i].app)
	}
	return out
}

func compactStringSlice(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		if limit == 0 {
			return nil
		}
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}

func compactIncidents(values []models.Incident, limit int) []models.Incident {
	if limit < 0 || len(values) <= limit {
		return append([]models.Incident(nil), values...)
	}
	if limit == 0 {
		return nil
	}
	return append([]models.Incident(nil), values[:limit]...)
}

func compactFacts(values []models.IncidentFact, limit int) []models.IncidentFact {
	if limit < 0 || len(values) <= limit {
		return append([]models.IncidentFact(nil), values...)
	}
	if limit == 0 {
		return nil
	}
	return append([]models.IncidentFact(nil), values[:limit]...)
}

func compactText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func genericPayload(payload map[string]interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func stripGeneralUserOnlyFields(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, key := range []string{"admin_summary", "container_id", "container_name", "image_ref", "web_url", "template_path", "target", "allowed_log_sources", "recent_logs", "last_incident_ids", "llm_visible_admin", "restart_allowed_admin_only", "blacklisted_for_general_users", "blacklisted_for_automatic_llm", "admin_allows_blacklisted_names", "audit_tail", "llm_policies"} {
			delete(typed, key)
		}
		for key, child := range typed {
			typed[key] = stripGeneralUserOnlyFields(child)
		}
		return typed
	case []interface{}:
		for i, child := range typed {
			typed[i] = stripGeneralUserOnlyFields(child)
		}
		return typed
	default:
		return typed
	}
}

func Instructions() string {
	return strings.Join([]string{
		"You are a diagnostic assistant for a local NoobBoard dashboard.",
		"You receive only sanitized incident facts, status data, and policy-approved log excerpts.",
		"Never claim you can repair the system or execute actions.",
		"Never recommend destructive storage, Unraid array, Docker removal, firewall, VLAN, or filesystem actions.",
		"Choose recommended_action_id only from the JSON schema enum.",
		"Set recommended_action_target.kind to app with the exact app_id or display name for app-specific recommendations; otherwise use none, server, network, storage, or manual.",
		"Return a single JSON object that matches the schema exactly.",
	}, "\n")
}

func AgentInstructions() string {
	return strings.Join([]string{
		"You are a diagnostic assistant for a local NoobBoard dashboard.",
		"You receive only sanitized incident facts, status data, policy-approved log excerpts, and optionally read-only NoobBoard status tools.",
		"You may call the provided NoobBoard tools only to refresh live read-only status.",
		"Never claim you can repair the system unless a separate explicit mutating tool is provided; no mutating tools are available in this request.",
		"Never request shell access, filesystem access, raw credentials, Docker control, Unraid mutations, UniFi configuration changes, or arbitrary local/API commands.",
		"Choose recommended_action_id only from the JSON schema enum.",
		"Set recommended_action_target.kind to app with the exact app_id or display name for app-specific recommendations; otherwise use none, server, network, storage, or manual.",
		"Return a single JSON object that matches the schema exactly after any needed tool calls.",
	}, "\n")
}

func BuildPrompt(contextText string) string {
	return fmt.Sprintf("Sanitized diagnostic context:\n%s", contextText)
}
