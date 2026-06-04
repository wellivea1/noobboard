package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/adapters/probes"
	"github.com/wellivea1/noobboard/internal/adapters/unifi"
	"github.com/wellivea1/noobboard/internal/adapters/unraid"
	"github.com/wellivea1/noobboard/internal/audit"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/diagnostics"
	"github.com/wellivea1/noobboard/internal/llm"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/notifications"
	"github.com/wellivea1/noobboard/internal/privacy"
	"github.com/wellivea1/noobboard/internal/users"
)

func TestVisibilitySettingsEndpointPersists(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "visibility-settings")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	body := `{
		"hidden_app_ids": ["emby"],
		"hidden_container_names": ["secret-container"],
		"general_user_can_use_llm": false,
		"show_nas_status_to_users": false,
		"show_wan_status_to_users": true,
		"show_incident_ids_to_users": true
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/visibility", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/settings/visibility status = %d, body = %s", rec.Code, rec.Body.String())
	}

	stored, ok, err := app.deps.Store.RuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("visibility update did not save runtime settings")
	}
	if stored.Visibility.GeneralUserCanUseLLM {
		t.Fatal("runtime settings did not persist updated general_user_can_use_llm")
	}
	if got := stored.Visibility.HiddenAppIDs; len(got) != 1 || got[0] != "emby" {
		t.Fatalf("runtime settings hidden_app_ids = %#v", got)
	}

	reloaded := newTestApp(t, cfg)
	if reloaded.deps.Config.Visibility.GeneralUserCanUseLLM {
		t.Fatal("saved visibility settings were not applied on server startup")
	}
	if got := reloaded.deps.Config.Visibility.HiddenContainerNames; len(got) != 1 || got[0] != "secret-container" {
		t.Fatalf("reloaded hidden_container_names = %#v", got)
	}
}

func TestGeneralUserDiagnoseRespectsVisibilitySetting(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "general-user-diagnose")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.Visibility.GeneralUserCanUseLLM = false

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAs(t, router, "viewer", "change-me-now")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/user/diagnose", strings.NewReader(`{"question":"what is wrong?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /api/user/diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDiagnoseRequiresConfiguredLLMProvider(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "diagnose-provider-required")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAs(t, router, "viewer", "change-me-now")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/user/diagnose", strings.NewReader(`{"question":"what is wrong?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /api/user/diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "NOOBBOARD_LLM_PROVIDER") {
		t.Fatalf("diagnose error did not explain provider setup: %s", rec.Body.String())
	}
}

func TestAdminDiagnoseReportsOpenAIUsageLimit(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "diagnose-openai-usage-limit")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"
	cfg.LLM.AgentControlEnabled = true

	app := newTestApp(t, cfg)
	app.settingsMu.Lock()
	app.deps.LLM = usageLimitLLMClient{}
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("POST /api/admin/diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != llm.OpenAIUsageLimitCode {
		t.Fatalf("diagnose code = %#v, body = %s", response["code"], rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(response["error"]), "usage limit") {
		t.Fatalf("diagnose error did not report usage limit: %s", rec.Body.String())
	}
}

func TestAdminDiagnoseIncludesLockedAgentApprovalPlan(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "diagnose-agent-approval-plan")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"
	cfg.LLM.AgentControlEnabled = true

	app := newTestApp(t, cfg)
	app.settingsMu.Lock()
	app.deps.LLM = &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityHigh,
			Confidence:          0.88,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Emby"},
			Diagnosis:           "Emby is down.",
			Evidence:            []string{"App is offline."},
			GeneralUserSummary:  "Emby is not working.",
			AdminMessage:        "Review whether Emby should be restarted.",
			RecommendedActionID: "ask_admin_to_restart_container",
			ShouldNotifyAdmin:   true,
		},
	}
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.AgentPlan == nil {
		t.Fatalf("agent_plan missing from response: %s", rec.Body.String())
	}
	if response.AgentPlan.CanExecute {
		t.Fatalf("agent plan should not expose execution: %#v", response.AgentPlan)
	}
	if response.AgentPlan.Status != "approval_locked" {
		t.Fatalf("agent plan status = %q, want approval_locked", response.AgentPlan.Status)
	}
	if !response.AgentPlan.ActionKnown {
		t.Fatalf("agent plan should identify the allowlisted recommendation: %#v", response.AgentPlan)
	}
	if !response.AgentPlan.RequiresAdminApproval || response.AgentPlan.RecommendedActionID != "ask_admin_to_restart_container" {
		t.Fatalf("agent plan did not preserve approval action: %#v", response.AgentPlan)
	}
	if !response.AgentPlan.Target.Resolved || response.AgentPlan.Target.Kind != "app" || response.AgentPlan.Target.ID != "emby" {
		t.Fatalf("agent plan did not resolve the app target: %#v", response.AgentPlan.Target)
	}
	if response.AgentPlan.ApprovalToken == "" {
		t.Fatalf("agent plan did not include an approval token: %#v", response.AgentPlan)
	}
	tokenPayload, err := app.verifyAgentApprovalToken(response.AgentPlan.ApprovalToken, "admin-1")
	if err != nil {
		t.Fatalf("agent plan approval token did not verify: %v", err)
	}
	if tokenPayload.TargetKind != "app" || tokenPayload.TargetID != "emby" {
		t.Fatalf("approval token did not bind the target app: %#v", tokenPayload)
	}
	if !response.AgentPlan.ApprovalExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("agent plan approval expiry is not in the future: %#v", response.AgentPlan.ApprovalExpiresAt)
	}
	var sawDeny, sawAllowLocked bool
	for _, option := range response.AgentPlan.Options {
		if option.ID == "deny" && option.Enabled {
			sawDeny = true
		}
		if option.ID == "allow_once" && !option.Enabled && option.Reason != "" {
			sawAllowLocked = true
		}
	}
	if !sawDeny || !sawAllowLocked {
		t.Fatalf("approval options missing deny/locked allow choices: %#v", response.AgentPlan.Options)
	}
}

func TestAdminDiagnoseDoesNotOpenApprovalForUnresolvedAppTarget(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "diagnose-agent-unresolved-target")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"

	app := newTestApp(t, cfg)
	app.settingsMu.Lock()
	app.deps.LLM = &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityHigh,
			Confidence:          0.88,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Not A Real App"},
			Diagnosis:           "A service is down.",
			Evidence:            []string{"App is offline."},
			GeneralUserSummary:  "A service is not working.",
			AdminMessage:        "Review whether the service should be restarted.",
			RecommendedActionID: "ask_admin_to_restart_container",
			RecommendedTarget:   llm.ActionTarget{Kind: "app", IDOrName: "Not A Real App"},
			ShouldNotifyAdmin:   true,
		},
	}
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.AgentPlan == nil {
		t.Fatalf("agent_plan missing from response: %s", rec.Body.String())
	}
	if response.AgentPlan.RequiresAdminApproval || response.AgentPlan.ApprovalToken != "" {
		t.Fatalf("unresolved app target should not open an approval plan: %#v", response.AgentPlan)
	}
	if response.AgentPlan.Status != "target_unresolved" || response.AgentPlan.Target.Resolved {
		t.Fatalf("unresolved app target status = %#v", response.AgentPlan.Target)
	}
}

func TestAdminDiagnoseNormalizesUnknownAgentAction(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "diagnose-agent-unknown-plan")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"

	app := newTestApp(t, cfg)
	app.settingsMu.Lock()
	app.deps.LLM = &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityHigh,
			Confidence:          0.88,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Emby"},
			Diagnosis:           "Emby is down.",
			Evidence:            []string{"App is offline."},
			GeneralUserSummary:  "Emby is not working.",
			AdminMessage:        "The model attempted an unsupported action.",
			RecommendedActionID: "delete_all_apps",
			ShouldNotifyAdmin:   true,
		},
	}
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.AgentPlan == nil {
		t.Fatalf("agent_plan missing from response: %s", rec.Body.String())
	}
	if response.AgentPlan.ActionKnown || response.AgentPlan.RecommendedActionID != "unknown" || response.AgentPlan.RequiresAdminApproval {
		t.Fatalf("unsupported model action was not normalized to a non-approval-eligible plan: %#v", response.AgentPlan)
	}
	if response.AgentPlan.Status != "not_actionable" {
		t.Fatalf("unknown-action agent plan status = %q, want not_actionable", response.AgentPlan.Status)
	}
}

func TestAdminDiagnoseKeepsManualRecommendationInformational(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "diagnose-agent-manual-recommendation")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"
	cfg.LLM.AgentControlEnabled = true

	app := newTestApp(t, cfg)
	app.settingsMu.Lock()
	app.deps.LLM = &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityMedium,
			Confidence:          0.82,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Emby"},
			Diagnosis:           "Emby has suspicious logs.",
			Evidence:            []string{"App is offline."},
			GeneralUserSummary:  "Emby needs an admin check.",
			AdminMessage:        "Check Emby logs before taking action.",
			RecommendedActionID: "ask_admin_to_check_logs",
			RecommendedTarget:   llm.ActionTarget{Kind: "app", IDOrName: "emby"},
			ShouldNotifyAdmin:   true,
		},
	}
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.AgentPlan == nil {
		t.Fatalf("agent_plan missing from response: %s", rec.Body.String())
	}
	if !response.AgentPlan.ActionKnown || response.AgentPlan.RecommendedActionID != "ask_admin_to_check_logs" {
		t.Fatalf("manual recommendation was not preserved as a known action: %#v", response.AgentPlan)
	}
	if response.AgentPlan.RequiresAdminApproval || response.AgentPlan.ApprovalToken != "" || response.AgentPlan.CanExecute {
		t.Fatalf("manual recommendation opened an executable approval path: %#v", response.AgentPlan)
	}
	if response.AgentPlan.Status != "not_actionable" {
		t.Fatalf("manual recommendation status = %q, want not_actionable", response.AgentPlan.Status)
	}
}

func TestAgentApprovalEndpointAuditsAndFailsClosed(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "agent-approval-endpoint")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"
	cfg.LLM.AgentControlEnabled = true

	app := newTestApp(t, cfg)
	app.settingsMu.Lock()
	app.deps.LLM = &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityHigh,
			Confidence:          0.88,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Emby"},
			Diagnosis:           "Emby is down.",
			Evidence:            []string{"App is offline."},
			GeneralUserSummary:  "Emby is not working.",
			AdminMessage:        "Review whether Emby should be restarted.",
			RecommendedActionID: "ask_admin_to_restart_container",
			ShouldNotifyAdmin:   true,
		},
	}
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var diagnosis diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if diagnosis.AgentPlan == nil || diagnosis.AgentPlan.ApprovalToken == "" {
		t.Fatalf("diagnose did not return signed approval plan: %s", rec.Body.String())
	}
	approvalToken := diagnosis.AgentPlan.ApprovalToken

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"deny"}`, approvalToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deny approval status = %d, body = %s", rec.Code, rec.Body.String())
	}
	tail, err := app.deps.Store.AuditTail(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) == 0 || tail[len(tail)-1].Action != "llm.agent_plan.denied" {
		t.Fatalf("deny approval was not audited: %#v", tail)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"allow_once"}`, approvalToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("unarmed allow approval status = %d, body = %s", rec.Code, rec.Body.String())
	}
	tail, err = app.deps.Store.AuditTail(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) == 0 || tail[len(tail)-1].Action != "llm.agent_plan.not_armed" {
		t.Fatalf("unarmed approval was not audited: %#v", tail)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/arm", strings.NewReader(`{"armed":true,"duration_seconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("arm agent status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"allow_once"}`, approvalToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("armed allow approval status = %d, body = %s", rec.Code, rec.Body.String())
	}
	tail, err = app.deps.Store.AuditTail(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) == 0 || tail[len(tail)-1].Action != "llm.agent_plan.refused" {
		t.Fatalf("refused approval was not audited: %#v", tail)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(`{"approval_token":"tampered","choice":"deny"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tampered approval status = %d, body = %s", rec.Code, rec.Body.String())
	}

	invalidActionToken := app.signAgentApprovalToken(agentApprovalTokenPayload{
		PlanID:              agentApprovalPlanID,
		ActorID:             "admin-1",
		RecommendedActionID: "delete_all_apps",
		ExpiresAt:           time.Now().UTC().Add(time.Minute).Unix(),
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"deny"}`, invalidActionToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unregistered-action approval status = %d, body = %s", rec.Code, rec.Body.String())
	}

	nonApprovalEligibleToken := app.signAgentApprovalToken(agentApprovalTokenPayload{
		PlanID:              agentApprovalPlanID,
		ActorID:             "admin-1",
		RecommendedActionID: "none",
		ExpiresAt:           time.Now().UTC().Add(time.Minute).Unix(),
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"deny"}`, nonApprovalEligibleToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-approval-eligible approval status = %d, body = %s", rec.Code, rec.Body.String())
	}

	missingTargetToken := app.signAgentApprovalToken(agentApprovalTokenPayload{
		PlanID:              agentApprovalPlanID,
		ActorID:             "admin-1",
		RecommendedActionID: "ask_admin_to_restart_container",
		ExpiresAt:           time.Now().UTC().Add(time.Minute).Unix(),
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"deny"}`, missingTargetToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing-target approval status = %d, body = %s", rec.Code, rec.Body.String())
	}

	wrongPlanToken := app.signAgentApprovalToken(agentApprovalTokenPayload{
		PlanID:              "other_plan",
		ActorID:             "admin-1",
		RecommendedActionID: "ask_admin_to_restart_container",
		ExpiresAt:           time.Now().UTC().Add(time.Minute).Unix(),
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"deny"}`, wrongPlanToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong-plan approval status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAgentApprovalExecutesOptedInRestartOnce(t *testing.T) {
	oldDelay := agentRepairVerificationDelay
	agentRepairVerificationDelay = 0
	t.Cleanup(func() { agentRepairVerificationDelay = oldDelay })

	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "agent-approval-executes")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"
	cfg.LLM.AgentControlEnabled = true
	cfg.AppCatalog.AgentRepairAllowed = map[string]bool{"emby": true}

	app := newTestApp(t, cfg)
	collector := &recordingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerID:           "container:Emby",
		ContainerName:         "Emby",
		Category:              "docker",
		DockerState:           models.DockerExited,
		CurrentStatus:         models.StatusOffline,
		VisibleToGeneralUsers: true,
	}},
		afterControlApps: []models.AppStatus{{
			AppID:                 "emby",
			DisplayName:           "Emby",
			ContainerID:           "container:Emby",
			ContainerName:         "Emby",
			Category:              "docker",
			DockerState:           models.DockerRunning,
			CurrentStatus:         models.StatusOnline,
			VisibleToGeneralUsers: true,
		}}}
	app.deps.Collectors.Docker = collector
	app.settingsMu.Lock()
	app.deps.LLM = &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityHigh,
			Confidence:          0.9,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Emby"},
			Diagnosis:           "Emby is down.",
			Evidence:            []string{"App is offline."},
			GeneralUserSummary:  "Emby is not working.",
			AdminMessage:        "Restart Emby once.",
			RecommendedActionID: "ask_admin_to_restart_container",
			RecommendedTarget:   llm.ActionTarget{Kind: "app", IDOrName: "emby"},
			ShouldNotifyAdmin:   true,
		},
	}
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agent/arm", strings.NewReader(`{"armed":true,"duration_seconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("arm agent status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var diagnosis diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if diagnosis.AgentPlan == nil || diagnosis.AgentPlan.ApprovalToken == "" {
		t.Fatalf("diagnose did not return executable approval plan: %s", rec.Body.String())
	}
	if !diagnosis.AgentPlan.CanExecute || diagnosis.AgentPlan.Status != "approval_ready" {
		t.Fatalf("agent plan was not ready to execute: %#v", diagnosis.AgentPlan)
	}
	tokenPayload, err := app.verifyAgentApprovalToken(diagnosis.AgentPlan.ApprovalToken, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if tokenPayload.Nonce == "" {
		t.Fatalf("approval token did not include a replay nonce: %#v", tokenPayload)
	}
	var allowEnabled bool
	for _, option := range diagnosis.AgentPlan.Options {
		if option.ID == "allow_once" && option.Enabled {
			allowEnabled = true
		}
	}
	if !allowEnabled {
		t.Fatalf("allow_once option was not enabled: %#v", diagnosis.AgentPlan.Options)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"allow_once"}`, diagnosis.AgentPlan.ApprovalToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("approve restart status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var approval struct {
		Status  string                    `json:"status"`
		Outcome llmAgentRepairOutcomeView `json:"outcome"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&approval); err != nil {
		t.Fatal(err)
	}
	if approval.Status != "executed" || !approval.Outcome.Verified || !approval.Outcome.Recovered {
		t.Fatalf("approval outcome did not report recovered execution: %#v", approval)
	}
	if approval.Outcome.BeforeStatus != models.StatusOffline || approval.Outcome.AfterStatus != models.StatusOnline {
		t.Fatalf("approval outcome statuses = %s -> %s", approval.Outcome.BeforeStatus, approval.Outcome.AfterStatus)
	}
	if collector.callCount != 1 || collector.called != docker.ActionRestart || collector.app.AppID != "emby" || !collector.app.AgentRepairAllowed {
		t.Fatalf("restart was not executed exactly once on resolved opted-in app: count=%d action=%q app=%#v", collector.callCount, collector.called, collector.app)
	}
	history, err := app.deps.History.Query(db.HistoryFilter{SubjectType: models.SubjectApp, SubjectID: "emby", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 || history[0].Note != "Auto-repair: restarted - recovered." || history[0].From != models.StatusOffline || history[0].To != models.StatusOnline {
		t.Fatalf("repair verification history event missing: %#v", history)
	}
	tail, err := app.deps.Store.AuditTail(10)
	if err != nil {
		t.Fatal(err)
	}
	var sawApproved, sawExecuted, sawVerified bool
	for _, entry := range tail {
		if entry.Action == "llm.agent_plan.approved" {
			sawApproved = true
		}
		if entry.Action == "llm.agent_plan.executed" {
			sawExecuted = true
		}
		if entry.Action == "llm.agent_plan.verified" {
			sawVerified = true
		}
	}
	if !sawApproved || !sawExecuted || !sawVerified {
		t.Fatalf("approval lifecycle was not audited: %#v", tail)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"allow_once"}`, diagnosis.AgentPlan.ApprovalToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("replay approval status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.callCount != 1 {
		t.Fatalf("replayed approval called docker again: count=%d", collector.callCount)
	}
	tail, err = app.deps.Store.AuditTail(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) == 0 || tail[len(tail)-1].Action != "llm.agent_plan.replay_blocked" {
		t.Fatalf("replay was not audited: %#v", tail)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"can you restart Emby again?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var secondDiagnosis diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&secondDiagnosis); err != nil {
		t.Fatal(err)
	}
	if secondDiagnosis.AgentPlan == nil || secondDiagnosis.AgentPlan.Status != "approval_rate_limited" || secondDiagnosis.AgentPlan.CanExecute {
		t.Fatalf("second plan was not rate-limited by cooldown: %#v", secondDiagnosis.AgentPlan)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"allow_once"}`, secondDiagnosis.AgentPlan.ApprovalToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cooldown approval status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.callCount != 1 {
		t.Fatalf("cooldown approval called docker again: count=%d", collector.callCount)
	}
	tail, err = app.deps.Store.AuditTail(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) == 0 || tail[len(tail)-1].Action != "llm.agent_plan.rate_limited" {
		t.Fatalf("cooldown refusal was not audited: %#v", tail)
	}
}

func TestAgentApprovalAutoReviewDenialBlocksDocker(t *testing.T) {
	oldDelay := agentRepairVerificationDelay
	agentRepairVerificationDelay = 0
	t.Cleanup(func() { agentRepairVerificationDelay = oldDelay })
	t.Chdir(filepath.Join("..", ".."))

	cfg := config.Defaults()
	cfg.Database.Path = filepath.Join(t.TempDir(), "dashboard.db.json")
	cfg.FixtureDir = "fixtures"
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"
	cfg.LLM.AgentControlEnabled = true
	cfg.LLM.ActionAutoReviewEnabled = true
	cfg.LLM.ActionAutoReviewModel = "same"
	cfg.LLM.ActionAutoReviewReferencePaths = []string{"docs/security.md", "auth.txt", "../README.md"}
	cfg.AppCatalog.AgentRepairAllowed = map[string]bool{"emby": true}

	app := newTestApp(t, cfg)
	collector := &recordingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerID:           "container:Emby",
		ContainerName:         "Emby",
		Category:              "docker",
		DockerState:           models.DockerExited,
		CurrentStatus:         models.StatusOffline,
		VisibleToGeneralUsers: true,
	}}}
	app.deps.Collectors.Docker = collector
	llmClient := &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityHigh,
			Confidence:          0.9,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Emby"},
			Diagnosis:           "Emby is down.",
			Evidence:            []string{"App is offline."},
			GeneralUserSummary:  "Emby is not working.",
			AdminMessage:        "Restart Emby once.",
			RecommendedActionID: "ask_admin_to_restart_container",
			RecommendedTarget:   llm.ActionTarget{Kind: "app", IDOrName: "emby"},
			ShouldNotifyAdmin:   true,
		},
		reviewDecision: llm.ActionReviewDecision{
			Allow:      false,
			Confidence: 0.91,
			Summary:    "Target evidence is ambiguous.",
			Issues:     []string{"Reviewer could not confirm the proposed restart from the evidence."},
			CheckedAt:  time.Now().UTC(),
		},
	}
	app.settingsMu.Lock()
	app.deps.LLM = llmClient
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agent/arm", strings.NewReader(`{"armed":true,"duration_seconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("arm agent status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var diagnosis diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if diagnosis.AgentPlan == nil || !diagnosis.AgentPlan.CanExecute || diagnosis.AgentPlan.ApprovalToken == "" {
		t.Fatalf("diagnose did not return executable approval plan: %#v", diagnosis.AgentPlan)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"allow_once"}`, diagnosis.AgentPlan.ApprovalToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("auto-review-denied approval status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.callCount != 0 {
		t.Fatalf("auto-review denial called docker: count=%d", collector.callCount)
	}
	if llmClient.reviewCalls != 1 || llmClient.reviewRequest.ActionID != "ask_admin_to_restart_container" || llmClient.reviewRequest.TargetID != "emby" {
		t.Fatalf("auto-review request was not recorded correctly: calls=%d req=%#v", llmClient.reviewCalls, llmClient.reviewRequest)
	}
	if len(llmClient.reviewRequest.References) != 1 || llmClient.reviewRequest.References[0].Path != "docs/security.md" {
		t.Fatalf("auto-review did not load only safe references: %#v", llmClient.reviewRequest.References)
	}
	tail, err := app.deps.Store.AuditTail(10)
	if err != nil {
		t.Fatal(err)
	}
	var sawReviewed, sawRefused bool
	for _, entry := range tail {
		if entry.Action == "llm.agent_plan.auto_reviewed" {
			sawReviewed = true
		}
		if entry.Action == "llm.agent_plan.auto_review_refused" {
			sawRefused = true
		}
	}
	if !sawReviewed || !sawRefused {
		t.Fatalf("auto-review denial was not audited: %#v", tail)
	}
}

func TestAgentApprovalGlobalRateLimitBlocksDocker(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "agent-approval-global-rate")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"
	cfg.LLM.AgentControlEnabled = true
	cfg.AppCatalog.AgentRepairAllowed = map[string]bool{"emby": true}

	app := newTestApp(t, cfg)
	collector := &recordingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerID:           "container:Emby",
		ContainerName:         "Emby",
		Category:              "docker",
		DockerState:           models.DockerExited,
		CurrentStatus:         models.StatusOffline,
		VisibleToGeneralUsers: true,
	}}}
	app.deps.Collectors.Docker = collector
	app.settingsMu.Lock()
	app.deps.LLM = &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityHigh,
			Confidence:          0.9,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Emby"},
			Diagnosis:           "Emby is down.",
			AdminMessage:        "Restart Emby once.",
			RecommendedActionID: "ask_admin_to_restart_container",
			RecommendedTarget:   llm.ActionTarget{Kind: "app", IDOrName: "emby"},
		},
	}
	app.settingsMu.Unlock()
	now := time.Now().UTC()
	app.agentRepairMu.Lock()
	app.agentRepairGlobal = []time.Time{
		now.Add(-time.Minute),
		now.Add(-2 * time.Minute),
		now.Add(-3 * time.Minute),
		now.Add(-4 * time.Minute),
		now.Add(-5 * time.Minute),
	}
	app.agentRepairMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agent/arm", strings.NewReader(`{"armed":true,"duration_seconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("arm agent status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/diagnose", strings.NewReader(`{"question":"what is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var diagnosis diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if diagnosis.AgentPlan == nil || diagnosis.AgentPlan.Status != "approval_rate_limited" || diagnosis.AgentPlan.CanExecute {
		t.Fatalf("global-limited plan was not locked: %#v", diagnosis.AgentPlan)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/approval", strings.NewReader(fmt.Sprintf(`{"approval_token":%q,"choice":"allow_once"}`, diagnosis.AgentPlan.ApprovalToken)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rate-limited approval status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.callCount != 0 {
		t.Fatalf("global rate-limited approval called docker: count=%d", collector.callCount)
	}
	tail, err := app.deps.Store.AuditTail(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) == 0 || tail[len(tail)-1].Action != "llm.agent_plan.rate_limited" {
		t.Fatalf("global rate limit refusal was not audited: %#v", tail)
	}
}

