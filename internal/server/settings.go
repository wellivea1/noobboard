package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/llm"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
)

// Settings endpoints and their wire types. Reads never return secrets - only
// *_set booleans - and every write is CSRF-checked, audited, and applied to the
// running process through applyRuntimeSettings.

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
	AgentRestartSuggestionEnabled  bool                        `json:"agent_restart_suggestion_enabled"`
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
	AgentRestartSuggestionEnabled  *bool                       `json:"agent_restart_suggestion_enabled"`
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
		AgentRestartSuggestionEnabled:  cfg.RestartSuggestionEnabled(),
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
	if update.AgentRestartSuggestionEnabled != nil {
		enabled := *update.AgentRestartSuggestionEnabled
		settings.AgentRestartSuggestionEnabled = &enabled
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
