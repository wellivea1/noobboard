package config

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

type Config struct {
	Server          ServerConfig
	Database        DatabaseConfig
	Auth            AuthConfig
	Visibility      models.VisibilitySettings
	Privacy         PrivacyConfig
	AppCatalog      AppCatalogConfig
	Notifications   NotificationConfig
	LLM             LLMConfig
	Integrations    IntegrationConfig
	FixtureDir      string
	FixtureScenario string
	Polling         PollingConfig
	Retention       RetentionConfig
}

type ServerConfig struct {
	BindAddress    string
	Port           int
	CompactPort    int
	PublicURL      string
	AllowedOrigins []string
}

func (s ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", s.BindAddress, s.Port)
}

func (s ServerConfig) CompactAddress() string {
	return fmt.Sprintf("%s:%d", s.BindAddress, s.CompactPort)
}

type DatabaseConfig struct {
	Path string
}

type AuthConfig struct {
	BootstrapAdminUsername string
	BootstrapAdminPassword string
	SessionTimeout         time.Duration
	RememberSessionTimeout time.Duration
	CookieSecure           bool
	AllowInsecureRemote    bool
}

type PrivacyConfig struct {
	BlacklistAppIDs         []string `json:"blacklist_app_ids"`
	BlacklistContainerNames []string `json:"blacklist_container_names"`
	BlacklistDisplayNames   []string `json:"blacklist_display_names"`
	BlacklistFolderPaths    []string `json:"blacklist_folder_paths"`
	BlacklistShareNames     []string `json:"blacklist_share_names"`
	BlacklistFilePaths      []string `json:"blacklist_file_paths"`
	BlacklistFilenameGlobs  []string `json:"blacklist_filename_globs"`
	BlacklistLogPatterns    []string `json:"blacklist_log_patterns"`
	BlacklistEnvNames       []string `json:"blacklist_env_names"`
	BlacklistURLPatterns    []string `json:"blacklist_url_patterns"`
	BlacklistHostnames      []string `json:"blacklist_hostnames"`
	BlacklistIPs            []string `json:"blacklist_ips"`
	BlacklistUsernames      []string `json:"blacklist_usernames"`
	RedactIPs               bool     `json:"redact_ips"`
	RedactHostnames         bool     `json:"redact_hostnames"`
	RedactEmails            bool     `json:"redact_emails"`
}

type AppCatalogConfig struct {
	IconOverrides                map[string]string `json:"icon_overrides"`
	AgentRepairAllowed           map[string]bool   `json:"agent_repair_allowed,omitempty"`
	GeneralUserRestartsEnabled   bool              `json:"general_user_restarts_enabled,omitempty"`
	GeneralUserAutoRepairEnabled bool              `json:"general_user_auto_repair_enabled,omitempty"`
	RestartAllowedGeneralUser    map[string]bool   `json:"restart_allowed_general_user,omitempty"`
}

type NotificationConfig struct {
	Enabled             bool          `json:"enabled"`
	GlobalOptInEnabled  bool          `json:"global_opt_in_enabled"`
	Backend             string        `json:"backend"`
	RateLimitWindow     time.Duration `json:"rate_limit_window"`
	WholeOutageDeduping bool          `json:"whole_outage_deduping"`
}

type LLMConfig struct {
	Enabled                        bool                        `json:"enabled"`
	Provider                       string                      `json:"provider"`
	OpenAIAuthMethod               string                      `json:"openai_auth_method"`
	OpenAIAPIKey                   string                      `json:"openai_api_key,omitempty"`
	OpenAIModel                    string                      `json:"openai_model"`
	ChatGPTRefreshToken            string                      `json:"chatgpt_refresh_token,omitempty"`
	ChatGPTAccessToken             string                      `json:"chatgpt_access_token,omitempty"`
	ChatGPTTokenExpiresAt          time.Time                   `json:"chatgpt_token_expires_at,omitempty"`
	ChatGPTAccountID               string                      `json:"chatgpt_account_id,omitempty"`
	AnthropicAPIKey                string                      `json:"anthropic_api_key,omitempty"`
	AnthropicModel                 string                      `json:"anthropic_model"`
	Timeout                        time.Duration               `json:"timeout"`
	AgentControlEnabled            bool                        `json:"agent_control_enabled"`
	AgentAutoRepairEnabled         bool                        `json:"agent_auto_repair_enabled"`
	AgentArmDuration               time.Duration               `json:"agent_arm_duration"`
	ActionAutoReviewEnabled        bool                        `json:"action_auto_review_enabled"`
	ActionAutoReviewModel          string                      `json:"action_auto_review_model,omitempty"`
	ActionAutoReviewReasoning      string                      `json:"action_auto_review_reasoning,omitempty"`
	ActionAutoReviewReferencePaths []string                    `json:"action_auto_review_reference_paths,omitempty"`
	Policies                       map[string]models.LLMPolicy `json:"policies"`
}

const (
	OpenAIAuthMethodAPIKey          = "api_key"
	OpenAIAuthMethodChatGPTBrowser  = "chatgpt_browser"
	OpenAIAuthMethodChatGPTHeadless = "chatgpt_headless"
)