func TestGeneralUserDiagnoseIncludesRepairRequestPlan(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "general-user-repair-plan")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"

	app := newTestApp(t, cfg)
	app.deps.Collectors.Docker = &recordingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerID:           "container:Emby",
		ContainerName:         "Emby",
		Category:              "docker",
		DockerState:           models.DockerExited,
		CurrentStatus:         models.StatusOffline,
		VisibleToGeneralUsers: true,
	}}}
	app.settingsMu.Lock()
	app.deps.LLM = &recordingLLMClient{
		diagnosis: llm.Diagnosis{
			Severity:            models.SeverityHigh,
			Confidence:          0.9,
			IncidentType:        models.IncidentAppDown,
			AffectedServices:    []string{"Emby"},
			Diagnosis:           "Emby is down.",
			GeneralUserSummary:  "Emby is not working.",
			RecommendedActionID: "ask_admin_to_restart_container",
			RecommendedTarget:   llm.ActionTarget{Kind: "app", IDOrName: "emby"},
			ShouldNotifyAdmin:   true,
		},
	}
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAs(t, router, "viewer", "change-me-now")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/user/diagnose", strings.NewReader(`{"question":"what is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("general diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response diagnosisResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.AgentPlan == nil || !response.AgentPlan.CanRequestRepair || response.AgentPlan.ApprovalToken != "" || response.AgentPlan.CanExecute {
		t.Fatalf("general-user plan should only expose a request affordance: %#v", response.AgentPlan)
	}
	if response.AgentPlan.Target.ID != "emby" || response.AgentPlan.Status != "request_available" {
		t.Fatalf("general-user plan did not resolve Emby: %#v", response.AgentPlan)
	}
}

