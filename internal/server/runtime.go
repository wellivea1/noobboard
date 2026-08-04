package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/adapters/probes"
	"github.com/wellivea1/noobboard/internal/adapters/unifi"
	"github.com/wellivea1/noobboard/internal/adapters/unraid"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/llm"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
)

// Applying settings to a running process: normalising stored values, rebuilding
// collectors, and the placeholder clients used when an integration is not
// configured so a missing integration degrades instead of crashing.

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

func (c unavailableUniFiClient) RestartableDevices(context.Context) ([]unifi.RestartableDevice, error) {
	return nil, errors.New(string(c))
}

func (c unavailableUniFiClient) RestartDevice(context.Context, string) (unifi.DeviceControlResult, error) {
	return unifi.DeviceControlResult{}, errors.New(string(c))
}

func (c unavailableUniFiClient) DeviceOnline(context.Context, string) (bool, error) {
	return false, errors.New(string(c))
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