type IntegrationConfig struct {
	Mode                string `json:"mode"`
	UnraidBaseURL       string `json:"unraid_base_url"`
	UnraidAPIKey        string `json:"unraid_api_key,omitempty"`
	UnraidAPIKeyFile    string `json:"unraid_api_key_file,omitempty"`
	UnraidSSHFallback   bool   `json:"unraid_ssh_fallback"`
	UnraidSSHHost       string `json:"unraid_ssh_host,omitempty"`
	UnraidSSHPort       int    `json:"unraid_ssh_port"`
	UnraidSSHUser       string `json:"unraid_ssh_user,omitempty"`
	UnraidSSHKeyFile    string `json:"unraid_ssh_key_file,omitempty"`
	UnraidSSHCommand    string `json:"unraid_ssh_command,omitempty"`
	UniFiBaseURL        string `json:"unifi_base_url"`
	UniFiAPIKey         string `json:"unifi_api_key,omitempty"`
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

type PollingConfig struct {
	Interval time.Duration
}

type RetentionConfig struct {
	MaxAuditEntries           int
	MaxNotificationHistory    int
	MaxLogLinesPerSource      int
	MaxIncidentAge            time.Duration
	MaxStatusEventAge         time.Duration
	MaxStatusEventsPerSubject int
	MaxLatencyBucketAge       time.Duration
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		path = defaultConfigPath()
	}
	if _, err := os.Stat(path); err == nil {
		if err := applySimpleConfigFile(&cfg, path); err != nil {
			return Config{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	applyEnv(&cfg)
	if err := applySecretFiles(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Defaults() Config {
	base := defaultBaseDir()
	return Config{
		Server: ServerConfig{
			BindAddress: "127.0.0.1",
			Port:        8787,
			CompactPort: 8788,
			PublicURL:   "http://127.0.0.1:8787",
		},
		Database: DatabaseConfig{
			Path: filepath.Join(base, "data", "dashboard.db.json"),
		},
		Auth: AuthConfig{
			BootstrapAdminUsername: "admin",
			BootstrapAdminPassword: "",
			SessionTimeout:         12 * time.Hour,
			RememberSessionTimeout: 10 * 365 * 24 * time.Hour,
			CookieSecure:           false,
		},
		Visibility: models.VisibilitySettings{
			DefaultRole:          models.RoleGeneralUser,
			GeneralUserCanUseLLM: true,
			ShowNASStatusToUsers: true,
			ShowWANStatusToUsers: true,
		},
		Privacy: PrivacyConfig{
			BlacklistEnvNames: []string{"*_KEY", "*_TOKEN", "*PASSWORD*", "AUTHORIZATION", "COOKIE"},
			RedactEmails:      true,
		},
		AppCatalog: AppCatalogConfig{
			IconOverrides:             map[string]string{},
			AgentRepairAllowed:        map[string]bool{},
			RestartAllowedGeneralUser: map[string]bool{},
		},
		Notifications: NotificationConfig{
			Enabled:             true,
			GlobalOptInEnabled:  true,
			Backend:             "mock",
			RateLimitWindow:     15 * time.Minute,
			WholeOutageDeduping: true,
		},
		LLM: LLMConfig{
			Enabled:               true,
			Provider:              "disabled",
			OpenAIAuthMethod:      OpenAIAuthMethodAPIKey,
			OpenAIModel:           "gpt-5.6-terra",
			AnthropicModel:        "claude-opus-5",
			Timeout:               45 * time.Second,
			AgentArmDuration:      10 * time.Minute,
			ActionAutoReviewModel: "same",
			ActionAutoReviewReferencePaths: []string{
				"docs/feature-app-detail-history.md",
				"docs/security.md",
				"docs/llm-policy.md",
			},
			Policies: defaultLLMPolicies(),
		},
		Integrations: IntegrationConfig{
			Mode:             "live",
			UnraidSSHPort:    22,
			UnraidSSHCommand: "ssh",
			UniFiSiteID:      "default",
			UniFiInsecureTLS: true,
			InternetProbeURL: "https://www.gstatic.com/generate_204",
			DNSProbeHost:     "cloudflare.com",
		},
		FixtureDir:      "fixtures",
		FixtureScenario: "all_systems_online",
		Polling: PollingConfig{
			Interval: 30 * time.Second,
		},
		Retention: RetentionConfig{
			MaxAuditEntries:           1000,
			MaxNotificationHistory:    1000,
			MaxLogLinesPerSource:      200,
			MaxIncidentAge:            30 * 24 * time.Hour,
			MaxStatusEventAge:         90 * 24 * time.Hour,
			// A fortnight of 5-minute buckets is ~4k rows per probe: enough to
			// see a weekly pattern and a bad night, small enough to load and
			// chart without paging.
			MaxLatencyBucketAge: 14 * 24 * time.Hour,
			MaxStatusEventsPerSubject: 500,
		},
	}
}

func defaultLLMPolicies() map[string]models.LLMPolicy {
	return map[string]models.LLMPolicy{
		"admin_requested": {
			Name:                  "admin_requested",
			Enabled:               true,
			IncludeLogs:           true,
			PreferIncidentFacts:   true,
			AllowHiddenAppNames:   true,
			AllowBlacklistedNames: false,
			MaxContextBytes:       32000,
			MaxLogLines:           80,
			FailClosedOnRedaction: true,
			RecipientRole:         models.RoleAdmin,
			AgentToolsEnabled: true,
			// Diagnosing one failed app is now status -> logs -> history, so a
			// budget of 2 could not complete the workflow the log and history
			// tools exist for. Still a hard cap, and every call is read-only,
			// role-filtered and audited. Existing configs keep their own value;
			// this is the default for new installs.
			AgentMaxToolCalls: 4,
			AgentToolRules: []models.LLMAgentToolRule{
				{Tool: "noobboard_current_status", Action: "allow"},
				{Tool: "noobboard_server_status", Action: "allow"},
				{Tool: "noobboard_network_status", Action: "allow"},
				{Tool: "noobboard_app_status", Action: "allow"},
				{Tool: "noobboard_app_logs", Action: "allow"},
				{Tool: "noobboard_app_history", Action: "allow"},
			},
		},
		"general_user_requested": {
			Name:                  "general_user_requested",
			Enabled:               true,
			IncludeLogs:           false,
			PreferIncidentFacts:   true,
			AllowHiddenAppNames:   false,
			AllowBlacklistedNames: false,
			MaxContextBytes:       12000,
			MaxLogLines:           0,
			FailClosedOnRedaction: true,
			RecipientRole:         models.RoleGeneralUser,
			AgentToolsEnabled:     false,
			AgentMaxToolCalls:     0,
		},
		"automatic_incident": {
			Name:                  "automatic_incident",
			Enabled:               true,
			IncludeLogs:           false,
			PreferIncidentFacts:   true,
			AllowHiddenAppNames:   false,
			AllowBlacklistedNames: false,
			MaxContextBytes:       16000,
			MaxLogLines:           20,
			FailClosedOnRedaction: true,
			RecipientRole:         models.RoleAdmin,
			AgentToolsEnabled:     false,
			AgentMaxToolCalls:     0,
		},
		"notification_message": {
			Name:                  "notification_message",
			Enabled:               true,
			IncludeLogs:           false,
			PreferIncidentFacts:   true,
			AllowHiddenAppNames:   false,
			AllowBlacklistedNames: false,
			MaxContextBytes:       8000,
			MaxLogLines:           0,
			FailClosedOnRedaction: true,
			RecipientRole:         models.RoleAdmin,
			AgentToolsEnabled:     false,
			AgentMaxToolCalls:     0,
		},
	}
}

func (c Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server port %d is invalid", c.Server.Port)
	}
	if c.Server.CompactPort <= 0 || c.Server.CompactPort > 65535 {
		return fmt.Errorf("server compact_port %d is invalid", c.Server.CompactPort)
	}
	if c.Server.CompactPort == c.Server.Port {
		return errors.New("server compact_port must be different from server port")
	}
	if strings.TrimSpace(c.Server.BindAddress) == "" {
		return errors.New("server bind address is required")
	}
	if strings.TrimSpace(c.Database.Path) == "" {
		return errors.New("database path is required")
	}
	if c.Auth.SessionTimeout < time.Minute {
		return errors.New("auth session timeout must be at least one minute")
	}
	if c.Auth.RememberSessionTimeout < c.Auth.SessionTimeout {
		return errors.New("auth remember session timeout must be at least the normal session timeout")
	}
	if !c.Auth.AllowInsecureRemote && isRemoteBindAddress(c.Server.BindAddress) && isDefaultPassword(c.Auth.BootstrapAdminPassword) {
		return errors.New("remote bind requires NOOBBOARD_BOOTSTRAP_ADMIN_PASSWORD or auth.allow_insecure_remote for development only")
	}
	if c.Server.PublicURL != "" {
		if _, err := url.ParseRequestURI(c.Server.PublicURL); err != nil {
			return fmt.Errorf("server public_url is invalid: %w", err)
		}
	}
	for _, origin := range c.Server.AllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("server allowed origin %q is invalid", origin)
		}
	}
	if c.Polling.Interval < time.Second {
		return errors.New("polling interval must be at least one second")
	}
	if c.Retention.MaxStatusEventAge <= 0 {
		return errors.New("retention max_status_event_age must be positive")
	}
	if c.Retention.MaxStatusEventsPerSubject <= 0 {
		return errors.New("retention max_status_events_per_subject must be positive")
	}
	if err := validateRoleVisibility(c.Visibility); err != nil {
		return err
	}
	for key, iconURL := range c.AppCatalog.IconOverrides {
		if strings.TrimSpace(key) == "" {
			return errors.New("app icon override key is required")
		}
		if _, err := NormalizeIconURL(iconURL); err != nil {
			return fmt.Errorf("app icon override %s: %w", key, err)
		}
	}
	for key := range c.AppCatalog.AgentRepairAllowed {
		if strings.TrimSpace(key) == "" {
			return errors.New("app automatic repair key is required")
		}
	}
	for key := range c.AppCatalog.RestartAllowedGeneralUser {
		if strings.TrimSpace(key) == "" {
			return errors.New("app general-user restart key is required")
		}
	}
	switch c.Integrations.Mode {
	case "fixture", "mixed", "live":
	default:
		return fmt.Errorf("integration mode %q is invalid", c.Integrations.Mode)
	}
	if err := validateProbeSettings(c.Integrations); err != nil {
		return err
	}
	if err := validateIntegrationBaseURL("unraid_base_url", c.Integrations.UnraidBaseURL); err != nil {
		return err
	}
	if c.Integrations.UnraidSSHFallback {
		if strings.TrimSpace(c.Integrations.UnraidSSHHost) == "" {
			return errors.New("unraid ssh fallback requires integrations.unraid_ssh_host")
		}
		if strings.TrimSpace(c.Integrations.UnraidSSHUser) == "" {
			return errors.New("unraid ssh fallback requires integrations.unraid_ssh_user")
		}
	}
	if c.Integrations.UnraidSSHPort <= 0 || c.Integrations.UnraidSSHPort > 65535 {
		return fmt.Errorf("unraid ssh port %d is invalid", c.Integrations.UnraidSSHPort)
	}
	if strings.TrimSpace(c.Integrations.UnraidSSHCommand) == "" {
		return errors.New("unraid ssh command is required")
	}
	if err := validateIntegrationBaseURL("unifi_base_url", c.Integrations.UniFiBaseURL); err != nil {
		return err
	}
	if c.Integrations.ExpectedNASLinkMbps < 0 || c.Integrations.ExpectedNASLinkMbps > 100000 {
		return fmt.Errorf("expected NAS link Mbps %d is invalid", c.Integrations.ExpectedNASLinkMbps)
	}
	switch c.LLM.Provider {
	case "disabled", "openai", "anthropic":
	default:
		return fmt.Errorf("llm provider %q is invalid", c.LLM.Provider)
	}
	switch strings.TrimSpace(c.LLM.OpenAIAuthMethod) {
	case "", OpenAIAuthMethodAPIKey, OpenAIAuthMethodChatGPTBrowser, OpenAIAuthMethodChatGPTHeadless:
	default:
		return fmt.Errorf("openai auth method %q is invalid", c.LLM.OpenAIAuthMethod)
	}
	if c.LLM.AgentArmDuration <= 0 {
		return errors.New("llm agent_arm_duration must be positive")
	}
	if c.LLM.AgentArmDuration > time.Hour {
		return errors.New("llm agent_arm_duration must not exceed 1h")
	}
	if err := validateActionAutoReview(c.LLM); err != nil {
		return err
	}
	for name, policy := range c.LLM.Policies {
		if policy.MaxContextBytes <= 0 {
			return fmt.Errorf("llm policy %s must have max_context_bytes", name)
		}
		if !policy.FailClosedOnRedaction {
			return fmt.Errorf("llm policy %s must fail closed on redaction", name)
		}
		if policy.AgentToolsEnabled {
			if policy.RecipientRole != models.RoleAdmin {
				return fmt.Errorf("llm policy %s cannot enable agent tools for non-admin recipients", name)
			}
			if policy.AgentMaxToolCalls <= 0 {
				return fmt.Errorf("llm policy %s must set agent_max_tool_calls when agent tools are enabled", name)
			}
		}
		for _, rule := range policy.AgentToolRules {
			if strings.TrimSpace(rule.Tool) == "" {
				return fmt.Errorf("llm policy %s has an agent tool rule without a tool name", name)
			}
			switch strings.TrimSpace(rule.Action) {
			case "allow", "ask", "deny":
			default:
				return fmt.Errorf("llm policy %s has invalid agent tool action %q", name, rule.Action)
			}
		}
	}
	return nil
}

func validateActionAutoReview(settings LLMConfig) error {
	model := strings.TrimSpace(settings.ActionAutoReviewModel)
	if model == "" || model == "same" {
		// Empty is normalized to "same"; accepting both keeps older runtime settings valid.
	} else {
		parts := strings.SplitN(model, "/", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return fmt.Errorf("llm action_auto_review_model %q must be same or provider/model", model)
		}
		switch strings.TrimSpace(parts[0]) {
		case "openai", "anthropic", "chatgpt":
		default:
			return fmt.Errorf("llm action_auto_review_model provider %q is invalid", parts[0])
		}
	}
	switch strings.TrimSpace(settings.ActionAutoReviewReasoning) {
	case "", "low", "medium", "high", "xhigh":
	default:
		return fmt.Errorf("llm action_auto_review_reasoning %q is invalid", settings.ActionAutoReviewReasoning)
	}
	for _, path := range settings.ActionAutoReviewReferencePaths {
		if strings.ContainsAny(path, "\x00\r\n") {
			return errors.New("llm action_auto_review_reference_paths contains an invalid path")
		}
	}
	return nil
}

func validateIntegrationBaseURL(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s %q is invalid", label, value)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s %q must use http or https", label, value)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not contain credentials", label)
	}
	return nil
}

