package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfigValidation(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8787 {
		t.Fatalf("default admin port = %d, want 8787", cfg.Server.Port)
	}
	if cfg.Server.CompactPort != 8788 {
		t.Fatalf("default compact port = %d, want 8788", cfg.Server.CompactPort)
	}
	if cfg.LLM.Provider != "disabled" {
		t.Fatalf("default llm provider = %q, want disabled", cfg.LLM.Provider)
	}
	if cfg.LLM.OpenAIAuthMethod != OpenAIAuthMethodAPIKey {
		t.Fatalf("default OpenAI auth method = %q, want api_key", cfg.LLM.OpenAIAuthMethod)
	}
	if cfg.Retention.MaxStatusEventAge != 90*24*time.Hour || cfg.Retention.MaxStatusEventsPerSubject != 500 {
		t.Fatalf("default status history retention = %s/%d", cfg.Retention.MaxStatusEventAge, cfg.Retention.MaxStatusEventsPerSubject)
	}
}

func TestConfigValidationRejectsInvalidCompactPort(t *testing.T) {
	cfg := Defaults()
	cfg.Server.CompactPort = cfg.Server.Port
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate admin and compact ports to fail validation")
	}

	cfg = Defaults()
	cfg.Server.CompactPort = 70000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid compact port to fail validation")
	}
}

func TestConfigValidationRejectsUnsafeLLMPolicy(t *testing.T) {
	cfg := Defaults()
	policy := cfg.LLM.Policies["admin_requested"]
	policy.FailClosedOnRedaction = false
	cfg.LLM.Policies["admin_requested"] = policy
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsafe LLM policy to fail validation")
	}
}

func TestConfigValidationRejectsInvalidActionAutoReviewSettings(t *testing.T) {
	cfg := Defaults()
	cfg.LLM.ActionAutoReviewModel = "openai"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected provider without model to fail action auto-review validation")
	}

	cfg = Defaults()
	cfg.LLM.ActionAutoReviewModel = "local/model"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported action auto-review provider to fail validation")
	}

	cfg = Defaults()
	cfg.LLM.ActionAutoReviewReasoning = "extreme"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid action auto-review reasoning to fail validation")
	}

	cfg = Defaults()
	cfg.LLM.ActionAutoReviewReferencePaths = []string{"docs/security.md", "bad\npath"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid action auto-review reference path to fail validation")
	}

	cfg = Defaults()
	cfg.LLM.ActionAutoReviewModel = "chatgpt/gpt-5.1-codex"
	cfg.LLM.ActionAutoReviewReasoning = "xhigh"
	cfg.LLM.ActionAutoReviewReferencePaths = []string{"docs/security.md", "AGENTS.md"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid action auto-review settings should pass: %v", err)
	}
}

func TestConfigValidationRejectsUnknownModes(t *testing.T) {
	cfg := Defaults()
	cfg.Integrations.Mode = "remote-control"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid integration mode to fail validation")
	}

	cfg = Defaults()
	cfg.LLM.Provider = "unknown-provider"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid LLM provider to fail validation")
	}

	cfg = Defaults()
	cfg.LLM.Provider = "mock"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected mock LLM provider to fail validation")
	}

	cfg = Defaults()
	cfg.LLM.OpenAIAuthMethod = "session_cookie"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid OpenAI auth method to fail validation")
	}
}

func TestConfigValidationRejectsUnsafeAppIconURL(t *testing.T) {
	cfg := Defaults()
	cfg.AppCatalog.IconOverrides["emby"] = "javascript:alert(1)"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsafe app icon URL to fail validation")
	}

	if got, err := NormalizeIconURL(" https://example.invalid/icon.png "); err != nil || got != "https://example.invalid/icon.png" {
		t.Fatalf("NormalizeIconURL = %q, %v", got, err)
	}
	if _, err := NormalizeIconURL("https://user:pass@example.invalid/icon.png"); err == nil {
		t.Fatal("expected icon URL credentials to fail validation")
	}
	if got, err := NormalizeIconURL("/icons/custom.png"); err != nil || got != "/icons/custom.png" {
		t.Fatalf("NormalizeIconURL local path = %q, %v", got, err)
	}
}

func TestLoadParsesAppCatalogRepairFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
app_catalog:
  agent_repair_allowed: emby;plex
  general_user_restarts_enabled: true
  restart_allowed_general_user: emby,jellyfin
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AppCatalog.AgentRepairAllowed["emby"] || !cfg.AppCatalog.AgentRepairAllowed["plex"] {
		t.Fatalf("agent repair flags were not parsed: %#v", cfg.AppCatalog.AgentRepairAllowed)
	}
	if !cfg.AppCatalog.GeneralUserRestartsEnabled {
		t.Fatalf("general-user restart switch was not parsed: %#v", cfg.AppCatalog)
	}
	if !cfg.AppCatalog.RestartAllowedGeneralUser["emby"] || !cfg.AppCatalog.RestartAllowedGeneralUser["jellyfin"] {
		t.Fatalf("general-user restart flags were not parsed: %#v", cfg.AppCatalog.RestartAllowedGeneralUser)
	}
}

