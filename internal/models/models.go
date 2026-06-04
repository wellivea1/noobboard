package models

import "time"

type Role string

const (
	RoleAdmin       Role = "admin"
	RoleGeneralUser Role = "general_user"
)

type CurrentStatus string

const (
	StatusOnline   CurrentStatus = "online"
	StatusDegraded CurrentStatus = "degraded"
	StatusOffline  CurrentStatus = "offline"
	StatusUnknown  CurrentStatus = "unknown"
	StatusHidden   CurrentStatus = "hidden"
)

type StatusSubjectType string

const (
	SubjectApp   StatusSubjectType = "app"
	SubjectInfra StatusSubjectType = "infra"
)

type Severity string

const (
	SeverityNone     Severity = "none"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type ProbeType string

const (
	ProbeDockerState  ProbeType = "docker_state"
	ProbeDockerHealth ProbeType = "docker_health"
	ProbeHTTP         ProbeType = "http"
	ProbeTCP          ProbeType = "tcp"
	ProbeCustom       ProbeType = "custom"
)

type DockerState string

const (
	DockerRunning DockerState = "running"
	DockerExited  DockerState = "exited"
	DockerUnknown DockerState = "unknown"
)

type DockerHealth string

const (
	HealthHealthy   DockerHealth = "healthy"
	HealthUnhealthy DockerHealth = "unhealthy"
	HealthNone      DockerHealth = "none"
	HealthUnknown   DockerHealth = "unknown"
)

type EndpointStatus string

const (
	EndpointOK      EndpointStatus = "ok"
	EndpointFailed  EndpointStatus = "failed"
	EndpointSkipped EndpointStatus = "skipped"
	EndpointUnknown EndpointStatus = "unknown"
)

type AppStatus struct {
	AppID                       string         `json:"app_id"`
	DisplayName                 string         `json:"display_name"`
	ContainerID                 string         `json:"container_id,omitempty"`
	ContainerName               string         `json:"container_name"`
	Category                    string         `json:"category"`
	IconURL                     string         `json:"icon_url,omitempty"`
	IconSource                  string         `json:"icon_source,omitempty"`
	ImageRef                    string         `json:"image_ref,omitempty"`
	WebURL                      string         `json:"web_url,omitempty"`
	TemplatePath                string         `json:"template_path,omitempty"`
	DataSource                  string         `json:"data_source,omitempty"`
	VisibleToGeneralUsers       bool           `json:"visible_to_general_users"`
	ProbeType                   ProbeType      `json:"probe_type"`
	DockerState                 DockerState    `json:"docker_state"`
	DockerHealth                DockerHealth   `json:"docker_health"`
	EndpointStatus              EndpointStatus `json:"endpoint_status"`
	LastSeenOnline              *time.Time     `json:"last_seen_online,omitempty"`
	LastSeenOffline             *time.Time     `json:"last_seen_offline,omitempty"`
	CurrentStatus               CurrentStatus  `json:"current_status"`
	Severity                    Severity       `json:"severity"`
	ServerSummary               string         `json:"server_summary"`
	AdminSummary                string         `json:"admin_summary"`
	AllowedLogSources           []string       `json:"allowed_log_sources"`
	LLMVisibleAdmin             bool           `json:"llm_visible_admin"`
	LLMVisibleGeneral           bool           `json:"llm_visible_general"`
	NotificationOptInAllowed    bool           `json:"notification_opt_in_allowed"`
	RestartAllowedAdminOnly     bool           `json:"restart_allowed_admin_only"`
	RestartAllowedGeneralUser   bool           `json:"restart_allowed_general_user"`
	AgentRepairAllowed          bool           `json:"agent_repair_allowed,omitempty"`
	SubscriptionCount           int            `json:"subscription_count"`
	RecentLogs                  []LogLine      `json:"recent_logs,omitempty"`
	CurrentProbeResult          ProbeResult    `json:"current_probe_result"`
	LastIncidentIDs             []string       `json:"last_incident_ids,omitempty"`
	BlacklistedForGeneralUsers  bool           `json:"blacklisted_for_general_users"`
	BlacklistedForAutomaticLLM  bool           `json:"blacklisted_for_automatic_llm"`
	AdminAllowsBlacklistedNames bool           `json:"admin_allows_blacklisted_names"`
}

type ProbeResult struct {
	Type      ProbeType `json:"type"`
	Target    string    `json:"target"`
	OK        bool      `json:"ok"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
	LatencyMS int64     `json:"latency_ms"`
}

type LogLine struct {
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}

type InfrastructureStatus struct {
	InternetReachable       bool         `json:"internet_reachable"`
	DNSOK                   bool         `json:"dns_ok"`
	RouterReachable         bool         `json:"router_reachable"`
	UniFiWANUp              bool         `json:"unifi_wan_up"`
	UniFiGatewayReachable   bool         `json:"unifi_gateway_reachable"`
	UniFiSiteID             string       `json:"unifi_site_id,omitempty"`
	UniFiSiteName           string       `json:"unifi_site_name,omitempty"`
	UniFiDeviceCount        int          `json:"unifi_device_count,omitempty"`
	UniFiOfflineDeviceCount int          `json:"unifi_offline_device_count,omitempty"`
	UniFiClientCount        int          `json:"unifi_client_count,omitempty"`
	UniFiFirmwareUpdates    int          `json:"unifi_firmware_updates,omitempty"`
	UniFiWANCount           int          `json:"unifi_wan_count,omitempty"`
	UniFiWarnings           []string     `json:"unifi_warnings,omitempty"`
	NASReachable            bool         `json:"nas_reachable"`
	NASLinkSpeedMbps        int          `json:"nas_link_speed_mbps"`
	ExpectedNASLinkMbps     int          `json:"expected_nas_link_mbps"`
	UnraidAPIReachable      bool         `json:"unraid_api_reachable"`
	UnraidVersion           string       `json:"unraid_version,omitempty"`
	UnraidUptimeSeconds     int64        `json:"unraid_uptime_seconds,omitempty"`
	UnraidCPUBrand          string       `json:"unraid_cpu_brand,omitempty"`
	UnraidCPUCores          int          `json:"unraid_cpu_cores,omitempty"`
	UnraidCPUThreads        int          `json:"unraid_cpu_threads,omitempty"`
	UnraidMemoryTotalBytes  int64        `json:"unraid_memory_total_bytes,omitempty"`
	UnraidMemoryUsedBytes   int64        `json:"unraid_memory_used_bytes,omitempty"`
	UnraidMemoryUsedPct     float64      `json:"unraid_memory_used_pct,omitempty"`
	UnraidNotificationCount int          `json:"unraid_notification_count,omitempty"`
	UnraidAlertCount        int          `json:"unraid_alert_count,omitempty"`
	UnraidWarningCount      int          `json:"unraid_warning_count,omitempty"`
	UnraidVMCount           int          `json:"unraid_vm_count,omitempty"`
	UnraidVMRunningCount    int          `json:"unraid_vm_running_count,omitempty"`
	UnraidVMStoppedCount    int          `json:"unraid_vm_stopped_count,omitempty"`
	UnraidVMNames           []string     `json:"unraid_vm_names,omitempty"`
	UnraidShareCount        int          `json:"unraid_share_count,omitempty"`
	UnraidShareNames        []string     `json:"unraid_share_names,omitempty"`
	UnraidArrayState        string       `json:"unraid_array_state"`
	UnraidArrayHealthy      bool         `json:"unraid_array_healthy"`
	ArrayDiskCount          int          `json:"array_disk_count,omitempty"`
	ArrayDiskWarningCount   int          `json:"array_disk_warning_count,omitempty"`
	ArrayCapacityTotalBytes int64        `json:"array_capacity_total_bytes,omitempty"`
	ArrayCapacityUsedBytes  int64        `json:"array_capacity_used_bytes,omitempty"`
	ArrayCapacityFreeBytes  int64        `json:"array_capacity_free_bytes,omitempty"`
	ArrayCapacityUsedPct    float64      `json:"array_capacity_used_pct,omitempty"`
	DockerServiceAvailable  bool         `json:"docker_service_available"`
	DockerNetworkCount      int          `json:"docker_network_count,omitempty"`
	DockerNetworkNames      []string     `json:"docker_network_names,omitempty"`
	StorageWarnings         []string     `json:"storage_warnings,omitempty"`
	ParityCheckState        string       `json:"parity_check_state,omitempty"`
	LastCheckedAt           time.Time    `json:"last_checked_at"`
	SourceHealth            SourceHealth `json:"source_health"`
}

type SourceHealth struct {
	Unraid string `json:"unraid"`
	Docker string `json:"docker"`
	UniFi  string `json:"unifi"`
	Probes string `json:"probes"`
}

type Snapshot struct {
	GeneratedAt          time.Time            `json:"generated_at"`
	OverallStatus        CurrentStatus        `json:"overall_status"`
	ServerSummary        string               `json:"server_summary"`
	AdminSummary         string               `json:"admin_summary"`
	Infrastructure       InfrastructureStatus `json:"infrastructure"`
	Apps                 []AppStatus          `json:"apps"`
	Incidents            []Incident           `json:"incidents"`
	Facts                []IncidentFact       `json:"facts"`
	Visibility           VisibilitySettings   `json:"visibility"`
	LLMPolicies          map[string]LLMPolicy `json:"llm_policies,omitempty"`
	DiagnosticsAvailable bool                 `json:"diagnostics_available"`
	DiagnosticsProvider  string               `json:"diagnostics_provider,omitempty"`
	NotificationInfo     NotificationRollup   `json:"notification_info"`
	AuditTail            []AuditEntry         `json:"audit_tail,omitempty"`
	IntegrationMode      string               `json:"integration_mode,omitempty"`
	FixtureScenario      string               `json:"fixture_scenario,omitempty"`
}

type VisibilitySettings struct {
	HiddenAppIDs           []string         `json:"hidden_app_ids"`
	HiddenContainerNames   []string         `json:"hidden_container_names"`
	DefaultRole            Role             `json:"default_role,omitempty"`
	Roles                  []RoleVisibility `json:"roles,omitempty"`
	GeneralUserCanUseLLM   bool             `json:"general_user_can_use_llm"`
	ShowNASStatusToUsers   bool             `json:"show_nas_status_to_users"`
	ShowWANStatusToUsers   bool             `json:"show_wan_status_to_users"`
	ShowIncidentIDsToUsers bool             `json:"show_incident_ids_to_users"`
}

type RoleVisibility struct {
	Role                   Role     `json:"role"`
	DisplayName            string   `json:"display_name"`
	CanUseLLM              bool     `json:"can_use_llm"`
	ShowNASStatusToUsers   bool     `json:"show_nas_status_to_users"`
	ShowWANStatusToUsers   bool     `json:"show_wan_status_to_users"`
	ShowIncidentIDsToUsers bool     `json:"show_incident_ids_to_users"`
	HiddenAppIDs           []string `json:"hidden_app_ids"`
	HiddenContainerNames   []string `json:"hidden_container_names"`
}

type LLMPolicy struct {
	Name                  string             `json:"name"`
	Enabled               bool               `json:"enabled"`
	IncludeLogs           bool               `json:"include_logs"`
	PreferIncidentFacts   bool               `json:"prefer_incident_facts"`
	AllowedLogSources     []string           `json:"allowed_log_sources"`
	AllowHiddenAppNames   bool               `json:"allow_hidden_app_names"`
	AllowBlacklistedNames bool               `json:"allow_blacklisted_names"`
	MaxContextBytes       int                `json:"max_context_bytes"`
	MaxLogLines           int                `json:"max_log_lines"`
	FailClosedOnRedaction bool               `json:"fail_closed_on_redaction"`
	RecipientRole         Role               `json:"recipient_role"`
	AgentToolsEnabled     bool               `json:"agent_tools_enabled"`
	AgentMaxToolCalls     int                `json:"agent_max_tool_calls"`
	AgentToolRules        []LLMAgentToolRule `json:"agent_tool_rules,omitempty"`
}

type LLMAgentToolRule struct {
	Tool   string `json:"tool"`
	Action string `json:"action"`
}

type IncidentType string

const (
	IncidentInternetDown         IncidentType = "internet_down"
	IncidentNASUnreachable       IncidentType = "nas_unreachable"
	IncidentUnraidAPIUnavailable IncidentType = "unraid_api_unavailable"
	IncidentArrayStopped         IncidentType = "array_stopped"
	IncidentDockerServiceDown    IncidentType = "docker_service_down"
	IncidentAppDown              IncidentType = "app_down"
	IncidentAppDegraded          IncidentType = "app_degraded"
	IncidentStorageWarning       IncidentType = "storage_warning"
	IncidentUnifiIssue           IncidentType = "unifi_issue"
	IncidentDNSIssue             IncidentType = "dns_issue"
	IncidentUnknown              IncidentType = "unknown"
)

type IncidentFact struct {
	ID               string       `json:"id"`
	Type             IncidentType `json:"type"`
	Severity         Severity     `json:"severity"`
	Summary          string       `json:"summary"`
	Evidence         []string     `json:"evidence"`
	AffectedServices []string     `json:"affected_services"`
	CreatedAt        time.Time    `json:"created_at"`
	VisibleToUsers   bool         `json:"visible_to_users"`
}

type Incident struct {
	ID               string        `json:"id"`
	Type             IncidentType  `json:"type"`
	Severity         Severity      `json:"severity"`
	Status           CurrentStatus `json:"status"`
	Summary          string        `json:"summary"`
	AdminSummary     string        `json:"admin_summary"`
	AffectedServices []string      `json:"affected_services"`
	Evidence         []string      `json:"evidence"`
	StartedAt        time.Time     `json:"started_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	ResolvedAt       *time.Time    `json:"resolved_at,omitempty"`
}

type NotificationPreference struct {
	UserID           string    `json:"user_id"`
	AppID            string    `json:"app_id"`
	NotifyOnDown     bool      `json:"notify_on_down"`
	NotifyOnRecovery bool      `json:"notify_on_recovery"`
	QuietHours       string    `json:"quiet_hours,omitempty"`
	LastSentAt       time.Time `json:"last_sent_at,omitempty"`
	LastStatusSeen   string    `json:"last_status_seen,omitempty"`
	DedupeKey        string    `json:"dedupe_key,omitempty"`
}

type NotificationRollup struct {
	Enabled              bool `json:"enabled"`
	GlobalOptInEnabled   bool `json:"global_opt_in_enabled"`
	PendingDedupedEvents int  `json:"pending_deduped_events"`
}

type StatusEvent struct {
	ID          string            `json:"id"`
	SubjectType StatusSubjectType `json:"subject_type"`
	SubjectID   string            `json:"subject_id"`
	DisplayName string            `json:"display_name"`
	From        CurrentStatus     `json:"from"`
	To          CurrentStatus     `json:"to"`
	At          time.Time         `json:"at"`
	Note        string            `json:"note,omitempty"`
}

type StatusHistory struct {
	SubjectType     StatusSubjectType `json:"subject_type"`
	SubjectID       string            `json:"subject_id"`
	DisplayName     string            `json:"display_name"`
	Current         CurrentStatus     `json:"current"`
	LastSeenOnline  *time.Time        `json:"last_seen_online,omitempty"`
	LastSeenOffline *time.Time        `json:"last_seen_offline,omitempty"`
	UptimePct24h    *float64          `json:"uptime_pct_24h,omitempty"`
	UptimePct7d     *float64          `json:"uptime_pct_7d,omitempty"`
	Events          []StatusEvent     `json:"events"`
}

type AuditEntry struct {
	ID       string                 `json:"id"`
	Time     time.Time              `json:"time"`
	Actor    string                 `json:"actor"`
	Action   string                 `json:"action"`
	Redacted bool                   `json:"redacted"`
	Details  map[string]interface{} `json:"details"`
}
