package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Mode     Mode
	Policy   models.LLMPolicy
	Snapshot models.Snapshot
	Question string
	ActorID  string
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

	payload := map[string]interface{}{
		"mode":        req.Mode,
		"question":    req.Question,
		"api_report":  buildAPIReport(snapshot),
		"snapshot":    snapshot,
		"instruction": "Diagnose the server status data. Do not request tools, do not execute actions, and recommend only one allowlisted next action.",
	}
	if req.Policy.RecipientRole != models.RoleAdmin {
		generic, err := genericPayload(payload)
		if err != nil {
			return "", err
		}
		payload = stripGeneralUserOnlyFields(generic).(map[string]interface{})
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	result := b.redactor.RedactString(string(data))
	if result.Changed && req.Policy.FailClosedOnRedaction {
		data = []byte(result.Text)
	}
	if len(data) > req.Policy.MaxContextBytes {
		return "", fmt.Errorf("llm context exceeds max_context_bytes: %d > %d", len(data), req.Policy.MaxContextBytes)
	}
	if strings.Contains(string(data), "replace_me") {
		return "", errors.New("placeholder secret reached llm context")
	}
	return string(data), nil
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
		"Return a single JSON object that matches the schema exactly.",
	}, "\n")
}

func BuildPrompt(contextText string) string {
	return fmt.Sprintf("Sanitized diagnostic context:\n%s", contextText)
}