func TestConfigValidationRejectsInvalidProbeSettings(t *testing.T) {
	cfg := Defaults()
	cfg.Integrations.InternetProbeURL = "ftp://example.invalid/probe"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid internet probe URL scheme to fail validation")
	}

	cfg = Defaults()
	cfg.Integrations.RouterProbeTarget = "router target"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected probe target whitespace to fail validation")
	}
}

func TestIntegrationBaseURLNormalizationAndValidation(t *testing.T) {
	if got := normalizeIntegrationBaseURL("192.168.0.214", "http"); got != "http://192.168.0.214" {
		t.Fatalf("bare Unraid base URL normalized to %q", got)
	}
	if got := normalizeIntegrationBaseURL("192.168.0.1", "https"); got != "https://192.168.0.1" {
		t.Fatalf("bare UniFi base URL normalized to %q", got)
	}
	if got := normalizeIntegrationBaseURL("https://192.168.0.1/", "https"); got != "https://192.168.0.1" {
		t.Fatalf("trailing slash normalized to %q", got)
	}

	cfg := Defaults()
	cfg.Integrations.UnraidBaseURL = "ftp://tower.local"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid Unraid base URL scheme to fail validation")
	}

	cfg = Defaults()
	cfg.Integrations.UniFiBaseURL = "https://user:pass@192.168.0.1"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected UniFi base URL credentials to fail validation")
	}

	cfg = Defaults()
	cfg.Integrations.ExpectedNASLinkMbps = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative expected NAS link speed to fail validation")
	}
}

func TestNoobBoardEnvOverridesAndLegacyAliases(t *testing.T) {
	t.Setenv("HSD_PORT", "8799")
	t.Setenv("NOOBBOARD_PORT", "8800")
	t.Setenv("HSD_INTEGRATION_MODE", "fixture")
	t.Setenv("NOOBBOARD_INTEGRATION_MODE", "mixed")

	cfg := Defaults()
	applyEnv(&cfg)
	if cfg.Server.Port != 8800 {
		t.Fatalf("NOOBBOARD_PORT should override legacy alias, got %d", cfg.Server.Port)
	}
	if cfg.Integrations.Mode != "mixed" {
		t.Fatalf("NOOBBOARD_INTEGRATION_MODE should override legacy alias, got %q", cfg.Integrations.Mode)
	}
}