func normalizeIntegrationBaseURL(value, defaultScheme string) string {
	normalized, err := NormalizeIntegrationBaseURL(value, defaultScheme)
	if err != nil {
		return strings.TrimRight(strings.TrimSpace(value), "/")
	}
	return normalized
}

func NormalizeIntegrationBaseURL(value, defaultScheme string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return "", nil
	}
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		value = defaultScheme + "://" + value
	}
	if err := validateIntegrationBaseURL("integration base URL", value); err != nil {
		return "", err
	}
	return value, nil
}

func validateProbeSettings(settings IntegrationConfig) error {
	if settings.InternetProbeURL != "" {
		parsed, err := url.Parse(settings.InternetProbeURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("internet probe URL %q is invalid", settings.InternetProbeURL)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("internet probe URL %q must use http or https", settings.InternetProbeURL)
		}
	}
	for label, value := range map[string]string{
		"dns probe host":      settings.DNSProbeHost,
		"router probe target": settings.RouterProbeTarget,
		"NAS probe target":    settings.NASProbeTarget,
	} {
		if strings.ContainsAny(value, " \t\r\n") {
			return fmt.Errorf("%s cannot contain whitespace", label)
		}
	}
	return nil
}

func validateRoleVisibility(settings models.VisibilitySettings) error {
	seen := map[models.Role]bool{}
	for _, role := range settings.Roles {
		roleName := models.Role(strings.TrimSpace(string(role.Role)))
		if roleName == "" {
			return errors.New("visibility role name is required")
		}
		if roleName == models.RoleAdmin {
			return errors.New("admin role visibility cannot be overridden")
		}
		if strings.ContainsAny(string(roleName), " \t\r\n/\\") {
			return fmt.Errorf("visibility role %q contains unsupported whitespace or slash", roleName)
		}
		if seen[roleName] {
			return fmt.Errorf("visibility role %q is duplicated", roleName)
		}
		seen[roleName] = true
	}
	return nil
}