func TestGeneralUserRepairRequestCanBeApprovedByArmedAdmin(t *testing.T) {
	oldDelay := agentRepairVerificationDelay
	agentRepairVerificationDelay = 0
	t.Cleanup(func() { agentRepairVerificationDelay = oldDelay })

	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "general-user-repair-request-approve")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.AgentControlEnabled = true
	cfg.AppCatalog.AgentRepairAllowed = map[string]bool{"emby": true}

	app := newTestApp(t, cfg)
	collector := &recordingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerID:           "container:Emby",
		ContainerName:         "Emby",
		Category:              "docker",
		DockerState:           models.DockerExited,
		CurrentStatus:         models.StatusOffline,
		VisibleToGeneralUsers: true,
	}},
		afterControlApps: []models.AppStatus{{
			AppID:                 "emby",
			DisplayName:           "Emby",
			ContainerID:           "container:Emby",
			ContainerName:         "Emby",
			Category:              "docker",
			DockerState:           models.DockerRunning,
			CurrentStatus:         models.StatusOnline,
			VisibleToGeneralUsers: true,
		}}}
	app.deps.Collectors.Docker = collector

	router := app.Router()
	viewerCookie, viewerCSRF := loginAs(t, router, "viewer", "change-me-now")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/user/repair-request", strings.NewReader(`{"app_id":"emby","action_id":"ask_admin_to_restart_container","diagnosis_summary":"Emby is not working."}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", viewerCSRF)
	req.AddCookie(viewerCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create repair request status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Request models.RepairRequest `json:"request"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Request.ID == "" || created.Request.Status != models.RepairRequestPending || created.Request.RequesterID != "user-1" {
		t.Fatalf("created repair request = %#v", created.Request)
	}

	adminCookie, adminCSRF := loginAdmin(t, router)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/arm", strings.NewReader(`{"armed":true,"duration_seconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(adminCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("arm agent status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/repair-requests/"+created.Request.ID+"/decision", strings.NewReader(`{"choice":"approve"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(adminCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("approve repair request status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.callCount != 1 || collector.called != docker.ActionRestart || collector.app.AppID != "emby" {
		t.Fatalf("repair request did not restart Emby once: count=%d action=%q app=%#v", collector.callCount, collector.called, collector.app)
	}
	stored, err := app.deps.Store.RepairRequestByID(created.Request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.RepairRequestExecuted || stored.Outcome == nil || !stored.Outcome.Recovered {
		t.Fatalf("stored repair request outcome = %#v", stored)
	}
}

func TestGeneralUserDirectRestartCanRestartOptedInApp(t *testing.T) {
	oldDelay := agentRepairVerificationDelay
	agentRepairVerificationDelay = 0
	t.Cleanup(func() { agentRepairVerificationDelay = oldDelay })

	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "general-user-direct-restart")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.AppCatalog.GeneralUserRestartsEnabled = true
	cfg.AppCatalog.RestartAllowedGeneralUser = map[string]bool{"emby": true}

	app := newTestApp(t, cfg)
	collector := &recordingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerID:           "container:Emby",
		ContainerName:         "Emby",
		Category:              "docker",
		DockerState:           models.DockerExited,
		CurrentStatus:         models.StatusOffline,
		VisibleToGeneralUsers: true,
	}},
		afterControlApps: []models.AppStatus{{
			AppID:                 "emby",
			DisplayName:           "Emby",
			ContainerID:           "container:Emby",
			ContainerName:         "Emby",
			Category:              "docker",
			DockerState:           models.DockerRunning,
			CurrentStatus:         models.StatusOnline,
			VisibleToGeneralUsers: true,
		}}}
	app.deps.Collectors.Docker = collector

	snapshot, err := app.Snapshot(context.Background(), models.RoleGeneralUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Apps) != 1 || !snapshot.Apps[0].RestartAllowedGeneralUser || snapshot.Apps[0].AgentRepairAllowed {
		t.Fatalf("general-user snapshot did not expose only user restart opt-in: %#v", snapshot.Apps)
	}

	router := app.Router()
	viewerCookie, viewerCSRF := loginAs(t, router, "viewer", "change-me-now")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/user/apps/emby/restart", strings.NewReader(`{"confirmed":true,"confirm_app_id":"emby"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", viewerCSRF)
	req.AddCookie(viewerCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("direct restart status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Status  string                    `json:"status"`
		Outcome llmAgentRepairOutcomeView `json:"outcome"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "executed" || !response.Outcome.Verified || !response.Outcome.Recovered {
		t.Fatalf("direct restart outcome = %#v", response)
	}
	if response.Outcome.Message != "Restart: restarted - recovered." {
		t.Fatalf("direct restart message = %q", response.Outcome.Message)
	}
	if collector.callCount != 1 || collector.called != docker.ActionRestart || collector.app.AppID != "emby" || !collector.app.RestartAllowedGeneralUser {
		t.Fatalf("direct restart did not restart opted-in app once: count=%d action=%q app=%#v", collector.callCount, collector.called, collector.app)
	}
}

func TestGeneralUserDirectRestartRefusesUnsafeTargets(t *testing.T) {
	cases := []struct {
		name        string
		cfg         func(config.Config) config.Config
		app         models.AppStatus
		wantStatus  int
		wantNoCalls bool
	}{
		{
			name: "global switch off",
			cfg: func(cfg config.Config) config.Config {
				cfg.AppCatalog.RestartAllowedGeneralUser = map[string]bool{"emby": true}
				return cfg
			},
			app: models.AppStatus{
				AppID:                 "emby",
				DisplayName:           "Emby",
				ContainerID:           "container:Emby",
				ContainerName:         "Emby",
				Category:              "docker",
				DockerState:           models.DockerExited,
				CurrentStatus:         models.StatusOffline,
				VisibleToGeneralUsers: true,
			},
			wantStatus:  http.StatusConflict,
			wantNoCalls: true,
		},
		{
			name: "per-app opt-in missing",
			cfg: func(cfg config.Config) config.Config {
				cfg.AppCatalog.GeneralUserRestartsEnabled = true
				cfg.AppCatalog.RestartAllowedGeneralUser = map[string]bool{}
				return cfg
			},
			app: models.AppStatus{
				AppID:                 "emby",
				DisplayName:           "Emby",
				ContainerID:           "container:Emby",
				ContainerName:         "Emby",
				Category:              "docker",
				DockerState:           models.DockerExited,
				CurrentStatus:         models.StatusOffline,
				VisibleToGeneralUsers: true,
			},
			wantStatus:  http.StatusConflict,
			wantNoCalls: true,
		},
		{
			name: "hidden app",
			cfg: func(cfg config.Config) config.Config {
				cfg.AppCatalog.GeneralUserRestartsEnabled = true
				cfg.AppCatalog.RestartAllowedGeneralUser = map[string]bool{"emby": true}
				return cfg
			},
			app: models.AppStatus{
				AppID:                 "emby",
				DisplayName:           "Emby",
				ContainerID:           "container:Emby",
				ContainerName:         "Emby",
				Category:              "docker",
				DockerState:           models.DockerExited,
				CurrentStatus:         models.StatusOffline,
				VisibleToGeneralUsers: false,
			},
			wantStatus:  http.StatusNotFound,
			wantNoCalls: true,
		},
		{
			name: "blacklisted app",
			cfg: func(cfg config.Config) config.Config {
				cfg.AppCatalog.GeneralUserRestartsEnabled = true
				cfg.AppCatalog.RestartAllowedGeneralUser = map[string]bool{"emby": true}
				cfg.Privacy.BlacklistAppIDs = []string{"emby"}
				return cfg
			},
			app: models.AppStatus{
				AppID:                 "emby",
				DisplayName:           "Emby",
				ContainerID:           "container:Emby",
				ContainerName:         "Emby",
				Category:              "docker",
				DockerState:           models.DockerExited,
				CurrentStatus:         models.StatusOffline,
				VisibleToGeneralUsers: true,
			},
			wantStatus:  http.StatusNotFound,
			wantNoCalls: true,
		},
		{
			name: "already online",
			cfg: func(cfg config.Config) config.Config {
				cfg.AppCatalog.GeneralUserRestartsEnabled = true
				cfg.AppCatalog.RestartAllowedGeneralUser = map[string]bool{"emby": true}
				return cfg
			},
			app: models.AppStatus{
				AppID:                 "emby",
				DisplayName:           "Emby",
				ContainerID:           "container:Emby",
				ContainerName:         "Emby",
				Category:              "docker",
				DockerState:           models.DockerRunning,
				CurrentStatus:         models.StatusOnline,
				VisibleToGeneralUsers: true,
			},
			wantStatus:  http.StatusConflict,
			wantNoCalls: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Database.Path = serverCacheTestPath(t, "general-user-direct-restart-"+strings.ReplaceAll(tc.name, " ", "-"))
			cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
			cfg = tc.cfg(cfg)

			app := newTestApp(t, cfg)
			collector := &recordingDockerCollector{apps: []models.AppStatus{tc.app}}
			app.deps.Collectors.Docker = collector

			router := app.Router()
			viewerCookie, viewerCSRF := loginAs(t, router, "viewer", "change-me-now")
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/user/apps/emby/restart", strings.NewReader(`{"confirmed":true,"confirm_app_id":"emby"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", viewerCSRF)
			req.AddCookie(viewerCookie)
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("direct restart status = %d, want %d, body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantNoCalls && collector.callCount != 0 {
				t.Fatalf("direct restart called docker for refused case: count=%d action=%q app=%#v", collector.callCount, collector.called, collector.app)
			}
		})
	}
}

