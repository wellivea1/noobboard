package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/fixture"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/diagnostics"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
)

func TestValidateDiagnosisRejectsInvalidJSON(t *testing.T) {
	_, err := ValidateDiagnosis([]byte(`{"severity":"panic","confidence":2}`))
	if err == nil {
		t.Fatal("expected invalid diagnosis to be rejected")
	}
}

func TestValidateDiagnosisAcceptsStrictSchemaShape(t *testing.T) {
	_, err := ValidateDiagnosis([]byte(`{
		"severity":"medium",
		"confidence":0.7,
		"incident_type":"app_down",
		"affected_services":["emby"],
		"diagnosis":"Emby is offline.",
		"evidence":["container exited"],
		"general_user_summary":"Emby is offline.",
		"admin_message":"Incident: Emby is offline.",
		"recommended_action_id":"ask_admin_to_check_logs",
		"should_notify_admin":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeneralUserLLMContextDoesNotLeakHiddenAppsOrLogs(t *testing.T) {
	snapshot, err := fixture.LoadSnapshot("../../fixtures", "llm_context_general_restricted")
	if err != nil {
		t.Fatal(err)
	}
	evaluated := diagnostics.NewRuleEngine().Evaluate(snapshot)
	snapshot.Facts = evaluated.Facts
	snapshot.Incidents = evaluated.Incidents
	snapshot.Apps = evaluated.Apps

	redactor := privacy.NewRedactor(config.PrivacyConfig{})
	builder := NewContextBuilder(redactor)
	contextText, err := builder.Build(Request{
		Mode: ModeGeneralUserRequested,
		Policy: models.LLMPolicy{
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
		},
		Snapshot: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"Hidden Tool", "hidden-tool", "admin_summary", "recent_logs"} {
		if strings.Contains(contextText, leaked) {
			t.Fatalf("general LLM context leaked %q: %s", leaked, contextText)
		}
	}
	payload := decodeContextPayload(t, contextText)
	apiReport := requireObject(t, payload, "api_report")
	requireObject(t, apiReport, "unraid")
	requireObject(t, apiReport, "unifi")
	requireObject(t, apiReport, "probes")
	docker := requireObject(t, apiReport, "docker")
	apps := requireArray(t, docker, "apps")
	if len(apps) != 1 {
		t.Fatalf("general api report app count = %d, want 1", len(apps))
	}
	app := apps[0].(map[string]interface{})
	if app["display_name"] != "Emby" {
		t.Fatalf("general api report app = %#v, want Emby", app["display_name"])
	}
	for _, hiddenKey := range []string{"admin_summary", "container_id", "container_name", "target", "recent_logs", "last_incident_ids"} {
		if _, ok := app[hiddenKey]; ok {
			t.Fatalf("general api report leaked key %q: %#v", hiddenKey, app)
		}
	}
	probes := requireObject(t, apiReport, "probes")
	probeResults := requireArray(t, probes, "app_probe_results")
	if len(probeResults) != 1 {
		t.Fatalf("general probe result count = %d, want 1", len(probeResults))
	}
	probe := probeResults[0].(map[string]interface{})
	currentProbe := requireObject(t, probe, "current_probe_result")
	if _, ok := currentProbe["target"]; ok {
		t.Fatalf("general probe report leaked target: %#v", currentProbe)
	}
}

func TestGeneralUserLLMContextStaysWithinDefaultLimitWithManyApps(t *testing.T) {
	now := time.Now().UTC()
	snapshot := models.Snapshot{
		GeneratedAt:   now,
		OverallStatus: models.StatusDegraded,
		ServerSummary: "Some apps are not working.",
		Infrastructure: models.InfrastructureStatus{
			InternetReachable:      true,
			DNSOK:                  true,
			RouterReachable:        true,
			NASReachable:           true,
			UnraidAPIReachable:     true,
			UnraidArrayState:       "started",
			UnraidArrayHealthy:     true,
			DockerServiceAvailable: true,
			LastCheckedAt:          now,
			SourceHealth:           models.SourceHealth{Unraid: "live unraid ok", Docker: "live docker ok", UniFi: "live unifi ok", Probes: "live probes ok"},
		},
		Visibility:      models.VisibilitySettings{GeneralUserCanUseLLM: true, ShowNASStatusToUsers: true, ShowWANStatusToUsers: true},
		IntegrationMode: "live",
	}
	for i := 0; i < 80; i++ {
		status := models.StatusOnline
		severity := models.SeverityNone
		summary := "Working normally."
		if i%5 == 0 {
			status = models.StatusOffline
			severity = models.SeverityMedium
			summary = "Not responding."
		}
		snapshot.Apps = append(snapshot.Apps, models.AppStatus{
			AppID:                 fmt.Sprintf("app-%02d", i),
			DisplayName:           fmt.Sprintf("App %02d", i),
			ContainerName:         fmt.Sprintf("container-%02d-should-not-leak", i),
			IconURL:               "https://example.invalid/very/long/icon/path/that/should/not/be/sent/to/the/llm.png",
			ImageRef:              "registry.example.invalid/private/image:latest",
			VisibleToGeneralUsers: true,
			CurrentStatus:         status,
			Severity:              severity,
			DockerState:           models.DockerRunning,
			EndpointStatus:        models.EndpointOK,
			ServerSummary:         summary,
			RecentLogs:            []models.LogLine{{Timestamp: now, Source: "secret", Line: strings.Repeat("log line should not leak ", 20)}},
			CurrentProbeResult: models.ProbeResult{
				Type:      models.ProbeHTTP,
				Target:    fmt.Sprintf("https://internal.example.invalid/app-%02d/secret-target", i),
				OK:        status == models.StatusOnline,
				Message:   summary,
				LatencyMS: int64(i),
				CheckedAt: now,
			},
		})
	}

	builder := NewContextBuilder(privacy.NewRedactor(config.PrivacyConfig{}))
	contextText, err := builder.Build(Request{
		Mode: ModeGeneralUserRequested,
		Policy: models.LLMPolicy{
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
		},
		Snapshot: snapshot,
		Question: "Is app-79 working?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contextText) > 12000 {
		t.Fatalf("general user context length = %d, want <= 12000", len(contextText))
	}
	for _, leaked := range []string{"snapshot", "recent_logs", "container-", "secret-target", "icon_url", "image_ref"} {
		if strings.Contains(contextText, leaked) {
			t.Fatalf("compact general context leaked %q: %s", leaked, contextText)
		}
	}
	payload := decodeContextPayload(t, contextText)
	apiReport := requireObject(t, payload, "api_report")
	if apiReport["integration_mode"] != "live" {
		t.Fatalf("compact context did not preserve live integration mode: %#v", apiReport["integration_mode"])
	}
	sourceHealth := requireObject(t, apiReport, "source_health")
	if sourceHealth["unraid"] != "live unraid ok" || sourceHealth["unifi"] != "live unifi ok" {
		t.Fatalf("compact context did not preserve live source health: %#v", sourceHealth)
	}
	docker := requireObject(t, apiReport, "docker")
	if got, ok := docker["omitted_app_count"].(float64); !ok || got <= 0 {
		t.Fatalf("compact context did not report omitted apps: %#v", docker["omitted_app_count"])
	}
	apps := requireArray(t, docker, "apps")
	foundQuestionApp := false
	for _, item := range apps {
		app := item.(map[string]interface{})
		if app["app_id"] == "app-79" {
			foundQuestionApp = true
		}
	}
	if !foundQuestionApp {
		t.Fatalf("compact context omitted app mentioned in question: %#v", apps)
	}
}

func TestAdminLLMContextRedactsSecretsInAllowedLogs(t *testing.T) {
	snapshot, err := fixture.LoadSnapshot("../../fixtures", "llm_context_admin_allowed_logs")
	if err != nil {
		t.Fatal(err)
	}
	redactor := privacy.NewRedactor(config.PrivacyConfig{})
	builder := NewContextBuilder(redactor)
	contextText, err := builder.Build(Request{
		Mode: ModeAdminRequested,
		Policy: models.LLMPolicy{
			Name:                  "admin_requested",
			Enabled:               true,
			IncludeLogs:           true,
			PreferIncidentFacts:   true,
			AllowHiddenAppNames:   true,
			AllowBlacklistedNames: false,
			MaxContextBytes:       32000,
			MaxLogLines:           10,
			FailClosedOnRedaction: true,
			RecipientRole:         models.RoleAdmin,
		},
		Snapshot: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(contextText, "secret-token") {
		t.Fatalf("admin LLM context leaked token: %s", contextText)
	}
	payload := decodeContextPayload(t, contextText)
	apiReport := requireObject(t, payload, "api_report")
	unraid := requireObject(t, apiReport, "unraid")
	if unraid["unraid_api_reachable"] != true {
		t.Fatalf("admin api report missing unRAID status: %#v", unraid)
	}
	requireObject(t, apiReport, "unifi")
	requireObject(t, apiReport, "probes")
	docker := requireObject(t, apiReport, "docker")
	apps := requireArray(t, docker, "apps")
	if len(apps) != 1 {
		t.Fatalf("admin api report app count = %d, want 1", len(apps))
	}
	app := apps[0].(map[string]interface{})
	if app["container_name"] != "emby" {
		t.Fatalf("admin api report did not include container name: %#v", app["container_name"])
	}
	logs := requireArray(t, app, "recent_logs")
	if len(logs) != 1 {
		t.Fatalf("admin api report log count = %d, want 1", len(logs))
	}
	line, ok := logs[0].(map[string]interface{})["line"].(string)
	if !ok || !strings.Contains(line, "[REDACTED]") || strings.Contains(line, "secret-token") {
		t.Fatalf("admin api report log was not safely redacted: %#v", logs[0])
	}
}

func TestContextBuilderFailsClosedInsteadOfReturningTruncatedJSON(t *testing.T) {
	snapshot, err := fixture.LoadSnapshot("../../fixtures", "all_systems_online")
	if err != nil {
		t.Fatal(err)
	}
	builder := NewContextBuilder(privacy.NewRedactor(config.PrivacyConfig{}))
	_, err = builder.Build(Request{
		Mode: ModeAdminRequested,
		Policy: models.LLMPolicy{
			Name:                  "admin_requested",
			Enabled:               true,
			IncludeLogs:           true,
			PreferIncidentFacts:   true,
			AllowHiddenAppNames:   true,
			AllowBlacklistedNames: false,
			MaxContextBytes:       20,
			MaxLogLines:           10,
			FailClosedOnRedaction: true,
			RecipientRole:         models.RoleAdmin,
		},
		Snapshot: snapshot,
	})
	if err == nil {
		t.Fatal("expected oversized context to fail")
	}

	contextText, err := builder.Build(Request{
		Mode: ModeAdminRequested,
		Policy: models.LLMPolicy{
			Name:                  "admin_requested",
			Enabled:               true,
			IncludeLogs:           true,
			PreferIncidentFacts:   true,
			AllowHiddenAppNames:   true,
			AllowBlacklistedNames: false,
			MaxContextBytes:       32000,
			MaxLogLines:           10,
			FailClosedOnRedaction: true,
			RecipientRole:         models.RoleAdmin,
		},
		Snapshot: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(contextText)) {
		t.Fatalf("context should stay valid JSON: %s", contextText)
	}
}

func decodeContextPayload(t *testing.T, contextText string) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(contextText), &payload); err != nil {
		t.Fatalf("context should be JSON object: %v\n%s", err, contextText)
	}
	return payload
}

func requireObject(t *testing.T, parent map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	value, ok := parent[key].(map[string]interface{})
	if !ok {
		t.Fatalf("missing object %q in %#v", key, parent)
	}
	return value
}

func requireArray(t *testing.T, parent map[string]interface{}, key string) []interface{} {
	t.Helper()
	value, ok := parent[key].([]interface{})
	if !ok {
		t.Fatalf("missing array %q in %#v", key, parent)
	}
	return value
}