func isDefaultPassword(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == "change-me-now"
}

func isRemoteBindAddress(value string) bool {
	host := strings.TrimSpace(value)
	if host == "" || host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback()
}

func NormalizeIconURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return value, nil
	}
	if strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return "", fmt.Errorf("image URL %q is invalid", value)
		}
		if parsed.User != nil {
			return "", errors.New("image URL must not contain credentials")
		}
		return value, nil
	}
	return "", fmt.Errorf("image URL must be http, https, or a local app path beginning with /")
}

func defaultBaseDir() string {
	if runtime.GOOS == "windows" {
		if programData := os.Getenv("ProgramData"); programData != "" {
			return filepath.Join(programData, "NoobBoard")
		}
		return filepath.Join(`C:\ProgramData`, "NoobBoard")
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "noobboard")
	}
	return filepath.Join("/var/lib", "noobboard")
}

func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		if programData := os.Getenv("ProgramData"); programData != "" {
			return filepath.Join(programData, "NoobBoard", "config.yaml")
		}
		return filepath.Join(`C:\ProgramData`, "NoobBoard", "config.yaml")
	}
	return filepath.Join("/etc", "noobboard", "config.yaml")
}

func applyEnv(cfg *Config) {
	if v := envValue("NOOBBOARD_BIND_ADDRESS", "HSD_BIND_ADDRESS"); v != "" {
		cfg.Server.BindAddress = v
	}
	if v := envValue("NOOBBOARD_PORT", "HSD_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := envValue("NOOBBOARD_COMPACT_PORT", "HSD_COMPACT_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.CompactPort = port
		}
	}
	if v := envValue("NOOBBOARD_PUBLIC_URL", "HSD_PUBLIC_URL"); v != "" {
		cfg.Server.PublicURL = strings.TrimRight(v, "/")
	}
	if v := envValue("NOOBBOARD_ALLOWED_ORIGINS", "HSD_ALLOWED_ORIGINS"); v != "" {
		cfg.Server.AllowedOrigins = splitList(v)
	}
	if v := envValue("NOOBBOARD_DATABASE_PATH", "HSD_DATABASE_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := envValue("NOOBBOARD_FIXTURE_DIR", "HSD_FIXTURE_DIR"); v != "" {
		cfg.FixtureDir = v
	}
	if v := envValue("NOOBBOARD_FIXTURE_SCENARIO", "HSD_FIXTURE_SCENARIO"); v != "" {
		cfg.FixtureScenario = v
	}
	if v := envValue("NOOBBOARD_BOOTSTRAP_ADMIN_USERNAME", "HSD_BOOTSTRAP_ADMIN_USERNAME"); v != "" {
		cfg.Auth.BootstrapAdminUsername = v
	}
	if v := envValue("NOOBBOARD_BOOTSTRAP_ADMIN_PASSWORD", "HSD_BOOTSTRAP_ADMIN_PASSWORD"); v != "" {
		cfg.Auth.BootstrapAdminPassword = v
	}
	if v := envValue("NOOBBOARD_COOKIE_SECURE", "HSD_COOKIE_SECURE"); v != "" {
		cfg.Auth.CookieSecure = parseBool(v)
	}
	if v := envValue("NOOBBOARD_SESSION_TIMEOUT", "HSD_SESSION_TIMEOUT"); v != "" {
		if duration, err := parseDurationValue(v); err == nil {
			cfg.Auth.SessionTimeout = duration
		}
	}
	if v := envValue("NOOBBOARD_REMEMBER_SESSION_TIMEOUT", "HSD_REMEMBER_SESSION_TIMEOUT"); v != "" {
		if duration, err := parseDurationValue(v); err == nil {
			cfg.Auth.RememberSessionTimeout = duration
		}
	}
	if v := envValue("NOOBBOARD_ALLOW_INSECURE_REMOTE", "HSD_ALLOW_INSECURE_REMOTE"); v != "" {
		cfg.Auth.AllowInsecureRemote = parseBool(v)
	}
	if v := os.Getenv("NOTIFICATION_BACKEND"); v != "" {
		cfg.Notifications.Backend = v
	}
	if v := envValue("NOOBBOARD_LLM_PROVIDER", "HSD_LLM_PROVIDER"); v != "" {
		cfg.LLM.Provider = v
	}
	if v := envValue("NOOBBOARD_OPENAI_AUTH_METHOD", "OPENAI_AUTH_METHOD"); v != "" {
		cfg.LLM.OpenAIAuthMethod = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.LLM.OpenAIAPIKey = v
	}
	if v := os.Getenv("OPENAI_MODEL"); v != "" {
		cfg.LLM.OpenAIModel = v
	}
	if v := envValue("NOOBBOARD_CHATGPT_REFRESH_TOKEN", "CHATGPT_REFRESH_TOKEN"); v != "" {
		cfg.LLM.ChatGPTRefreshToken = v
	}
	if v := envValue("NOOBBOARD_CHATGPT_ACCESS_TOKEN", "CHATGPT_ACCESS_TOKEN"); v != "" {
		cfg.LLM.ChatGPTAccessToken = v
	}
	if v := envValue("NOOBBOARD_CHATGPT_ACCOUNT_ID", "CHATGPT_ACCOUNT_ID"); v != "" {
		cfg.LLM.ChatGPTAccountID = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		cfg.LLM.AnthropicAPIKey = v
	}
	if v := os.Getenv("ANTHROPIC_MODEL"); v != "" {
		cfg.LLM.AnthropicModel = v
	}
	if v := envValue("NOOBBOARD_ACTION_AUTO_REVIEW_ENABLED", "HSD_ACTION_AUTO_REVIEW_ENABLED"); v != "" {
		cfg.LLM.ActionAutoReviewEnabled = parseBool(v)
	}
	if v := envValue("NOOBBOARD_AGENT_CONTROL_ENABLED", "HSD_AGENT_CONTROL_ENABLED"); v != "" {
		cfg.LLM.AgentControlEnabled = parseBool(v)
	}
	if v := envValue("NOOBBOARD_AGENT_AUTO_REPAIR_ENABLED", "HSD_AGENT_AUTO_REPAIR_ENABLED"); v != "" {
		cfg.LLM.AgentAutoRepairEnabled = parseBool(v)
	}
	if v := envValue("NOOBBOARD_ACTION_AUTO_REVIEW_MODEL", "HSD_ACTION_AUTO_REVIEW_MODEL"); v != "" {
		cfg.LLM.ActionAutoReviewModel = v
	}
	if v := envValue("NOOBBOARD_ACTION_AUTO_REVIEW_REASONING", "HSD_ACTION_AUTO_REVIEW_REASONING"); v != "" {
		cfg.LLM.ActionAutoReviewReasoning = v
	}
	if v := envValue("NOOBBOARD_ACTION_AUTO_REVIEW_REFERENCES", "HSD_ACTION_AUTO_REVIEW_REFERENCES"); v != "" {
		cfg.LLM.ActionAutoReviewReferencePaths = splitList(v)
	}
	if v := envValue("NOOBBOARD_INTEGRATION_MODE", "HSD_INTEGRATION_MODE"); v != "" {
		cfg.Integrations.Mode = v
	}
	if v := os.Getenv("UNRAID_BASE_URL"); v != "" {
		cfg.Integrations.UnraidBaseURL = normalizeIntegrationBaseURL(v, "http")
	}
	if v := os.Getenv("UNRAID_API_KEY"); v != "" {
		cfg.Integrations.UnraidAPIKey = v
	}
	if v := os.Getenv("UNRAID_API_KEY_FILE"); v != "" {
		cfg.Integrations.UnraidAPIKeyFile = v
	}
	if v := os.Getenv("UNRAID_SSH_FALLBACK_ENABLED"); v != "" {
		cfg.Integrations.UnraidSSHFallback = parseBool(v)
	}
	if v := os.Getenv("UNRAID_SSH_HOST"); v != "" {
		cfg.Integrations.UnraidSSHHost = v
	}
	if v := os.Getenv("UNRAID_SSH_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Integrations.UnraidSSHPort = port
		}
	}
	if v := os.Getenv("UNRAID_SSH_USER"); v != "" {
		cfg.Integrations.UnraidSSHUser = v
	}
	if v := os.Getenv("UNRAID_SSH_KEY_FILE"); v != "" {
		cfg.Integrations.UnraidSSHKeyFile = v
	}
	if v := os.Getenv("UNRAID_SSH_COMMAND"); v != "" {
		cfg.Integrations.UnraidSSHCommand = v
	}
	if v := os.Getenv("UNIFI_BASE_URL"); v != "" {
		cfg.Integrations.UniFiBaseURL = normalizeIntegrationBaseURL(v, "https")
	}
	if v := os.Getenv("UNIFI_API_KEY"); v != "" {
		cfg.Integrations.UniFiAPIKey = v
	}
	if v := os.Getenv("UNIFI_API_KEY_FILE"); v != "" {
		cfg.Integrations.UniFiAPIKeyFile = v
	}
	if v := os.Getenv("UNIFI_SITE_ID"); v != "" {
		cfg.Integrations.UniFiSiteID = v
	}
	if v := envValue("NOOBBOARD_UNIFI_NAS_CLIENT_HINT", "HSD_UNIFI_NAS_CLIENT_HINT"); v != "" {
		cfg.Integrations.UniFiNASClientHint = v
	}
	if v := envValue("NOOBBOARD_EXPECTED_NAS_LINK_MBPS", "HSD_EXPECTED_NAS_LINK_MBPS"); v != "" {
		if mbps, err := strconv.Atoi(v); err == nil {
			cfg.Integrations.ExpectedNASLinkMbps = mbps
		}
	}
	if v := envValue("NOOBBOARD_INTERNET_PROBE_URL", "HSD_INTERNET_PROBE_URL"); v != "" {
		cfg.Integrations.InternetProbeURL = strings.TrimRight(v, "/")
	}
	if v := envValue("NOOBBOARD_DNS_PROBE_HOST", "HSD_DNS_PROBE_HOST"); v != "" {
		cfg.Integrations.DNSProbeHost = v
	}
	if v := envValue("NOOBBOARD_ROUTER_PROBE_TARGET", "HSD_ROUTER_PROBE_TARGET"); v != "" {
		cfg.Integrations.RouterProbeTarget = strings.TrimRight(v, "/")
	}
	if v := envValue("NOOBBOARD_NAS_PROBE_TARGET", "HSD_NAS_PROBE_TARGET"); v != "" {
		cfg.Integrations.NASProbeTarget = strings.TrimRight(v, "/")
	}
	if v := envValue("NOOBBOARD_MAX_STATUS_EVENT_AGE", "HSD_MAX_STATUS_EVENT_AGE"); v != "" {
		if duration, err := parseDurationValue(v); err == nil {
			cfg.Retention.MaxStatusEventAge = duration
		}
	}
	if v := envValue("NOOBBOARD_MAX_STATUS_EVENTS_PER_SUBJECT", "HSD_MAX_STATUS_EVENTS_PER_SUBJECT"); v != "" {
		if count, err := strconv.Atoi(v); err == nil {
			cfg.Retention.MaxStatusEventsPerSubject = count
		}
	}
}