func TestLLMSettingsKeysAreWriteOnly(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "llm-settings-keys")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	body := `{
		"enabled": true,
		"provider": "openai",
		"openai_model": "gpt-test",
		"openai_api_key": "sk-local-test",
		"anthropic_model": "claude-test",
		"timeout": 45000000000
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/llm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/settings/llm status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-local-test") {
		t.Fatalf("llm settings response exposed API key: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"openai_api_key_set":true`) {
		t.Fatalf("llm settings response did not report saved key: %s", rec.Body.String())
	}
	stored, ok, err := app.deps.Store.RuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || stored.LLM.OpenAIAPIKey != "sk-local-test" {
		t.Fatalf("runtime LLM key was not saved: ok=%v settings=%#v", ok, stored.LLM)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/settings/llm", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/settings/llm status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-local-test") {
		t.Fatalf("llm settings GET exposed API key: %s", rec.Body.String())
	}
}

func TestLLMSettingsIncludesAgentReadinessMetadata(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "llm-agent-readiness")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	policy := cfg.LLM.Policies["admin_requested"]
	policy.AgentToolsEnabled = true
	policy.AgentMaxToolCalls = 4
	cfg.LLM.Policies["admin_requested"] = policy

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, _ := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings/llm", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/settings/llm status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response llmSettingsView
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.AgentReadiness.ReadOnlyToolsAvailable || !response.AgentReadiness.MutatingToolsAvailable {
		t.Fatalf("agent readiness availability = %#v", response.AgentReadiness)
	}
	if !response.AgentReadiness.AdminToolsEnabled || response.AgentReadiness.AdminToolCallLimit != 4 {
		t.Fatalf("admin tool readiness = %#v", response.AgentReadiness)
	}
	if len(response.AgentReadiness.ReadOnlyTools) != 4 {
		t.Fatalf("read-only tool count = %d", len(response.AgentReadiness.ReadOnlyTools))
	}
	if len(response.AgentReadiness.ReviewModes) != 4 {
		t.Fatalf("review mode count = %d", len(response.AgentReadiness.ReviewModes))
	}
	if !response.AgentReadiness.OpenCodeAutoReview.ReferenceReviewed || !response.AgentReadiness.OpenCodeAutoReview.SufficientReference {
		t.Fatalf("opencode auto-review summary = %#v", response.AgentReadiness.OpenCodeAutoReview)
	}
}

func TestAgentArmEndpointIsSessionScopedAndConfigGated(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "llm-agent-arm")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.AgentArmDuration = 2 * time.Minute

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/agent/arm", strings.NewReader(`{"armed":true,"duration_seconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("disabled arm status = %d, body = %s", rec.Code, rec.Body.String())
	}

	cfg.LLM.AgentControlEnabled = true
	cfg.Database.Path = serverCacheTestPath(t, "llm-agent-arm-enabled")
	app = newTestApp(t, cfg)
	router = app.Router()
	cookie, csrf = loginAdmin(t, router)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/arm", strings.NewReader(`{"armed":true,"duration_seconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("enabled arm status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var readiness llmAgentReadinessView
	if err := json.NewDecoder(rec.Body).Decode(&readiness); err != nil {
		t.Fatal(err)
	}
	if !readiness.AgentControlEnabled || !readiness.AgentArmed || !readiness.AgentArmedUntil.After(time.Now().UTC()) {
		t.Fatalf("arm readiness = %#v", readiness)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/settings/llm", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET armed settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var settings llmSettingsView
	if err := json.NewDecoder(rec.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if !settings.AgentReadiness.AgentArmed {
		t.Fatalf("session arm state was not reflected in settings: %#v", settings.AgentReadiness)
	}

	otherCookie, _ := loginAdmin(t, router)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/settings/llm", nil)
	req.AddCookie(otherCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET other session settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.AgentReadiness.AgentArmed {
		t.Fatalf("agent arm leaked across sessions: %#v", settings.AgentReadiness)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/agent/arm", strings.NewReader(`{"armed":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disarm status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.AgentArmed {
		t.Fatalf("disarm readiness = %#v", readiness)
	}
}

func TestLLMSettingsChatGPTTokensAreWriteOnlyAndClearable(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "llm-chatgpt-settings")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodChatGPTBrowser
	cfg.LLM.ChatGPTRefreshToken = "refresh-local-test"
	cfg.LLM.ChatGPTAccessToken = "access-local-test"
	cfg.LLM.ChatGPTAccountID = "account-local-test"
	cfg.LLM.ChatGPTTokenExpiresAt = time.Now().UTC().Add(time.Hour)

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings/llm", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/settings/llm status = %d, body = %s", rec.Code, rec.Body.String())
	}
	responseBody := rec.Body.String()
	for _, secret := range []string{"refresh-local-test", "access-local-test", "account-local-test"} {
		if strings.Contains(responseBody, secret) {
			t.Fatalf("llm settings response exposed ChatGPT secret/account value: %s", responseBody)
		}
	}
	if !strings.Contains(responseBody, `"chatgpt_connected":true`) || !strings.Contains(responseBody, `"chatgpt_account_id_set":true`) {
		t.Fatalf("llm settings response did not report ChatGPT connection state: %s", responseBody)
	}

	rec = httptest.NewRecorder()
	body := `{"provider":"openai","openai_auth_method":"api_key","clear_chatgpt_auth":true}`
	req = httptest.NewRequest(http.MethodPost, "/api/admin/settings/llm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/settings/llm status = %d, body = %s", rec.Code, rec.Body.String())
	}
	stored, ok, err := app.deps.Store.RuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("runtime settings were not saved")
	}
	if stored.LLM.ChatGPTRefreshToken != "" || stored.LLM.ChatGPTAccessToken != "" || stored.LLM.ChatGPTAccountID != "" {
		t.Fatalf("ChatGPT auth was not cleared: %#v", stored.LLM)
	}
}

