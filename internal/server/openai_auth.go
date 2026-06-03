package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/llm"
)

const (
	openAIChatGPTCallbackAddress = "localhost:1455"
	openAIChatGPTCallbackPath    = "/auth/callback"
	openAIChatGPTAuthTTL         = 5 * time.Minute
	openAIChatGPTHeadlessTTL     = 10 * time.Minute
)

type openAIAuthStore struct {
	mu             sync.Mutex
	browser        map[string]openAIBrowserPending
	browserResults map[string]openAIBrowserResult
	headless       map[string]openAIHeadlessPending
	callbackServer *http.Server
}

type openAIBrowserPending struct {
	Verifier    string
	RedirectURI string
	ExpiresAt   time.Time
}

type openAIBrowserResult struct {
	Tokens    llm.ChatGPTTokenResponse
	ExpiresAt time.Time
}

type openAIHeadlessPending struct {
	DeviceAuthID string
	UserCode     string
	ActorID      string
	ExpiresAt    time.Time
	Interval     time.Duration
}

type openAIPKCE struct {
	Verifier  string
	Challenge string
}

func newOpenAIAuthStore() *openAIAuthStore {
	return &openAIAuthStore{
		browser:        map[string]openAIBrowserPending{},
		browserResults: map[string]openAIBrowserResult{},
		headless:       map[string]openAIHeadlessPending{},
	}
}

func (a *App) startOpenAIChatGPTBrowserAuth(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if !openAIChatGPTBrowserAllowed(r) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":    "ChatGPT browser login only works when this admin page is opened as localhost on the NoobBoard host. Use the code login from LAN devices.",
			"fallback": "chatgpt_headless",
		})
		return
	}
	if err := a.ensureOpenAIChatGPTCallbackServer(); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	pkce, err := generateOpenAIPKCE()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	state, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	redirectURI := openAIChatGPTRedirectURI()
	expiresAt := time.Now().UTC().Add(openAIChatGPTAuthTTL)
	a.openAIAuth.mu.Lock()
	a.openAIAuth.cleanupExpiredLocked(time.Now().UTC())
	a.openAIAuth.browser[state] = openAIBrowserPending{
		Verifier:    pkce.Verifier,
		RedirectURI: redirectURI,
		ExpiresAt:   expiresAt,
	}
	a.openAIAuth.mu.Unlock()

	a.deps.Audit.Record(mustUser(r).ID, "settings.llm.chatgpt.browser.start", map[string]interface{}{"expires_at": expiresAt})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"auth_url":   buildOpenAIChatGPTAuthorizeURL(pkce, state, redirectURI),
		"poll_id":    state,
		"expires_at": expiresAt,
	})
}