func envValue(primary string, aliases ...string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	for _, alias := range aliases {
		if v := os.Getenv(alias); v != "" {
			return v
		}
	}
	return ""
}

func applySecretFiles(cfg *Config) error {
	if strings.TrimSpace(cfg.Integrations.UnraidAPIKeyFile) != "" {
		secret, err := ReadSecretFile(cfg.Integrations.UnraidAPIKeyFile)
		if err != nil {
			return fmt.Errorf("unraid api key file: %w", err)
		}
		cfg.Integrations.UnraidAPIKey = secret
	}
	if strings.TrimSpace(cfg.Integrations.UniFiAPIKeyFile) != "" {
		secret, err := ReadSecretFile(cfg.Integrations.UniFiAPIKeyFile)
		if err != nil {
			return fmt.Errorf("unifi api key file: %w", err)
		}
		cfg.Integrations.UniFiAPIKey = secret
	}
	return nil
}

func ReadSecretFile(path string) (string, error) {
	return readSecretFile(path)
}

func readSecretFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var values []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if token := secretLineValue(line); token != "" {
			values = append(values, token)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", errors.New("file contains no secret value")
	}
	if len(values) > 1 {
		return "", errors.New("file must contain exactly one non-comment secret value")
	}
	return values[0], nil
}

func secretLineValue(line string) string {
	for _, delimiter := range []string{"=", ":"} {
		if idx := strings.Index(line, delimiter); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			if isConfigLikeKey(key) {
				return strings.Trim(strings.TrimSpace(line[idx+1:]), `"`)
			}
		}
	}
	return strings.Trim(strings.TrimSpace(line), `"`)
}