func TestOpenAIChatGPTBrowserAuthorizeURLUsesRegisteredLoopbackRedirect(t *testing.T) {
	authURL, err := url.Parse(buildOpenAIChatGPTAuthorizeURL(openAIPKCE{Challenge: "challenge"}, "state", openAIChatGPTRedirectURI()))
	if err != nil {
		t.Fatal(err)
	}
	wantRedirect := "http://localhost:1455/auth/callback"
	if got := authURL.Query().Get("redirect_uri"); got != wantRedirect {
		t.Fatalf("redirect_uri = %q, want %q", got, wantRedirect)
	}
	if strings.Contains(authURL.Query().Get("redirect_uri"), "/api/admin/") {
		t.Fatalf("browser auth redirect_uri used an unregistered admin callback path: %s", authURL.String())
	}
}

func TestOpenAIChatGPTBrowserAuthRejectsLANOrigin(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "llm-chatgpt-browser-lan")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/llm/openai/browser/start", strings.NewReader(`{}`))
	req.Host = "192.168.1.50:8787"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /api/admin/settings/llm/openai/browser/start status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var response struct {
		Error    string `json:"error"`
		Fallback string `json:"fallback"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Fallback != config.OpenAIAuthMethodChatGPTHeadless || !strings.Contains(response.Error, "localhost") {
		t.Fatalf("LAN browser auth response = %#v", response)
	}
}

func TestOpenAIChatGPTBrowserCallbackDoesNotRequireSession(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "llm-chatgpt-browser-callback")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, openAIChatGPTCallbackPath, nil)
	app.openAIChatGPTBrowserCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET %s status = %d, body = %s", openAIChatGPTCallbackPath, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("callback content type = %q, want text/html", rec.Header().Get("Content-Type"))
	}
}

func TestUserDiagnoseAsAdminUsesCompactLLMRecipientRole(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "compact-diagnose-admin-role")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
	cfg.LLM.OpenAIAPIKey = "sk-test-local"
	cfg.Visibility.DefaultRole = models.RoleGeneralUser
	cfg.Visibility.GeneralUserCanUseLLM = true

	app := newTestApp(t, cfg)
	client := &recordingLLMClient{redactor: app.deps.Redactor}
	app.settingsMu.Lock()
	app.deps.LLM = client
	app.settingsMu.Unlock()

	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/user/diagnose", strings.NewReader(`{"question":"What is wrong with Emby?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/user/diagnose status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if client.request.Policy.RecipientRole != models.RoleGeneralUser {
		t.Fatalf("compact diagnosis recipient role = %q, want %q", client.request.Policy.RecipientRole, models.RoleGeneralUser)
	}
	if client.request.Mode != llm.ModeGeneralUserRequested {
		t.Fatalf("compact diagnosis mode = %q, want %q", client.request.Mode, llm.ModeGeneralUserRequested)
	}
	if client.contextText == "" {
		t.Fatal("recording LLM client did not build context")
	}
	if strings.Contains(client.contextText, `"snapshot"`) {
		t.Fatalf("compact diagnosis used admin snapshot payload: %s", client.contextText)
	}
}

func TestCompactDiagnosisRoleDownscopesAdmin(t *testing.T) {
	tests := []struct {
		name        string
		actorRole   models.Role
		defaultRole models.Role
		want        models.Role
	}{
		{name: "admin becomes general user when default is unset", actorRole: models.RoleAdmin, want: models.RoleGeneralUser},
		{name: "admin cannot inherit admin compact default", actorRole: models.RoleAdmin, defaultRole: models.RoleAdmin, want: models.RoleGeneralUser},
		{name: "admin inherits non-admin compact default", actorRole: models.RoleAdmin, defaultRole: "family", want: "family"},
		{name: "non-admin role is preserved", actorRole: "family", defaultRole: models.RoleGeneralUser, want: "family"},
		{name: "empty role fails closed to general user", want: models.RoleGeneralUser},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactDiagnosisRole(tt.actorRole, tt.defaultRole); got != tt.want {
				t.Fatalf("compactDiagnosisRole(%q, %q) = %q, want %q", tt.actorRole, tt.defaultRole, got, tt.want)
			}
		})
	}
}

func TestIntegrationSettingsPersistAndHideKeys(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "integration-settings")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	body := `{
		"mode": "live",
		"unraid_base_url": "192.168.0.214",
		"unraid_api_key": "unraid-local-test",
		"unraid_ssh_port": 22,
		"unraid_ssh_command": "ssh",
		"unifi_base_url": "192.168.0.1",
		"unifi_api_key": "unifi-local-test",
		"unifi_site_id": "default",
		"unifi_insecure_tls": true,
		"unifi_nas_client_hint": "192.168.0.214",
		"expected_nas_link_mbps": 1000,
		"internet_probe_url": "https://www.gstatic.com/generate_204",
		"dns_probe_host": "cloudflare.com"
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/integrations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/settings/integrations status = %d, body = %s", rec.Code, rec.Body.String())
	}
	responseBody := rec.Body.String()
	if strings.Contains(responseBody, "unraid-local-test") || strings.Contains(responseBody, "unifi-local-test") {
		t.Fatalf("integration settings response exposed API keys: %s", responseBody)
	}
	if !strings.Contains(responseBody, `"unraid_base_url":"http://192.168.0.214"`) || !strings.Contains(responseBody, `"unifi_base_url":"https://192.168.0.1"`) {
		t.Fatalf("integration settings response did not normalize URLs: %s", responseBody)
	}
	if !strings.Contains(responseBody, `"unraid_api_key_set":true`) || !strings.Contains(responseBody, `"unifi_api_key_set":true`) {
		t.Fatalf("integration settings response did not report saved keys: %s", responseBody)
	}
	if !strings.Contains(responseBody, `"unifi_nas_client_hint":"192.168.0.214"`) || !strings.Contains(responseBody, `"expected_nas_link_mbps":1000`) {
		t.Fatalf("integration settings response did not include NAS link settings: %s", responseBody)
	}

	stored, ok, err := app.deps.Store.RuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("integration settings were not persisted")
	}
	if stored.Integrations.UnraidBaseURL != "http://192.168.0.214" || stored.Integrations.UnraidAPIKey != "unraid-local-test" {
		t.Fatalf("stored Unraid integration = %#v", stored.Integrations)
	}
	if stored.Integrations.UniFiBaseURL != "https://192.168.0.1" || stored.Integrations.UniFiAPIKey != "unifi-local-test" {
		t.Fatalf("stored UniFi integration = %#v", stored.Integrations)
	}
	if stored.Integrations.UniFiNASClientHint != "192.168.0.214" || stored.Integrations.ExpectedNASLinkMbps != 1000 {
		t.Fatalf("stored NAS link integration = %#v", stored.Integrations)
	}

	reloaded := newTestApp(t, cfg)
	if reloaded.deps.Config.Integrations.UnraidAPIKey != "unraid-local-test" {
		t.Fatal("saved Unraid API key was not applied after reload")
	}
	if reloaded.deps.Config.Integrations.UniFiAPIKey != "unifi-local-test" {
		t.Fatal("saved UniFi API key was not applied after reload")
	}
	if reloaded.deps.Config.Integrations.UniFiNASClientHint != "192.168.0.214" || reloaded.deps.Config.Integrations.ExpectedNASLinkMbps != 1000 {
		t.Fatalf("saved NAS link settings were not applied after reload: %#v", reloaded.deps.Config.Integrations)
	}
}

func TestSiteConfigIdentifiesAdminAndCompactRouters(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "site-config")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	cases := []struct {
		name   string
		router http.Handler
		want   string
	}{
		{name: "admin", router: app.Router(), want: `"admin"`},
		{name: "compact", router: app.CompactRouter(), want: `"compact"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/site-config.js", nil)
			tc.router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /site-config.js status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("site config body = %q, want %s", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestCompactRouterExcludesAdminAPI(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "compact-router")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.CompactRouter()
	cookie, csrf := loginAdmin(t, router)

	adminCases := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/admin/status/full"},
		{method: http.MethodPost, path: "/api/admin/apps/emby/action", body: `{"action":"restart"}`},
		{method: http.MethodPost, path: "/api/admin/agent/arm", body: `{"armed":true}`},
		{method: http.MethodPost, path: "/api/admin/agent/approval", body: `{"choice":"allow_once"}`},
		{method: http.MethodGet, path: "/api/admin/settings/integrations"},
		{method: http.MethodPost, path: "/api/admin/settings/llm/openai/browser/start", body: `{}`},
		{method: http.MethodGet, path: "/api/admin/settings/llm/openai/browser/callback?code=test&state=test"},
		{method: http.MethodPost, path: "/api/admin/settings/llm/openai/browser/finish", body: `{"poll_id":"test"}`},
	}
	for _, tc := range adminCases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(cookie)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, body = %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status/summary", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/status/summary status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCustomRoleSettingsHideAppsIncidentsAndFacts(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "custom-role")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.FixtureScenario = "single_container_exited"

	app := newTestApp(t, cfg)
	router := app.Router()
	adminCookie, adminCSRF := loginAdmin(t, router)

	roleBody := `{
		"default_role": "general_user",
		"general_user_can_use_llm": true,
		"show_nas_status_to_users": true,
		"show_wan_status_to_users": true,
		"roles": [{
			"role": "kids",
			"display_name": "Kids",
			"can_use_llm": true,
			"show_nas_status_to_users": true,
			"show_wan_status_to_users": true,
			"hidden_app_ids": ["emby"]
		}]
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/roles", strings.NewReader(roleBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(adminCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/settings/roles status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(`{"username":"kid","display_name":"Kid","role":"kids","password":"change-me-now"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(adminCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/users status = %d, body = %s", rec.Code, rec.Body.String())
	}

	kidCookie, _ := loginAs(t, router, "kid", "change-me-now")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/status/summary", nil)
	req.AddCookie(kidCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/status/summary status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var snapshot models.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Apps) != 0 {
		t.Fatalf("hidden role app leaked: %#v", snapshot.Apps)
	}
	if len(snapshot.Incidents) != 0 || len(snapshot.Facts) != 0 {
		t.Fatalf("hidden role app incidents/facts leaked: incidents=%#v facts=%#v", snapshot.Incidents, snapshot.Facts)
	}
}

func TestCrossSiteMutatingRequestsAreRejected(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "origin-check")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"change-me-now"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site login status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"change-me-now"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://evil.example/login")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site referer login status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSameOriginRefererMutatingRequestIsAllowed(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "same-origin-referer")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"change-me-now"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "http://example.com/login")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin referer login status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestLoginFormDoesNotShipDefaultPassword(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "login-form")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login form status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `value="change-me-now"`) {
		t.Fatal("login form ships the default password value")
	}
}