func (a *App) finishOpenAIChatGPTBrowserAuth(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		PollID string `json:"poll_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pollID := strings.TrimSpace(body.PollID)
	now := time.Now().UTC()
	a.openAIAuth.mu.Lock()
	a.openAIAuth.cleanupExpiredLocked(now)
	result, hasResult := a.openAIAuth.browserResults[pollID]
	_, hasPending := a.openAIAuth.browser[pollID]
	if hasResult {
		delete(a.openAIAuth.browserResults, pollID)
	}
	a.openAIAuth.mu.Unlock()
	if hasResult {
		if err := a.saveOpenAIChatGPTTokens(mustUser(r).ID, config.OpenAIAuthMethodChatGPTBrowser, result.Tokens); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "connected", "settings": llmSettingsResponse(a.configSnapshot().LLM)})
		return
	}
	if hasPending {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "pending"})
		return
	}
	writeError(w, http.StatusNotFound, errors.New("OpenAI browser login has expired; start again"))
}

func (a *App) startOpenAIChatGPTHeadlessAuth(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	device, err := requestOpenAIHeadlessDeviceCode(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	pollID, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	interval := parseOpenAIHeadlessInterval(device.Interval)
	expiresAt := time.Now().UTC().Add(openAIChatGPTHeadlessTTL)
	a.openAIAuth.mu.Lock()
	a.openAIAuth.cleanupExpiredLocked(time.Now().UTC())
	a.openAIAuth.headless[pollID] = openAIHeadlessPending{
		DeviceAuthID: device.DeviceAuthID,
		UserCode:     device.UserCode,
		ActorID:      mustUser(r).ID,
		ExpiresAt:    expiresAt,
		Interval:     interval,
	}
	a.openAIAuth.mu.Unlock()

	a.deps.Audit.Record(mustUser(r).ID, "settings.llm.chatgpt.headless.start", map[string]interface{}{"expires_at": expiresAt})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"poll_id":          pollID,
		"verification_url": strings.TrimRight(llm.OpenAIChatGPTIssuer, "/") + "/codex/device",
		"user_code":        device.UserCode,
		"interval_seconds": int(interval / time.Second),
		"expires_at":       expiresAt,
	})
}

func (a *App) pollOpenAIChatGPTHeadlessAuth(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		PollID string `json:"poll_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pollID := strings.TrimSpace(body.PollID)
	a.openAIAuth.mu.Lock()
	pending, ok := a.openAIAuth.headless[pollID]
	if !ok || time.Now().UTC().After(pending.ExpiresAt) {
		delete(a.openAIAuth.headless, pollID)
		a.openAIAuth.mu.Unlock()
		writeError(w, http.StatusNotFound, errors.New("OpenAI headless login has expired; start again"))
		return
	}
	a.openAIAuth.mu.Unlock()

	code, verifier, pendingStatus, err := pollOpenAIHeadlessDeviceToken(r.Context(), pending)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if pendingStatus {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "pending", "interval_seconds": int(pending.Interval / time.Second)})
		return
	}
	tokens, err := exchangeOpenAIChatGPTCode(r.Context(), code, strings.TrimRight(llm.OpenAIChatGPTIssuer, "/")+"/deviceauth/callback", verifier)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if err := a.saveOpenAIChatGPTTokens(pending.ActorID, config.OpenAIAuthMethodChatGPTHeadless, tokens); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.openAIAuth.mu.Lock()
	delete(a.openAIAuth.headless, pollID)
	a.openAIAuth.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "connected", "settings": llmSettingsResponse(a.configSnapshot().LLM)})
}

func (a *App) ensureOpenAIChatGPTCallbackServer() error {
	a.openAIAuth.mu.Lock()
	defer a.openAIAuth.mu.Unlock()
	a.openAIAuth.cleanupExpiredLocked(time.Now().UTC())
	if a.openAIAuth.callbackServer != nil {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc(openAIChatGPTCallbackPath, a.openAIChatGPTBrowserCallback)
	server := &http.Server{
		Addr:              openAIChatGPTCallbackAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", openAIChatGPTCallbackAddress)
	if err != nil {
		return fmt.Errorf("OpenAI browser callback port %s is unavailable: %w", openAIChatGPTCallbackAddress, err)
	}
	a.openAIAuth.callbackServer = server
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.deps.Audit.Record("system", "settings.llm.chatgpt.browser.callback_error", map[string]interface{}{"error": err.Error()})
		}
	}()
	return nil
}

func (a *App) openAIChatGPTBrowserCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	if errValue := query.Get("error"); errValue != "" {
		a.writeOpenAICallbackHTML(w, http.StatusBadRequest, "Authorization Failed", firstNonEmpty(query.Get("error_description"), errValue))
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	state := strings.TrimSpace(query.Get("state"))
	if code == "" || state == "" {
		a.writeOpenAICallbackHTML(w, http.StatusBadRequest, "Authorization Failed", "OpenAI did not return the expected authorization code.")
		return
	}
	now := time.Now().UTC()
	a.openAIAuth.mu.Lock()
	pending, ok := a.openAIAuth.browser[state]
	if ok {
		delete(a.openAIAuth.browser, state)
	}
	a.openAIAuth.cleanupExpiredLocked(now)
	a.openAIAuth.mu.Unlock()
	defer a.stopOpenAIChatGPTCallbackServerIfIdle()
	if !ok || now.After(pending.ExpiresAt) {
		a.writeOpenAICallbackHTML(w, http.StatusBadRequest, "Authorization Failed", "The login request expired or did not match the expected state.")
		return
	}
	tokens, err := exchangeOpenAIChatGPTCode(r.Context(), code, pending.RedirectURI, pending.Verifier)
	if err != nil {
		a.writeOpenAICallbackHTML(w, http.StatusBadGateway, "Authorization Failed", err.Error())
		return
	}
	a.openAIAuth.mu.Lock()
	a.openAIAuth.browserResults[state] = openAIBrowserResult{
		Tokens:    tokens,
		ExpiresAt: time.Now().UTC().Add(openAIChatGPTAuthTTL),
	}
	a.openAIAuth.mu.Unlock()
	a.writeOpenAICallbackHTML(w, http.StatusOK, "OpenAI Connected", "You can close this window and return to NoobBoard.")
}