func TestActionAutoReviewConfigParsing(t *testing.T) {
	t.Setenv("NOOBBOARD_ACTION_AUTO_REVIEW_ENABLED", "true")
	t.Setenv("NOOBBOARD_AGENT_AUTO_REPAIR_ENABLED", "true")
	t.Setenv("NOOBBOARD_ACTION_AUTO_REVIEW_MODEL", "anthropic/claude-sonnet-4-5")
	t.Setenv("NOOBBOARD_ACTION_AUTO_REVIEW_REASONING", "high")
	t.Setenv("NOOBBOARD_ACTION_AUTO_REVIEW_REFERENCES", "docs/security.md,AGENTS.md")

	cfg := Defaults()
	applyEnv(&cfg)
	if !cfg.LLM.ActionAutoReviewEnabled ||
		!cfg.LLM.AgentAutoRepairEnabled ||
		cfg.LLM.ActionAutoReviewModel != "anthropic/claude-sonnet-4-5" ||
		cfg.LLM.ActionAutoReviewReasoning != "high" ||
		len(cfg.LLM.ActionAutoReviewReferencePaths) != 2 ||
		cfg.LLM.ActionAutoReviewReferencePaths[1] != "AGENTS.md" {
		t.Fatalf("env action auto-review settings were not applied: %#v", cfg.LLM)
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("llm:\n  action_auto_review_enabled: true\n  agent_auto_repair_enabled: true\n  action_auto_review_model: chatgpt/gpt-5.1-codex\n  action_auto_review_reasoning: xhigh\n  action_auto_review_reference_paths: docs/security.md,docs/llm-policy.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = Defaults()
	if err := applySimpleConfigFile(&cfg, configPath); err != nil {
		t.Fatal(err)
	}
	if !cfg.LLM.ActionAutoReviewEnabled ||
		!cfg.LLM.AgentAutoRepairEnabled ||
		cfg.LLM.ActionAutoReviewModel != "chatgpt/gpt-5.1-codex" ||
		cfg.LLM.ActionAutoReviewReasoning != "xhigh" ||
		len(cfg.LLM.ActionAutoReviewReferencePaths) != 2 ||
		cfg.LLM.ActionAutoReviewReferencePaths[1] != "docs/llm-policy.md" {
		t.Fatalf("file action auto-review settings were not applied: %#v", cfg.LLM)
	}
}

func TestLegacyEnvAliasStillWorks(t *testing.T) {
	t.Setenv("HSD_COMPACT_PORT", "8899")

	cfg := Defaults()
	applyEnv(&cfg)
	if cfg.Server.CompactPort != 8899 {
		t.Fatalf("legacy HSD_COMPACT_PORT alias was not applied, got %d", cfg.Server.CompactPort)
	}
}

func TestStatusHistoryRetentionConfigParsing(t *testing.T) {
	t.Setenv("NOOBBOARD_MAX_STATUS_EVENT_AGE", "14d")
	t.Setenv("NOOBBOARD_MAX_STATUS_EVENTS_PER_SUBJECT", "42")
	cfg := Defaults()
	applyEnv(&cfg)
	if cfg.Retention.MaxStatusEventAge != 14*24*time.Hour || cfg.Retention.MaxStatusEventsPerSubject != 42 {
		t.Fatalf("env retention = %s/%d", cfg.Retention.MaxStatusEventAge, cfg.Retention.MaxStatusEventsPerSubject)
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("retention:\n  max_status_event_age: 6h\n  max_status_events_per_subject: 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = Defaults()
	if err := applySimpleConfigFile(&cfg, configPath); err != nil {
		t.Fatal(err)
	}
	if cfg.Retention.MaxStatusEventAge != 6*time.Hour || cfg.Retention.MaxStatusEventsPerSubject != 7 {
		t.Fatalf("file retention = %s/%d", cfg.Retention.MaxStatusEventAge, cfg.Retention.MaxStatusEventsPerSubject)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("retention config should validate: %v", err)
	}
}

func TestNASLinkIntegrationConfigParsing(t *testing.T) {
	t.Setenv("NOOBBOARD_UNIFI_NAS_CLIENT_HINT", "192.168.0.214")
	t.Setenv("NOOBBOARD_EXPECTED_NAS_LINK_MBPS", "2500")
	cfg := Defaults()
	applyEnv(&cfg)
	if cfg.Integrations.UniFiNASClientHint != "192.168.0.214" || cfg.Integrations.ExpectedNASLinkMbps != 2500 {
		t.Fatalf("env NAS link settings = %q/%d", cfg.Integrations.UniFiNASClientHint, cfg.Integrations.ExpectedNASLinkMbps)
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("integrations:\n  unifi_nas_client_hint: tower.local\n  expected_nas_link_mbps: 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = Defaults()
	if err := applySimpleConfigFile(&cfg, configPath); err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations.UniFiNASClientHint != "tower.local" || cfg.Integrations.ExpectedNASLinkMbps != 1000 {
		t.Fatalf("file NAS link settings = %q/%d", cfg.Integrations.UniFiNASClientHint, cfg.Integrations.ExpectedNASLinkMbps)
	}
}

func TestConfigValidationRequiresSSHDetailsWhenFallbackEnabled(t *testing.T) {
	cfg := Defaults()
	cfg.Integrations.UnraidSSHFallback = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected SSH fallback without host/user to fail validation")
	}

	cfg.Integrations.UnraidSSHHost = "tower.local"
	cfg.Integrations.UnraidSSHUser = "root"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("SSH fallback with host/user should validate: %v", err)
	}
}

func TestSecretFileLoadsBareTokenAndKeyValue(t *testing.T) {
	dir := t.TempDir()
	unraidFile := filepath.Join(dir, "unraid.key")
	unifiFile := filepath.Join(dir, "unifi.key")
	if err := os.WriteFile(unraidFile, []byte("# local only\nbare-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unifiFile, []byte("UNIFI_API_KEY=key-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	cfg.Integrations.UnraidAPIKeyFile = unraidFile
	cfg.Integrations.UniFiAPIKeyFile = unifiFile
	if err := applySecretFiles(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations.UnraidAPIKey != "bare-token" {
		t.Fatal("Unraid API key file was not loaded")
	}
	if cfg.Integrations.UniFiAPIKey != "key-value" {
		t.Fatal("UniFi API key file was not loaded")
	}
}

func TestSecretFileRejectsMultipleValues(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "api.key")
	if err := os.WriteFile(secretFile, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecretFile(secretFile); err == nil {
		t.Fatal("expected multiple secret values to fail")
	}
}

func TestConfigValidationRequiresPasswordForRemoteBind(t *testing.T) {
	cfg := Defaults()
	cfg.Server.BindAddress = "0.0.0.0"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected remote bind with default password to fail validation")
	}

	cfg.Auth.BootstrapAdminPassword = "correct-horse-battery-staple"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("remote bind with explicit password should validate: %v", err)
	}
}