func TestSecurityHeadersAndAPINoStore(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "security-headers")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"base-uri 'self'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("CSP missing %q: %s", directive, csp)
		}
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}

	cookie, _ := loginAdmin(t, router)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/status/summary", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("API Cache-Control = %q", got)
	}
}

func TestHSTSHeaderOnlyWhenPublicURLIsHTTPS(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "hsts-default")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("default local HSTS = %q", got)
	}

	cfg = config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "hsts-https")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.Server.PublicURL = "https://status.example.com"

	app = newTestApp(t, cfg)
	router = app.Router()

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)
	if got := rec.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("HTTPS HSTS = %q", got)
	}
}

func TestLargeRequestBodiesAreRejected(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "body-limit")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()

	body := strings.NewReader(strings.Repeat("x", int(maxRequestBodyBytes)+1))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large login body status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestLoginFailuresAreRateLimited(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "login-rate-limit")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()

	for i := 0; i < maxLoginFailures; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed login %d status = %d, body = %s", i+1, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"change-me-now"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limited login did not set Retry-After")
	}
}

func TestSessionStorePrunesExpiredEntriesAndCapsActiveSessions(t *testing.T) {
	user := users.User{ID: "user-1", Username: "admin", Role: models.RoleAdmin}

	expiring := newSessionStore(time.Nanosecond)
	expired, err := expiring.create(user)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, ok := expiring.get(expired.Token); ok {
		t.Fatal("expired session was still valid")
	}
	if len(expiring.entries) != 0 {
		t.Fatalf("expired session was not pruned: %#v", expiring.entries)
	}

	capped := newSessionStore(time.Hour)
	var sessions []session
	for i := 0; i < maxSessionEntries+1; i++ {
		created, err := capped.create(user)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, created)
	}
	if len(capped.entries) != maxSessionEntries {
		t.Fatalf("session count = %d, want %d", len(capped.entries), maxSessionEntries)
	}
	if _, ok := capped.get(sessions[len(sessions)-1].Token); !ok {
		t.Fatal("newest session should remain valid")
	}
}

func TestLoginLimiterPrunesExpiredEntriesAndCapsFailureKeys(t *testing.T) {
	limiter := newLoginLimiter()
	limiter.failures["old"] = loginAttempt{Failures: 1, FirstFailure: time.Now().UTC().Add(-loginFailureWindow - time.Second)}
	limiter.retryAfter("missing")
	if len(limiter.failures) != 0 {
		t.Fatalf("expired login failure was not pruned: %#v", limiter.failures)
	}

	base := time.Now().UTC()
	for i := 0; i < maxLoginFailureKeys; i++ {
		limiter.failures[fmt.Sprintf("key-%04d", i)] = loginAttempt{
			Failures:     1,
			FirstFailure: base.Add(time.Duration(i) * time.Nanosecond),
		}
	}
	limiter.recordFailure("newest")
	if len(limiter.failures) != maxLoginFailureKeys {
		t.Fatalf("login failure key count = %d, want %d", len(limiter.failures), maxLoginFailureKeys)
	}
	if _, ok := limiter.failures["key-0000"]; ok {
		t.Fatal("oldest login failure key was not evicted")
	}
	if _, ok := limiter.failures["newest"]; !ok {
		t.Fatal("newest login failure key should remain")
	}
}

func TestLogoutRequiresCSRF(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "logout-csrf")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{}`))
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{}`))
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout with CSRF status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSnapshotDegradesInsteadOfFailingWhenLiveCollectorsFail(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "collector-failure")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	app.deps.Collectors.Unraid = failingUnraidCollector{}
	app.deps.Collectors.Docker = failingDockerCollector{}
	app.deps.Collectors.UniFi = failingUniFiCollector{}

	snapshot, err := app.Snapshot(context.Background(), users.AdminRole)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Infrastructure.UnraidAPIReachable {
		t.Fatal("unraid API should be marked unreachable after collector failure")
	}
	if snapshot.Infrastructure.DockerServiceAvailable {
		t.Fatal("docker should be marked unavailable after collector failure")
	}
	if snapshot.Infrastructure.SourceHealth.Unraid == "" || snapshot.Infrastructure.SourceHealth.Docker == "" || snapshot.Infrastructure.SourceHealth.UniFi == "" {
		t.Fatalf("source health did not preserve collector errors: %#v", snapshot.Infrastructure.SourceHealth)
	}
}

func TestLatestSnapshotUsesCacheAndDoesNotMutateAdminSnapshot(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "latest-snapshot-cache")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	app := newTestApp(t, cfg)
	collector := &countingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "EmbyServer",
		VisibleToGeneralUsers: true,
		DockerState:           models.DockerRunning,
		DockerHealth:          models.HealthHealthy,
		CurrentStatus:         models.StatusOnline,
		RecentLogs:            []models.LogLine{{Line: "admin-only log"}},
	}}}
	app.deps.Collectors.Docker = collector

	adminSnapshot, err := app.latestSnapshot(context.Background(), models.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if collector.Calls() != 1 {
		t.Fatalf("collector calls after first latest snapshot = %d", collector.Calls())
	}
	generalSnapshot, err := app.latestSnapshot(context.Background(), models.RoleGeneralUser)
	if err != nil {
		t.Fatal(err)
	}
	if collector.Calls() != 1 {
		t.Fatalf("cached latest snapshot should not recollect, calls = %d", collector.Calls())
	}
	if len(generalSnapshot.Apps) != 1 || generalSnapshot.Apps[0].ContainerName != "" || len(generalSnapshot.Apps[0].RecentLogs) != 0 {
		t.Fatalf("general snapshot leaked admin-only app fields: %#v", generalSnapshot.Apps)
	}
	adminAgain, err := app.latestSnapshot(context.Background(), models.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminAgain.Apps) != 1 || adminAgain.Apps[0].ContainerName != "EmbyServer" || len(adminAgain.Apps[0].RecentLogs) != 1 {
		t.Fatalf("general filtering mutated cached admin snapshot: before=%#v after=%#v", adminSnapshot.Apps, adminAgain.Apps)
	}
}

func TestInvalidateSnapshotForcesNextLatestSnapshotRefresh(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "latest-snapshot-invalidate")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	app := newTestApp(t, cfg)
	collector := &countingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "EmbyServer",
		VisibleToGeneralUsers: true,
		DockerState:           models.DockerRunning,
		DockerHealth:          models.HealthHealthy,
		CurrentStatus:         models.StatusOnline,
	}}}
	app.deps.Collectors.Docker = collector
	if _, err := app.latestSnapshot(context.Background(), models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	app.invalidateSnapshot()
	if _, err := app.latestSnapshot(context.Background(), models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if collector.Calls() != 2 {
		t.Fatalf("collector calls after invalidated latest snapshot = %d", collector.Calls())
	}
}

func TestRunPollerPopulatesSnapshotCache(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "poller-cache")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	app := newTestApp(t, cfg)
	called := make(chan struct{}, 1)
	app.deps.Collectors.Docker = &notifyingDockerCollector{
		apps: []models.AppStatus{{
			AppID:                 "emby",
			DisplayName:           "Emby",
			ContainerName:         "EmbyServer",
			VisibleToGeneralUsers: true,
			DockerState:           models.DockerRunning,
			DockerHealth:          models.HealthHealthy,
			CurrentStatus:         models.StatusOnline,
		}},
		called: called,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.RunPoller(ctx, time.Hour)
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not collect an initial snapshot")
	}
	snapshot, err := app.latestSnapshot(context.Background(), models.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Apps) != 1 || snapshot.Apps[0].AppID != "emby" {
		t.Fatalf("poller cache snapshot = %#v", snapshot.Apps)
	}
}

func TestPollerRefreshRecordsStatusHistoryTransitions(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "poller-history")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	app := newTestApp(t, cfg)
	collector := &countingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "EmbyServer",
		VisibleToGeneralUsers: true,
		DockerState:           models.DockerRunning,
		DockerHealth:          models.HealthHealthy,
		CurrentStatus:         models.StatusOnline,
		ServerSummary:         "Emby is working.",
	}}}
	app.deps.Collectors.Docker = collector
	if _, err := app.refreshSnapshot(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	events, err := app.deps.History.Query(db.HistoryFilter{SubjectType: models.SubjectApp, SubjectID: "emby"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("baseline should not emit history events: %#v", events)
	}
	collector.mu.Lock()
	collector.apps = []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "EmbyServer",
		VisibleToGeneralUsers: true,
		DockerState:           models.DockerExited,
		DockerHealth:          models.HealthUnknown,
		CurrentStatus:         models.StatusOffline,
		ServerSummary:         "Emby is not working.",
	}}
	collector.mu.Unlock()
	if _, err := app.refreshSnapshot(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	events, err = app.deps.History.Query(db.HistoryFilter{SubjectType: models.SubjectApp, SubjectID: "emby"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].From != models.StatusOnline || events[0].To != models.StatusOffline {
		t.Fatalf("unexpected history events: %#v", events)
	}
}

func TestStatusRefreshEndpointRequiresCSRFAndRecordsHistory(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "manual-refresh-history")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	app := newTestApp(t, cfg)
	collector := &countingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "EmbyServer",
		VisibleToGeneralUsers: true,
		DockerState:           models.DockerRunning,
		DockerHealth:          models.HealthHealthy,
		CurrentStatus:         models.StatusOnline,
		ServerSummary:         "Emby is working.",
	}}}
	app.deps.Collectors.Docker = collector
	if _, err := app.refreshSnapshot(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	collector.mu.Lock()
	collector.apps = []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "EmbyServer",
		VisibleToGeneralUsers: true,
		DockerState:           models.DockerExited,
		DockerHealth:          models.HealthUnknown,
		CurrentStatus:         models.StatusOffline,
		ServerSummary:         "Emby is not working.",
	}}
	collector.mu.Unlock()

	router := app.Router()
	cookie, csrf := loginAs(t, router, "viewer", "change-me-now")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/status/refresh", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("refresh without CSRF status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/status/refresh", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response models.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Apps) != 1 || response.Apps[0].AppID != "emby" || response.Apps[0].CurrentStatus != models.StatusOffline {
		t.Fatalf("refresh response did not include updated visible app: %#v", response.Apps)
	}
	events, err := app.deps.History.Query(db.HistoryFilter{SubjectType: models.SubjectApp, SubjectID: "emby"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].From != models.StatusOnline || events[0].To != models.StatusOffline {
		t.Fatalf("manual refresh did not record transition: %#v", events)
	}
}

