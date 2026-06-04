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
		"recommended_action_target":{"kind":"app","id_or_name":"emby"},
		"should_notify_admin":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateDiagnosisRequiresAppTargetForAppActions(t *testing.T) {
	_, err := ValidateDiagnosis([]byte(`{
		"severity":"medium",
		"confidence":0.7,
		"incident_type":"app_down",
		"affected_services":["emby"],
		"diagnosis":"Emby is offline.",
		"evidence":["container exited"],
		"general_user_summary":"Emby is offline.",
		"admin_message":"Incident: Emby is offline.",
		"recommended_action_id":"ask_admin_to_restart_container",
		"recommended_action_target":{"kind":"manual","id_or_name":""},
		"should_notify_admin":true
	}`))
	if err == nil {
		t.Fatal("expected restart recommendation without an app target to be rejected")
	}
}

func TestValidateActionReviewDecisionAcceptsStrictSchemaShape(t *testing.T) {
	decision, err := ValidateActionReviewDecision([]byte(`{
		"allow":false,
		"confidence":0.87,
		"summary":"The proposed restart target is not supported by the current snapshot.",
		"issues":["target evidence is ambiguous"],
		"checked_at":"2026-06-04T12:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allow || decision.Confidence != 0.87 || len(decision.Issues) != 1 {
		t.Fatalf("decision = %#v", decision)
	}

	if _, err := ValidateActionReviewDecision([]byte(`{"allow":true,"confidence":2,"summary":"bad","issues":[],"checked_at":"2026-06-04T12:00:00Z"}`)); err == nil {
		t.Fatal("expected invalid confidence to be rejected")
	}
}

func TestBuildActionReviewPromptIncludesReferencesAndCurrentEvidence(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	prompt := BuildActionReviewPrompt(ActionReviewRequest{
		ActionID:      "ask_admin_to_restart_container",
		ActionTitle:   "Restart recommendation",
		TargetID:      "emby",
		TargetLabel:   "Emby",
		CurrentStatus: models.StatusOffline,
		ActorRole:     models.RoleAdmin,
		Via:           "agent_plan",
		References: []ActionReviewReference{
			{Path: "docs/security.md", Content: "Restart-only repairs must stay approval-gated."},
		},
		Snapshot: models.Snapshot{
			Infrastructure: models.InfrastructureStatus{InternetReachable: true, LastCheckedAt: now},
			Apps: []models.AppStatus{{
				AppID:              "emby",
				DisplayName:        "Emby",
				CurrentStatus:      models.StatusOffline,
				AgentRepairAllowed: true,
			}},
		},
	})
	for _, want := range []string{"ask_admin_to_restart_container", "target_id: emby", "docs/security.md", "Restart-only repairs", "agent_repair_allowed=true"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
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

func TestGeneralUserLLMContextCompactsVerboseVisibleStatus(t *testing.T) {
	now := time.Now().UTC()
	snapshot := models.Snapshot{
		GeneratedAt:   now,
		OverallStatus: models.StatusOffline,
		ServerSummary: strings.Repeat("Several selected apps are not responding. ", 40),
		Infrastructure: models.InfrastructureStatus{
			InternetReachable:      true,
			DNSOK:                  true,
			RouterReachable:        true,
			NASReachable:           true,
			UnraidAPIReachable:     true,
			UnraidArrayState:       "started",
			UnraidArrayHealthy:     true,
			DockerServiceAvailable: true,
			UniFiWANUp:             true,
			UniFiGatewayReachable:  true,
			UniFiWarnings:          []string{strings.Repeat("Long network warning. ", 80)},
			SourceHealth:           models.SourceHealth{Unraid: "live", Docker: "live", UniFi: "live", Probes: "live"},
		},
		Visibility:      models.VisibilitySettings{GeneralUserCanUseLLM: true, ShowNASStatusToUsers: true, ShowWANStatusToUsers: true},
		IntegrationMode: "live",
	}
	for i := 0; i < 60; i++ {
		status := models.StatusOnline
		severity := models.SeverityNone
		if i%4 == 0 {
			status = models.StatusOffline
			severity = models.SeverityHigh
		}
		snapshot.Apps = append(snapshot.Apps, models.AppStatus{
			AppID:                 fmt.Sprintf("app-%02d", i),
			DisplayName:           fmt.Sprintf("Long App %02d", i),
			VisibleToGeneralUsers: true,
			CurrentStatus:         status,
			Severity:              severity,
			DockerState:           models.DockerRunning,
			EndpointStatus:        models.EndpointOK,
			ServerSummary:         strings.Repeat(fmt.Sprintf("Detailed status for app %02d. ", i), 40),
			CurrentProbeResult: models.ProbeResult{
				Type:      models.ProbeHTTP,
				OK:        status == models.StatusOnline,
				Message:   strings.Repeat("Probe output is verbose. ", 40),
				LatencyMS: int64(i),
				CheckedAt: now,
			},
		})
		snapshot.Incidents = append(snapshot.Incidents, models.Incident{
			ID:               fmt.Sprintf("incident-%02d", i),
			Type:             models.IncidentAppDown,
			Severity:         severity,
			Status:           status,
			Summary:          strings.Repeat("Long incident summary. ", 30),
			AffectedServices: []string{fmt.Sprintf("Long App %02d", i), "Another affected app"},
			StartedAt:        now,
			UpdatedAt:        now,
		})
		snapshot.Facts = append(snapshot.Facts, models.IncidentFact{
			ID:               fmt.Sprintf("fact-%02d", i),
			Type:             models.IncidentAppDown,
			Severity:         severity,
			Summary:          strings.Repeat("Long diagnostic fact. ", 30),
			AffectedServices: []string{fmt.Sprintf("Long App %02d", i)},
			CreatedAt:        now,
			VisibleToUsers:   true,
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
		Question: "What is wrong with Long App 56?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contextText) > 12000 {
		t.Fatalf("general user context length = %d, want <= 12000", len(contextText))
	}
	if !json.Valid([]byte(contextText)) {
		t.Fatalf("context should stay valid JSON: %s", contextText)
	}
	if !strings.Contains(contextText, "app-56") {
		t.Fatalf("compacted context omitted app mentioned in question: %s", contextText)
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

func TestAdminLLMContextCompactsVerboseLiveStatus(t *testing.T) {
	now := time.Now().UTC()
	snapshot := models.Snapshot{
		GeneratedAt:   now,
		OverallStatus: models.StatusOffline,
		ServerSummary: strings.Repeat("Several monitored apps are not responding. ", 60),
		AdminSummary:  strings.Repeat("Admin details include verbose collector observations. ", 60),
		Infrastructure: models.InfrastructureStatus{
			InternetReachable:      true,
			DNSOK:                  true,
			RouterReachable:        true,
			NASReachable:           true,
			UnraidAPIReachable:     true,
			UnraidArrayState:       "started",
			UnraidArrayHealthy:     true,
			DockerServiceAvailable: true,
			UniFiWANUp:             true,
			UniFiGatewayReachable:  true,
			StorageWarnings:        []string{strings.Repeat("Verbose storage warning. ", 90)},
			UniFiWarnings:          []string{strings.Repeat("Verbose network warning. ", 90)},
			SourceHealth:           models.SourceHealth{Unraid: "live", Docker: "live", UniFi: "live", Probes: "live"},
		},
		Visibility:      models.VisibilitySettings{GeneralUserCanUseLLM: true, ShowNASStatusToUsers: true, ShowWANStatusToUsers: true},
		IntegrationMode: "live",
	}
	for i := 0; i < 100; i++ {
		status := models.StatusOnline
		severity := models.SeverityNone
		if i%6 == 0 {
			status = models.StatusOffline
			severity = models.SeverityHigh
		}
		snapshot.Apps = append(snapshot.Apps, models.AppStatus{
			AppID:                 fmt.Sprintf("app-%02d", i),
			DisplayName:           fmt.Sprintf("Admin App %02d", i),
			ContainerName:         fmt.Sprintf("container-%02d", i),
			VisibleToGeneralUsers: true,
			CurrentStatus:         status,
			Severity:              severity,
			DockerState:           models.DockerRunning,
			EndpointStatus:        models.EndpointOK,
			ServerSummary:         strings.Repeat(fmt.Sprintf("Verbose server summary for app %02d. ", i), 35),
			AdminSummary:          strings.Repeat(fmt.Sprintf("Verbose admin summary for app %02d. ", i), 35),
			RecentLogs: []models.LogLine{
				{Timestamp: now, Source: "container", Line: strings.Repeat("long safe log line ", 80)},
				{Timestamp: now, Source: "container", Line: strings.Repeat("another long safe log line ", 80)},
			},
			CurrentProbeResult: models.ProbeResult{
				Type:      models.ProbeHTTP,
				Target:    fmt.Sprintf("https://example.invalid/admin-app-%02d", i),
				OK:        status == models.StatusOnline,
				Message:   strings.Repeat("Verbose probe output. ", 40),
				LatencyMS: int64(i),
				CheckedAt: now,
			},
		})
	}
	for i := 0; i < 30; i++ {
		snapshot.Incidents = append(snapshot.Incidents, models.Incident{
			ID:               fmt.Sprintf("incident-%02d", i),
			Type:             models.IncidentAppDown,
			Severity:         models.SeverityHigh,
			Status:           models.StatusOffline,
			Summary:          strings.Repeat("Verbose incident summary. ", 30),
			AdminSummary:     strings.Repeat("Verbose admin incident detail. ", 30),
			AffectedServices: []string{fmt.Sprintf("Admin App %02d", i), "another service"},
			Evidence:         []string{strings.Repeat("Verbose evidence. ", 30)},
			StartedAt:        now,
			UpdatedAt:        now,
		})
		snapshot.Facts = append(snapshot.Facts, models.IncidentFact{
			ID:               fmt.Sprintf("fact-%02d", i),
			Type:             models.IncidentAppDown,
			Severity:         models.SeverityHigh,
			Summary:          strings.Repeat("Verbose diagnostic fact. ", 30),
			Evidence:         []string{strings.Repeat("Verbose fact evidence. ", 30)},
			AffectedServices: []string{fmt.Sprintf("Admin App %02d", i)},
			CreatedAt:        now,
			VisibleToUsers:   true,
		})
	}

	builder := NewContextBuilder(privacy.NewRedactor(config.PrivacyConfig{}))
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
			MaxLogLines:           20,
			FailClosedOnRedaction: true,
			RecipientRole:         models.RoleAdmin,
		},
		Snapshot: snapshot,
		Question: "What is wrong with container-57?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contextText) > 32000 {
		t.Fatalf("admin context length = %d, want <= 32000", len(contextText))
	}
	if !json.Valid([]byte(contextText)) {
		t.Fatalf("context should stay valid JSON: %s", contextText)
	}
	if !strings.Contains(contextText, "container-57") {
		t.Fatalf("compacted admin context omitted app mentioned in question: %s", contextText)
	}
	payload := decodeContextPayload(t, contextText)
	apiReport := requireObject(t, payload, "api_report")
	docker := requireObject(t, apiReport, "docker")
	if got, ok := docker["app_count"].(float64); !ok || got != 100 {
		t.Fatalf("compacted admin context did not preserve app count: %#v", docker["app_count"])
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