func (a *App) stopOpenAIChatGPTCallbackServerIfIdle() {
	a.openAIAuth.mu.Lock()
	if len(a.openAIAuth.browser) != 0 || a.openAIAuth.callbackServer == nil {
		a.openAIAuth.mu.Unlock()
		return
	}
	server := a.openAIAuth.callbackServer
	a.openAIAuth.callbackServer = nil
	a.openAIAuth.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func (a *App) writeOpenAICallbackHTML(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>%s</title>
    <style>
      :root { color-scheme: dark; font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #101111; color: #f2f3f5; }
      body { min-height: 100vh; margin: 0; display: grid; place-items: center; }
      main { display: grid; gap: 10px; max-width: 520px; padding: 24px; }
      h1 { margin: 0; font-size: 1.4rem; }
      p { margin: 0; color: #afb5b9; line-height: 1.5; }
    </style>
  </head>
  <body>
    <main>
      <h1>%s</h1>
      <p>%s</p>
    </main>
    <script>setTimeout(function(){ window.close(); }, 1800);</script>
  </body>
</html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}

func (a *App) saveOpenAIChatGPTTokens(actorID, method string, tokens llm.ChatGPTTokenResponse) error {
	refreshToken := strings.TrimSpace(tokens.RefreshToken)
	accessToken := strings.TrimSpace(tokens.AccessToken)
	if refreshToken == "" || accessToken == "" {
		return errors.New("OpenAI did not return the expected access and refresh tokens")
	}
	accountID := llm.ExtractChatGPTAccountID(tokens)
	if accountID == "" {
		return errors.New("OpenAI did not return a ChatGPT account id")
	}
	settings := normalizeLLMSettings(a.configSnapshot().LLM)
	settings.Enabled = true
	settings.Provider = "openai"
	settings.OpenAIAuthMethod = method
	settings.ChatGPTRefreshToken = refreshToken
	settings.ChatGPTAccessToken = accessToken
	settings.ChatGPTTokenExpiresAt = llm.ChatGPTTokenExpiresAt(tokens, time.Now().UTC())
	settings.ChatGPTAccountID = accountID

	next := a.configSnapshot()
	next.LLM = settings
	if err := next.Validate(); err != nil {
		return err
	}
	a.settingsMu.Lock()
	a.deps.Config.LLM = settings
	a.deps.LLM = llm.NewClient(settings, a.deps.Redactor)
	runtimeSettings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(runtimeSettings); err != nil {
		return err
	}
	a.deps.Audit.Record(actorID, "settings.llm.chatgpt.connected", map[string]interface{}{"method": method, "account_id_set": true})
	return nil
}

func generateOpenAIPKCE() (openAIPKCE, error) {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	buf := make([]byte, 43)
	if _, err := rand.Read(buf); err != nil {
		return openAIPKCE{}, err
	}
	for i, b := range buf {
		buf[i] = chars[int(b)%len(chars)]
	}
	verifier := string(buf)
	sum := sha256.Sum256([]byte(verifier))
	return openAIPKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

func buildOpenAIChatGPTAuthorizeURL(pkce openAIPKCE, state, redirectURI string) string {
	params := url.Values{
		"response_type":              {"code"},
		"client_id":                  {llm.OpenAIChatGPTClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {"openid profile email offline_access"},
		"code_challenge":             {pkce.Challenge},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"state":                      {state},
		"originator":                 {"noobboard"},
	}
	return strings.TrimRight(llm.OpenAIChatGPTIssuer, "/") + "/oauth/authorize?" + params.Encode()
}

func openAIChatGPTRedirectURI() string {
	return "http://" + openAIChatGPTCallbackAddress + openAIChatGPTCallbackPath
}

func openAIChatGPTBrowserAllowed(r *http.Request) bool {
	host := strings.TrimSpace(r.Host)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func exchangeOpenAIChatGPTCode(ctx context.Context, code, redirectURI, verifier string) (llm.ChatGPTTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {llm.OpenAIChatGPTClientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(llm.OpenAIChatGPTIssuer, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return llm.ChatGPTTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return llm.ChatGPTTokenResponse{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return llm.ChatGPTTokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return llm.ChatGPTTokenResponse{}, fmt.Errorf("OpenAI token exchange failed with %d: %s", resp.StatusCode, string(data))
	}
	var tokens llm.ChatGPTTokenResponse
	if err := json.Unmarshal(data, &tokens); err != nil {
		return llm.ChatGPTTokenResponse{}, err
	}
	return tokens, nil
}

type openAIHeadlessDeviceResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     string `json:"interval"`
}

func requestOpenAIHeadlessDeviceCode(ctx context.Context) (openAIHeadlessDeviceResponse, error) {
	body := strings.NewReader(`{"client_id":"` + llm.OpenAIChatGPTClientID + `"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(llm.OpenAIChatGPTIssuer, "/")+"/api/accounts/deviceauth/usercode", body)
	if err != nil {
		return openAIHeadlessDeviceResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NoobBoard")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return openAIHeadlessDeviceResponse{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return openAIHeadlessDeviceResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return openAIHeadlessDeviceResponse{}, fmt.Errorf("OpenAI device authorization failed with %d: %s", resp.StatusCode, string(data))
	}
	var device openAIHeadlessDeviceResponse
	if err := json.Unmarshal(data, &device); err != nil {
		return openAIHeadlessDeviceResponse{}, err
	}
	if strings.TrimSpace(device.DeviceAuthID) == "" || strings.TrimSpace(device.UserCode) == "" {
		return openAIHeadlessDeviceResponse{}, errors.New("OpenAI device authorization response was incomplete")
	}
	return device, nil
}

func pollOpenAIHeadlessDeviceToken(ctx context.Context, pending openAIHeadlessPending) (code string, verifier string, stillPending bool, err error) {
	body, err := json.Marshal(map[string]string{
		"device_auth_id": pending.DeviceAuthID,
		"user_code":      pending.UserCode,
	})
	if err != nil {
		return "", "", false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(llm.OpenAIChatGPTIssuer, "/")+"/api/accounts/deviceauth/token", strings.NewReader(string(body)))
	if err != nil {
		return "", "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NoobBoard")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", false, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", false, err
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return "", "", true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", "", false, fmt.Errorf("OpenAI device token poll failed with %d: %s", resp.StatusCode, string(data))
	}
	var response struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", "", false, err
	}
	if strings.TrimSpace(response.AuthorizationCode) == "" || strings.TrimSpace(response.CodeVerifier) == "" {
		return "", "", false, errors.New("OpenAI device token response was incomplete")
	}
	return response.AuthorizationCode, response.CodeVerifier, false, nil
}

func parseOpenAIHeadlessInterval(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		seconds = 5
	}
	if seconds > 30 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func (s *openAIAuthStore) cleanupExpiredLocked(now time.Time) {
	for state, pending := range s.browser {
		if now.After(pending.ExpiresAt) {
			delete(s.browser, state)
		}
	}
	for state, result := range s.browserResults {
		if now.After(result.ExpiresAt) {
			delete(s.browserResults, state)
		}
	}
	for pollID, pending := range s.headless {
		if now.After(pending.ExpiresAt) {
			delete(s.headless, pollID)
		}
	}
}