func TestAppHistoryEndpointReturnsVisibleAppHistory(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "app-history-endpoint")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	app := newTestApp(t, cfg)
	app.deps.Collectors.Docker = &countingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "EmbyServer",
		VisibleToGeneralUsers: true,
		DockerState:           models.DockerExited,
		DockerHealth:          models.HealthUnknown,
		CurrentStatus:         models.StatusOffline,
		ServerSummary:         "Emby is not working.",
	}}}
	if _, err := app.refreshSnapshot(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := app.deps.History.Append([]models.StatusEvent{{
		ID:          "event-1",
		SubjectType: models.SubjectApp,
		SubjectID:   "emby",
		DisplayName: "Emby",
		From:        models.StatusOnline,
		To:          models.StatusOffline,
		At:          now.Add(-time.Minute),
		Note:        "Emby stopped responding.",
	}}); err != nil {
		t.Fatal(err)
	}
	router := app.Router()
	cookie, _ := loginAs(t, router, "viewer", "change-me-now")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/apps/emby/history?window=1d&limit=10", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET app history status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response models.StatusHistory
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.SubjectType != models.SubjectApp || response.SubjectID != "emby" || response.Current != models.StatusOffline || len(response.Events) != 1 {
		t.Fatalf("unexpected app history response: %#v", response)
	}
}

func TestAppHistoryEndpointDoesNotLeakHiddenApp(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "app-history-hidden")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.Visibility.HiddenAppIDs = []string{"emby"}
	app := newTestApp(t, cfg)
	app.deps.Collectors.Docker = &countingDockerCollector{apps: []models.AppStatus{{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "EmbyServer",
		VisibleToGeneralUsers: true,
		DockerState:           models.DockerRunning,
		DockerHealth:          models.HealthHealthy,
		CurrentStatus:         models.StatusOnline,
	}}}
	if _, err := app.refreshSnapshot(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	router := app.Router()
	cookie, _ := loginAs(t, router, "viewer", "change-me-now")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/apps/emby/history", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("hidden app history status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestInfrastructureHistoryEndpointHonorsRoleVisibility(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "infra-history-visibility")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.Visibility.ShowWANStatusToUsers = false
	app := newTestApp(t, cfg)
	if _, err := app.refreshSnapshot(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	router := app.Router()
	viewerCookie, _ := loginAs(t, router, "viewer", "change-me-now")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/infrastructure/history?subject=wan", nil)
	req.AddCookie(viewerCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("general user WAN history status = %d, body = %s", rec.Code, rec.Body.String())
	}

	adminCookie, _ := loginAdmin(t, router)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/infrastructure/history?subject=wan", nil)
	req.AddCookie(adminCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin WAN history status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestInfrastructureHistoryEndpointUsesPlainLanguageForGeneralUsers(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "infra-history-plain-language")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	app := newTestApp(t, cfg)
	if _, err := app.refreshSnapshot(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := app.deps.History.Append([]models.StatusEvent{
		{
			ID:          "nas-event",
			SubjectType: models.SubjectInfra,
			SubjectID:   "nas",
			DisplayName: "NAS",
			From:        models.StatusOnline,
			To:          models.StatusOffline,
			At:          now.Add(-time.Minute),
			Note:        "NAS is not reachable.",
		},
		{
			ID:          "dns-event",
			SubjectType: models.SubjectInfra,
			SubjectID:   "dns",
			DisplayName: "DNS",
			From:        models.StatusOffline,
			To:          models.StatusOnline,
			At:          now.Add(-time.Minute),
			Note:        "DNS is resolving.",
		},
	}); err != nil {
		t.Fatal(err)
	}

	router := app.Router()
	viewerCookie, _ := loginAs(t, router, "viewer", "change-me-now")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/infrastructure/history?subject=nas", nil)
	req.AddCookie(viewerCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("general user NAS history status = %d, body = %s", rec.Code, rec.Body.String())
	}
	bodyText := rec.Body.String()
	var response models.StatusHistory
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.DisplayName != "Server" {
		t.Fatalf("general user infra display name = %q, want Server", response.DisplayName)
	}
	if len(response.Events) != 1 {
		t.Fatalf("general user NAS history events = %#v", response.Events)
	}
	if strings.Contains(bodyText, "NAS") || strings.Contains(bodyText, "DNS") || strings.Contains(strings.ToLower(bodyText), "unraid") || strings.Contains(strings.ToLower(bodyText), "wan") {
		t.Fatalf("general user infra history leaked technical wording: %s", bodyText)
	}
	if !strings.Contains(bodyText, "Server is not responding.") {
		t.Fatalf("general user infra history did not rewrite note plainly: %s", bodyText)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/infrastructure/history?subject=dns", nil)
	req.AddCookie(viewerCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("general user DNS history status = %d, body = %s", rec.Code, rec.Body.String())
	}

	adminCookie, _ := loginAdmin(t, router)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/infrastructure/history?subject=dns", nil)
	req.AddCookie(adminCookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin DNS history status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAppIconEndpointPersistsAndAppliesOverride(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "app-icon")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")
	cfg.FixtureScenario = "single_container_exited"

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/apps/emby/icon", strings.NewReader(`{"icon_url":"https://example.invalid/custom-emby.png"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/apps/emby/icon status = %d, body = %s", rec.Code, rec.Body.String())
	}

	snapshot, err := app.Snapshot(context.Background(), users.AdminRole)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Apps) != 1 {
		t.Fatalf("apps len = %d", len(snapshot.Apps))
	}
	if snapshot.Apps[0].IconURL != "https://example.invalid/custom-emby.png" || snapshot.Apps[0].IconSource != "custom" {
		t.Fatalf("icon override was not applied: %#v", snapshot.Apps[0])
	}

	reloaded := newTestApp(t, cfg)
	reloadedSnapshot, err := reloaded.Snapshot(context.Background(), users.AdminRole)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedSnapshot.Apps[0].IconURL != "https://example.invalid/custom-emby.png" {
		t.Fatalf("icon override did not persist: %#v", reloadedSnapshot.Apps[0])
	}
}

func TestBuiltInAppIconsApplyWithoutOverridingExistingIcons(t *testing.T) {
	apps := []models.AppStatus{
		{AppID: "emby", DisplayName: "Emby", ContainerName: "emby"},
		{AppID: "postgres", DisplayName: "Postgres", ContainerName: "postgres"},
		{AppID: "custom-app", DisplayName: "Custom App", ContainerName: "custom-app", DockerState: models.DockerRunning},
		{AppID: "plex", DisplayName: "Plex", IconURL: "https://example.invalid/plex.png", IconSource: "docker-label"},
	}

	applyAppCatalog(apps, config.AppCatalogConfig{})

	if apps[0].IconURL != "/app-icons/media-server.svg" || apps[0].IconSource != "built-in" {
		t.Fatalf("known media app icon = %#v", apps[0])
	}
	if apps[1].IconURL != "/app-icons/database.svg" || apps[1].IconSource != "built-in" {
		t.Fatalf("database app icon = %#v", apps[1])
	}
	if apps[2].IconURL != "/app-icons/container.svg" || apps[2].IconSource != "built-in" {
		t.Fatalf("generic docker app icon = %#v", apps[2])
	}
	if apps[3].IconURL != "https://example.invalid/plex.png" || apps[3].IconSource != "docker-label" {
		t.Fatalf("existing icon was overwritten: %#v", apps[3])
	}
}

func TestAppCatalogSettingsPersistRepairOptIn(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "app-catalog-repair-opt-in")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/apps", strings.NewReader(`{
		"icon_overrides":{" emby ":"/app-icons/media-server.svg"},
		"agent_repair_allowed":{" emby ":true,"plex":false," ":true},
		"general_user_restarts_enabled":true,
		"restart_allowed_general_user":{" emby ":true,"plex":false," ":true}
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST app catalog settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response config.AppCatalogConfig
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.IconOverrides["emby"] != "/app-icons/media-server.svg" {
		t.Fatalf("icon overrides were not normalized: %#v", response.IconOverrides)
	}
	if !response.AgentRepairAllowed["emby"] || response.AgentRepairAllowed["plex"] {
		t.Fatalf("repair opt-in map was not normalized: %#v", response.AgentRepairAllowed)
	}
	if !response.GeneralUserRestartsEnabled || !response.RestartAllowedGeneralUser["emby"] || response.RestartAllowedGeneralUser["plex"] {
		t.Fatalf("user restart opt-in map was not normalized: %#v", response)
	}
	stored, ok, err := app.deps.Store.RuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !stored.AppCatalog.AgentRepairAllowed["emby"] {
		t.Fatalf("repair opt-in was not persisted: ok=%v settings=%#v", ok, stored.AppCatalog)
	}
	if !ok || !stored.AppCatalog.GeneralUserRestartsEnabled || !stored.AppCatalog.RestartAllowedGeneralUser["emby"] {
		t.Fatalf("user restart opt-in was not persisted: ok=%v settings=%#v", ok, stored.AppCatalog)
	}
}

func TestRoleSettingsUsesCurrentDockerSnapshotApps(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "role-settings-live-apps")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	app.deps.Collectors.Docker = &recordingDockerCollector{apps: []models.AppStatus{
		{
			AppID:         "jellyfin",
			DisplayName:   "Jellyfin",
			ContainerName: "jellyfin",
			DockerState:   models.DockerRunning,
			CurrentStatus: models.StatusOnline,
			DataSource:    "unraid-docker",
		},
		{
			AppID:         "actual-budget",
			DisplayName:   "Actual Budget",
			ContainerName: "actual-budget",
			DockerState:   models.DockerRunning,
			CurrentStatus: models.StatusOnline,
			DataSource:    "unraid-docker",
		},
	}}
	router := app.Router()
	cookie, _ := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings/roles", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/settings/roles status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Apps []models.AppStatus `json:"apps"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Apps) != 2 {
		t.Fatalf("role settings app count = %d, apps = %#v", len(response.Apps), response.Apps)
	}
	got := []string{response.Apps[0].AppID, response.Apps[1].AppID}
	if strings.Join(got, ",") != "jellyfin,actual-budget" {
		t.Fatalf("role settings apps came from unexpected source: %#v", got)
	}
}

func TestAdminAppActionControlsResolvedDockerApp(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "app-action")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	collector := &recordingDockerCollector{apps: []models.AppStatus{{
		AppID:         "emby",
		DisplayName:   "Emby",
		ContainerID:   "container:Emby",
		ContainerName: "Emby",
		Category:      "docker",
		DockerState:   models.DockerRunning,
		CurrentStatus: models.StatusOnline,
	}}}
	app.deps.Collectors.Docker = collector
	router := app.Router()
	cookie, csrf := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/apps/emby/action", strings.NewReader(`{"action":"restart"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed restart status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.called != "" {
		t.Fatalf("unconfirmed restart should not call docker collector: action=%q", collector.called)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/apps/emby/action", strings.NewReader(`{"action":"restart","confirmed":true,"confirm_app_id":"wrong-app"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched restart confirmation status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.called != "" {
		t.Fatalf("mismatched restart confirmation should not call docker collector: action=%q", collector.called)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/apps/emby/action", strings.NewReader(`{"action":"restart","confirmed":true,"confirm_app_id":"emby"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /api/admin/apps/emby/action status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.called != docker.ActionRestart || collector.app.ContainerID != "container:Emby" {
		t.Fatalf("docker action was not resolved from snapshot: action=%q app=%#v", collector.called, collector.app)
	}
}

