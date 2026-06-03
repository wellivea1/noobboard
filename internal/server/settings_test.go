package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	reloaded := newTestApp(t, cfg)
	if reloaded.deps.Config.Integrations.UnraidAPIKey != "unraid-local-test" {
		t.Fatal("saved Unraid API key was not applied after reload")
	}
	if reloaded.deps.Config.Integrations.UniFiAPIKey != "unifi-local-test" {
		t.Fatal("saved UniFi API key was not applied after reload")
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
		{method: http.MethodGet, path: "/api/admin/settings/integrations"},
		{method: http.MethodPost, path: "/api/admin/settings/llm/openai/browser/start", body: `{}`},
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

type recordingDockerCollector struct {
	apps       []models.AppStatus
	logs       []models.LogLine
	called     docker.ContainerAction
	app        models.AppStatus
	logApp     models.AppStatus
	logOptions docker.LogOptions
}

func (c *recordingDockerCollector) Apps(context.Context) ([]models.AppStatus, error) {
	return c.apps, nil
}

func (c *recordingDockerCollector) ControlContainer(_ context.Context, app models.AppStatus, action docker.ContainerAction) (docker.ControlResult, error) {
	c.called = action
	c.app = app
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

func newTestApp(t *testing.T, cfg config.Config) *App {
	t.Helper()
	store, err := db.OpenFileStore(cfg.Database.Path)
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
	dir := filepath.Join("..", "..", ".cache", "tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name+"-"+time.Now().UTC().Format("20060102150405.000000000")+".json")
}
