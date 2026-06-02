package config

import (
	"os"
	"path/filepath"
	"testing"
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