func TestAdminAppLogsAreResolvedLimitedAndRedacted(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "app-logs")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	collector := &recordingDockerCollector{
		apps: []models.AppStatus{{
			AppID:         "emby",
			DisplayName:   "Emby",
			ContainerID:   "container:Emby",
			ContainerName: "Emby",
			Category:      "docker",
			DockerState:   models.DockerRunning,
			CurrentStatus: models.StatusOnline,
		}},
		logs: []models.LogLine{
			{Source: "Emby", Line: "old line"},
			{Source: "Emby", Line: "password=super-secret"},
		},
	}
	app.deps.Collectors.Docker = collector
	router := app.Router()
	cookie, _ := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/apps/emby/logs?limit=1", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/apps/emby/logs status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if collector.logApp.ContainerID != "container:Emby" || collector.logOptions.Limit != 1 {
		t.Fatalf("docker logs were not resolved from snapshot: app=%#v opts=%#v", collector.logApp, collector.logOptions)
	}
	var response struct {
		Redacted bool             `json:"redacted"`
		Logs     []models.LogLine `json:"logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Logs) != 1 {
		t.Fatalf("log count = %d, logs = %#v", len(response.Logs), response.Logs)
	}
	if !response.Redacted || strings.Contains(response.Logs[0].Line, "super-secret") || !strings.Contains(response.Logs[0].Line, "[REDACTED]") {
		t.Fatalf("logs were not redacted before response: %#v", response)
	}
}

func TestAdminAppLogsRejectGeneralUser(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.Path = serverCacheTestPath(t, "app-logs-general-user")
	cfg.FixtureDir = filepath.Join("..", "..", "fixtures")

	app := newTestApp(t, cfg)
	router := app.Router()
	cookie, _ := loginAs(t, router, "viewer", "change-me-now")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/apps/emby/logs", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("general user app logs status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

type failingUnraidCollector struct{}

func (failingUnraidCollector) Status(context.Context) (models.InfrastructureStatus, []models.LogLine, error) {
	return models.InfrastructureStatus{}, nil, errors.New("unraid unavailable")
}

type failingDockerCollector struct{}

func (failingDockerCollector) Apps(context.Context) ([]models.AppStatus, error) {
	return nil, errors.New("docker unavailable")
}

func (failingDockerCollector) ControlContainer(context.Context, models.AppStatus, docker.ContainerAction) (docker.ControlResult, error) {
	return docker.ControlResult{}, errors.New("docker unavailable")
}

func (failingDockerCollector) Logs(context.Context, models.AppStatus, docker.LogOptions) ([]models.LogLine, error) {
	return nil, errors.New("docker unavailable")
}

type countingDockerCollector struct {
	mu    sync.Mutex
	calls int
	apps  []models.AppStatus
}

func (c *countingDockerCollector) Apps(context.Context) ([]models.AppStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return append([]models.AppStatus(nil), c.apps...), nil
}

func (c *countingDockerCollector) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingDockerCollector) ControlContainer(context.Context, models.AppStatus, docker.ContainerAction) (docker.ControlResult, error) {
	return docker.ControlResult{}, nil
}

func (c *countingDockerCollector) Logs(context.Context, models.AppStatus, docker.LogOptions) ([]models.LogLine, error) {
	return nil, nil
}

type notifyingDockerCollector struct {
	apps   []models.AppStatus
	called chan<- struct{}
}

func (c *notifyingDockerCollector) Apps(context.Context) ([]models.AppStatus, error) {
	select {
	case c.called <- struct{}{}:
	default:
	}
	return append([]models.AppStatus(nil), c.apps...), nil
}

func (c *notifyingDockerCollector) ControlContainer(context.Context, models.AppStatus, docker.ContainerAction) (docker.ControlResult, error) {
	return docker.ControlResult{}, nil
}

func (c *notifyingDockerCollector) Logs(context.Context, models.AppStatus, docker.LogOptions) ([]models.LogLine, error) {
	return nil, nil
}

type recordingDockerCollector struct {
	apps             []models.AppStatus
	afterControlApps []models.AppStatus
	logs             []models.LogLine
	called           docker.ContainerAction
	callCount        int
	app              models.AppStatus
	logApp           models.AppStatus
	logOptions       docker.LogOptions
}

func (c *recordingDockerCollector) Apps(context.Context) ([]models.AppStatus, error) {
	return c.apps, nil
}

func (c *recordingDockerCollector) ControlContainer(_ context.Context, app models.AppStatus, action docker.ContainerAction) (docker.ControlResult, error) {
	c.called = action
	c.callCount++
	c.app = app
	if len(c.afterControlApps) > 0 {
		c.apps = append([]models.AppStatus(nil), c.afterControlApps...)
	}
	return docker.ControlResult{
		Action:        action,
		AppID:         app.AppID,
		ContainerID:   app.ContainerID,
		ContainerName: app.ContainerName,
		DockerState:   app.DockerState,
		Status:        "accepted",
	}, nil
}

func (c *recordingDockerCollector) Logs(_ context.Context, app models.AppStatus, opts docker.LogOptions) ([]models.LogLine, error) {
	c.logApp = app
	c.logOptions = opts
	if opts.Limit > 0 && len(c.logs) > opts.Limit {
		return append([]models.LogLine(nil), c.logs[len(c.logs)-opts.Limit:]...), nil
	}
	return append([]models.LogLine(nil), c.logs...), nil
}

type failingUniFiCollector struct{}

func (failingUniFiCollector) Status(context.Context) (models.InfrastructureStatus, error) {
	return models.InfrastructureStatus{}, errors.New("unifi unavailable")
}

type recordingLLMClient struct {
	redactor       *privacy.Redactor
	diagnosis      llm.Diagnosis
	request        llm.Request
	contextText    string
	reviewDecision llm.ActionReviewDecision
	reviewErr      error
	reviewRequest  llm.ActionReviewRequest
	reviewCalls    int
}

func (c *recordingLLMClient) Diagnose(_ context.Context, req llm.Request) (llm.Diagnosis, error) {
	c.request = req
	redactor := c.redactor
	if redactor == nil {
		redactor = privacy.NewRedactor(config.PrivacyConfig{})
	}
	contextText, err := llm.NewContextBuilder(redactor).Build(req)
	if err != nil {
		return llm.Diagnosis{}, err
	}
	c.contextText = contextText
	if c.diagnosis.Diagnosis != "" {
		return c.diagnosis, nil
	}
	return llm.Diagnosis{
		Severity:            models.SeverityNone,
		Confidence:          0.98,
		IncidentType:        models.IncidentUnknown,
		Diagnosis:           "No problem found.",
		Evidence:            []string{"Compact diagnosis request completed."},
		GeneralUserSummary:  "Everything visible looks normal.",
		AdminMessage:        "Compact diagnosis request completed.",
		RecommendedActionID: "none",
		ShouldNotifyAdmin:   false,
	}, nil
}

func (c *recordingLLMClient) ReviewAction(_ context.Context, req llm.ActionReviewRequest) (llm.ActionReviewDecision, error) {
	c.reviewRequest = req
	c.reviewCalls++
	if c.reviewErr != nil {
		return llm.ActionReviewDecision{}, c.reviewErr
	}
	if c.reviewDecision.Summary != "" {
		return c.reviewDecision, nil
	}
	return llm.ActionReviewDecision{
		Allow:      true,
		Confidence: 0.95,
		Summary:    "Test reviewer allowed the action.",
		Issues:     nil,
		CheckedAt:  time.Now().UTC(),
	}, nil
}

type usageLimitLLMClient struct{}

func (usageLimitLLMClient) Diagnose(context.Context, llm.Request) (llm.Diagnosis, error) {
	return llm.Diagnosis{}, &llm.ProviderError{
		Label:      "openai responses api",
		StatusCode: http.StatusTooManyRequests,
		Code:       llm.OpenAIUsageLimitCode,
		Message:    "OpenAI usage limit reached.",
	}
}

func (usageLimitLLMClient) ReviewAction(context.Context, llm.ActionReviewRequest) (llm.ActionReviewDecision, error) {
	return llm.ActionReviewDecision{}, &llm.ProviderError{
		Label:      "openai action review api",
		StatusCode: http.StatusTooManyRequests,
		Code:       llm.OpenAIUsageLimitCode,
		Message:    "OpenAI usage limit reached.",
	}
}

func newTestApp(t *testing.T, cfg config.Config) *App {
	t.Helper()
	store, err := db.OpenFileStore(cfg.Database.Path)
	if err != nil {
		t.Fatal(err)
	}
	historyStore, err := db.OpenFileHistoryStore(db.HistoryPathForDatabase(cfg.Database.Path), cfg.Retention.MaxStatusEventsPerSubject)
	if err != nil {
		t.Fatal(err)
	}
	redactor := privacy.NewRedactor(cfg.Privacy)
	auditor := audit.New(store, redactor)
	notifier := notifications.NewManager(store, notifications.NewMockBackend(), cfg.Notifications, auditor)
	app, err := New(Dependencies{
		Config: cfg,
		Collectors: Collectors{
			Unraid: unraid.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			Docker: docker.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			UniFi:  unifi.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
			Probes: probes.NewFixtureClient(cfg.FixtureDir, cfg.FixtureScenario),
		},
		Store:         store,
		History:       historyStore,
		Users:         users.NewRegistry(store, cfg.Auth),
		Audit:         auditor,
		Notifications: notifier,
		Redactor:      redactor,
		Diagnostics:   diagnostics.NewRuleEngine(),
		LLM:           llm.NewClient(cfg.LLM, redactor),
		Version:       "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func loginAdmin(t *testing.T, router http.Handler) (*http.Cookie, string) {
	t.Helper()
	return loginAs(t, router, "admin", "change-me-now")
}

func loginAs(t *testing.T, router http.Handler, username, password string) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}
	var response struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(rec.Result().Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.CSRFToken == "" {
		t.Fatal("login did not return a CSRF token")
	}
	return cookies[0], response.CSRFToken
}

func serverCacheTestPath(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join("..", "..", ".cache", "tests", name+"-"+time.Now().UTC().Format("20060102150405.000000000"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "dashboard.db.json")
}