func isConfigLikeKey(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func applySimpleConfigFile(cfg *Config, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.Contains(line, " ") {
			section = strings.TrimSuffix(line, ":")
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("%s: unsupported config line %q", path, line)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		applyConfigKey(cfg, section, key, value)
	}
	return scanner.Err()
}

func applyConfigKey(cfg *Config, section, key, value string) {
	switch section + "." + key {
	case "server.bind_address":
		cfg.Server.BindAddress = value
	case "server.port":
		if port, err := strconv.Atoi(value); err == nil {
			cfg.Server.Port = port
		}
	case "server.compact_port":
		if port, err := strconv.Atoi(value); err == nil {
			cfg.Server.CompactPort = port
		}
	case "server.public_url":
		cfg.Server.PublicURL = strings.TrimRight(value, "/")
	case "server.allowed_origins":
		cfg.Server.AllowedOrigins = splitList(value)
	case "database.path":
		cfg.Database.Path = value
	case "fixtures.dir":
		cfg.FixtureDir = value
	case "fixtures.scenario":
		cfg.FixtureScenario = value
	case "auth.bootstrap_admin_username":
		cfg.Auth.BootstrapAdminUsername = value
	case "auth.bootstrap_admin_password":
		cfg.Auth.BootstrapAdminPassword = value
	case "auth.session_timeout":
		if duration, err := parseDurationValue(value); err == nil {
			cfg.Auth.SessionTimeout = duration
		}
	case "auth.remember_session_timeout":
		if duration, err := parseDurationValue(value); err == nil {
			cfg.Auth.RememberSessionTimeout = duration
		}
	case "auth.cookie_secure":
		cfg.Auth.CookieSecure = parseBool(value)
	case "auth.allow_insecure_remote":
		cfg.Auth.AllowInsecureRemote = parseBool(value)
	case "notifications.enabled":
		cfg.Notifications.Enabled = parseBool(value)
	case "notifications.global_opt_in_enabled":
		cfg.Notifications.GlobalOptInEnabled = parseBool(value)
	case "notifications.backend":
		cfg.Notifications.Backend = value
	case "llm.enabled":
		cfg.LLM.Enabled = parseBool(value)
	case "llm.provider":
		cfg.LLM.Provider = value
	case "llm.openai_auth_method":
		cfg.LLM.OpenAIAuthMethod = value
	case "llm.openai_api_key":
		cfg.LLM.OpenAIAPIKey = value
	case "llm.openai_model":
		cfg.LLM.OpenAIModel = value
	case "llm.chatgpt_refresh_token":
		cfg.LLM.ChatGPTRefreshToken = value
	case "llm.chatgpt_access_token":
		cfg.LLM.ChatGPTAccessToken = value
	case "llm.chatgpt_account_id":
		cfg.LLM.ChatGPTAccountID = value
	case "llm.chatgpt_token_expires_at":
		if expiresAt, err := time.Parse(time.RFC3339, value); err == nil {
			cfg.LLM.ChatGPTTokenExpiresAt = expiresAt
		}
	case "llm.anthropic_api_key":
		cfg.LLM.AnthropicAPIKey = value
	case "llm.anthropic_model":
		cfg.LLM.AnthropicModel = value
	case "llm.action_auto_review_enabled":
		cfg.LLM.ActionAutoReviewEnabled = parseBool(value)
	case "llm.agent_control_enabled":
		cfg.LLM.AgentControlEnabled = parseBool(value)
	case "llm.agent_auto_repair_enabled":
		cfg.LLM.AgentAutoRepairEnabled = parseBool(value)
	case "llm.action_auto_review_model":
		cfg.LLM.ActionAutoReviewModel = value
	case "llm.action_auto_review_reasoning":
		cfg.LLM.ActionAutoReviewReasoning = value
	case "llm.action_auto_review_reference_paths":
		cfg.LLM.ActionAutoReviewReferencePaths = splitList(value)
	case "app_catalog.agent_repair_allowed":
		cfg.AppCatalog.AgentRepairAllowed = splitBoolMap(value)
	case "app_catalog.general_user_restarts_enabled":
		cfg.AppCatalog.GeneralUserRestartsEnabled = parseBool(value)
	case "app_catalog.general_user_auto_repair_enabled":
		cfg.AppCatalog.GeneralUserAutoRepairEnabled = parseBool(value)
	case "app_catalog.restart_allowed_general_user":
		cfg.AppCatalog.RestartAllowedGeneralUser = splitBoolMap(value)
	case "integrations.mode":
		cfg.Integrations.Mode = value
	case "integrations.unraid_base_url":
		cfg.Integrations.UnraidBaseURL = normalizeIntegrationBaseURL(value, "http")
	case "integrations.unraid_api_key":
		cfg.Integrations.UnraidAPIKey = value
	case "integrations.unraid_api_key_file":
		cfg.Integrations.UnraidAPIKeyFile = value
	case "integrations.unraid_ssh_fallback":
		cfg.Integrations.UnraidSSHFallback = parseBool(value)
	case "integrations.unraid_ssh_host":
		cfg.Integrations.UnraidSSHHost = value
	case "integrations.unraid_ssh_port":
		if port, err := strconv.Atoi(value); err == nil {
			cfg.Integrations.UnraidSSHPort = port
		}
	case "integrations.unraid_ssh_user":
		cfg.Integrations.UnraidSSHUser = value
	case "integrations.unraid_ssh_key_file":
		cfg.Integrations.UnraidSSHKeyFile = value
	case "integrations.unraid_ssh_command":
		cfg.Integrations.UnraidSSHCommand = value
	case "integrations.unifi_base_url":
		cfg.Integrations.UniFiBaseURL = normalizeIntegrationBaseURL(value, "https")
	case "integrations.unifi_api_key":
		cfg.Integrations.UniFiAPIKey = value
	case "integrations.unifi_api_key_file":
		cfg.Integrations.UniFiAPIKeyFile = value
	case "integrations.unifi_site_id":
		cfg.Integrations.UniFiSiteID = value
	case "integrations.unifi_nas_client_hint":
		cfg.Integrations.UniFiNASClientHint = value
	case "integrations.expected_nas_link_mbps":
		if mbps, err := strconv.Atoi(value); err == nil {
			cfg.Integrations.ExpectedNASLinkMbps = mbps
		}
	case "integrations.internet_probe_url":
		cfg.Integrations.InternetProbeURL = strings.TrimRight(value, "/")
	case "integrations.dns_probe_host":
		cfg.Integrations.DNSProbeHost = value
	case "integrations.router_probe_target":
		cfg.Integrations.RouterProbeTarget = strings.TrimRight(value, "/")
	case "integrations.nas_probe_target":
		cfg.Integrations.NASProbeTarget = strings.TrimRight(value, "/")
	case "retention.max_status_event_age":
		if duration, err := parseDurationValue(value); err == nil {
			cfg.Retention.MaxStatusEventAge = duration
		}
	case "retention.max_status_events_per_subject":
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Retention.MaxStatusEventsPerSubject = count
		}
	}
}

func splitList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, strings.TrimRight(trimmed, "/"))
		}
	}
	return out
}

func splitBoolMap(value string) map[string]bool {
	values := splitList(value)
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseDurationValue(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("duration is empty")
	}
	if strings.HasSuffix(strings.ToLower(value), "d") {
		days, err := strconv.Atoi(strings.TrimSpace(value[:len(value)-1]))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return duration, nil
	}
	nanos, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(nanos), nil
}
