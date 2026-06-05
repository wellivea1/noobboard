package llm

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/privacy"
)

const (
	OpenAIChatGPTClientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	OpenAIChatGPTIssuer         = "https://auth.openai.com"
	OpenAIChatGPTCodexEndpoint  = "https://chatgpt.com/backend-api/codex/responses"
	DefaultChatGPTCodexModel    = "gpt-5.5"
	ChatGPTCodexReasoningHigh   = "high"
	defaultChatGPTAccessSeconds = 3600
)

var chatGPTCodexAllowedModels = map[string]bool{
	"gpt-5.5":      true,
	"gpt-5.5-pro":  true,
	"gpt-5.4":      true,
	"gpt-5.4-pro":  true,
	"gpt-5.4-mini": true,
	"gpt-5.4-nano": true,
	"gpt-5.2":      true,
	"gpt-5.2-pro":  true,
	"gpt-5.1":      true,
	"gpt-5":        true,
	"gpt-5-mini":   true,
	"gpt-5-nano":   true,
	"gpt-4.1":      true,
	"gpt-4.1-mini": true,
	"gpt-4o-mini":  true,
}

var chatGPTCodexModelFallbacks = []string{
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.2",
	"gpt-5.1",
	"gpt-5",
	"gpt-4.1",
	"gpt-4o-mini",
}

type ChatGPTTokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type ChatGPTClient struct {
	mu           sync.Mutex
	refreshToken string
	accessToken  string
	expiresAt    time.Time
	accountID    string
	model        string
	issuer       string
	endpoint     string
	http         *http.Client
	builder      ContextBuilder
}

func NewChatGPTClient(cfg config.LLMConfig, redactor *privacy.Redactor) *ChatGPTClient {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	return &ChatGPTClient{
		refreshToken: firstConfigured(cfg.ChatGPTRefreshToken, os.Getenv("NOOBBOARD_CHATGPT_REFRESH_TOKEN"), os.Getenv("CHATGPT_REFRESH_TOKEN")),
		accessToken:  firstConfigured(cfg.ChatGPTAccessToken, os.Getenv("NOOBBOARD_CHATGPT_ACCESS_TOKEN"), os.Getenv("CHATGPT_ACCESS_TOKEN")),
		expiresAt:    cfg.ChatGPTTokenExpiresAt,
		accountID:    firstConfigured(cfg.ChatGPTAccountID, os.Getenv("NOOBBOARD_CHATGPT_ACCOUNT_ID"), os.Getenv("CHATGPT_ACCOUNT_ID")),
		model:        chatGPTModel(cfg.OpenAIModel),
		issuer:       OpenAIChatGPTIssuer,
		endpoint:     OpenAIChatGPTCodexEndpoint,
		http:         &http.Client{Timeout: timeout},
		builder:      NewContextBuilder(redactor),
	}
}

func (c *ChatGPTClient) Diagnose(ctx context.Context, req Request) (Diagnosis, error) {
	if strings.TrimSpace(c.refreshToken) == "" {
		return Diagnosis{}, errors.New("ChatGPT connector is not connected; use LLM settings to connect OpenAI")
	}
	if strings.TrimSpace(c.accountID) == "" {
		return Diagnosis{}, errors.New("ChatGPT connector is missing an account id; reconnect OpenAI in LLM settings")
	}
	accessToken, err := c.ensureAccessToken(ctx)
	if err != nil {
		return Diagnosis{}, err
	}
	contextText, err := c.builder.Build(req)
	if err != nil {
		return Diagnosis{}, err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+accessToken)
	headers.Set("ChatGPT-Account-Id", c.accountID)
	headers.Set("Originator", "noobboard")
	headers.Set("Session-Id", chatGPTSessionID())
	headers.Set("User-Agent", "NoobBoard")
	opts := openAIResponsesOptions{
		ReasoningEffort:           ChatGPTCodexReasoningHigh,
		IncludeEncryptedReasoning: true,
		InputAsList:               true,
		StoreFalse:                true,
		Stream:                    true,
	}
	var lastUnsupported error
	for _, model := range chatGPTModelCandidates(c.model) {
		diagnosis, err := runOpenAIResponsesDiagnosis(ctx, c.http, c.endpoint, headers, model, contextText, req, c.builder.redactor, "chatgpt codex responses api", opts)
		if err == nil {
			return diagnosis, nil
		}
		if !isChatGPTUnsupportedModelError(err) {
			return Diagnosis{}, err
		}
		lastUnsupported = err
	}
	return Diagnosis{}, lastUnsupported
}

func (c *ChatGPTClient) ReviewAction(ctx context.Context, req ActionReviewRequest) (ActionReviewDecision, error) {
	if strings.TrimSpace(c.refreshToken) == "" {
		return ActionReviewDecision{}, errors.New("ChatGPT connector is not connected; use LLM settings to connect OpenAI")
	}
	if strings.TrimSpace(c.accountID) == "" {
		return ActionReviewDecision{}, errors.New("ChatGPT connector is missing an account id; reconnect OpenAI in LLM settings")
	}
	accessToken, err := c.ensureAccessToken(ctx)
	if err != nil {
		return ActionReviewDecision{}, err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+accessToken)
	headers.Set("ChatGPT-Account-Id", c.accountID)
	headers.Set("Originator", "noobboard")
	headers.Set("Session-Id", chatGPTSessionID())
	headers.Set("User-Agent", "NoobBoard")
	reasoning := strings.TrimSpace(req.Reasoning)
	if reasoning == "" {
		reasoning = ChatGPTCodexReasoningHigh
	}
	prompt := BuildActionReviewPrompt(req)
	opts := openAIResponsesOptions{
		ReasoningEffort:           openAIReviewReasoning(reasoning),
		IncludeEncryptedReasoning: true,
		InputAsList:               true,
		StoreFalse:                true,
		Stream:                    true,
	}
	var lastUnsupported error
	for _, model := range chatGPTModelCandidates(c.model) {
		data, err := openAIActionReviewBody(model, prompt, opts)
		if err != nil {
			return ActionReviewDecision{}, err
		}
		respData, err := postOpenAIResponses(ctx, c.http, c.endpoint, headers, data, "chatgpt action review api", openAIResponsesOptions{Stream: true})
		if err == nil {
			return actionReviewFromResponsesBody(respData)
		}
		if !isChatGPTUnsupportedModelError(err) {
			return ActionReviewDecision{}, err
		}
		lastUnsupported = err
	}
	return ActionReviewDecision{}, lastUnsupported
}

func (c *ChatGPTClient) ensureAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if token := strings.TrimSpace(c.accessToken); token != "" && (c.expiresAt.IsZero() || time.Now().UTC().Add(time.Minute).Before(c.expiresAt)) {
		return token, nil
	}
	if strings.TrimSpace(c.refreshToken) == "" {
		return "", errors.New("ChatGPT refresh token is not set")
	}
	tokens, err := refreshChatGPTAccessToken(ctx, c.http, c.issuer, c.refreshToken)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return "", errors.New("OpenAI token refresh did not return an access token")
	}
	c.accessToken = strings.TrimSpace(tokens.AccessToken)
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		c.refreshToken = strings.TrimSpace(tokens.RefreshToken)
	}
	if accountID := ExtractChatGPTAccountID(tokens); accountID != "" {
		c.accountID = accountID
	}
	c.expiresAt = ChatGPTTokenExpiresAt(tokens, time.Now().UTC())
	return c.accessToken, nil
}

func refreshChatGPTAccessToken(ctx context.Context, client *http.Client, issuer, refreshToken string) (ChatGPTTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {OpenAIChatGPTClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(issuer, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return ChatGPTTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return ChatGPTTokenResponse{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ChatGPTTokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ChatGPTTokenResponse{}, fmt.Errorf("OpenAI token refresh failed with %d: %s", resp.StatusCode, string(data))
	}
	var tokens ChatGPTTokenResponse
	if err := json.Unmarshal(data, &tokens); err != nil {
		return ChatGPTTokenResponse{}, err
	}
	return tokens, nil
}

func ExtractChatGPTAccountID(tokens ChatGPTTokenResponse) string {
	for _, token := range []string{tokens.IDToken, tokens.AccessToken} {
		claims := parseJWTClaims(token)
		if len(claims) == 0 {
			continue
		}
		if accountID := stringClaim(claims, "chatgpt_account_id"); accountID != "" {
			return accountID
		}
		if nested, ok := claims["https://api.openai.com/auth"].(map[string]interface{}); ok {
			if accountID := stringClaim(nested, "chatgpt_account_id"); accountID != "" {
				return accountID
			}
		}
		if orgs, ok := claims["organizations"].([]interface{}); ok && len(orgs) > 0 {
			if org, ok := orgs[0].(map[string]interface{}); ok {
				if accountID := stringClaim(org, "id"); accountID != "" {
					return accountID
				}
			}
		}
	}
	return ""
}

func ChatGPTTokenExpiresAt(tokens ChatGPTTokenResponse, now time.Time) time.Time {
	expiresIn := tokens.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = defaultChatGPTAccessSeconds
	}
	return now.Add(time.Duration(expiresIn) * time.Second).UTC()
}

func chatGPTSessionID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("noobboard-%d", time.Now().UTC().UnixNano())
	}
	return "noobboard-" + hex.EncodeToString(data[:])
}

func parseJWTClaims(token string) map[string]interface{} {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func stringClaim(claims map[string]interface{}, key string) string {
	value, _ := claims[key].(string)
	return strings.TrimSpace(value)
}

func chatGPTModel(model string) string {
	model = strings.TrimSpace(model)
	if chatGPTCodexModelAllowed(model) {
		return model
	}
	return DefaultChatGPTCodexModel
}

func chatGPTCodexModelAllowed(model string) bool {
	return chatGPTCodexAllowedModels[model]
}

func chatGPTModelCandidates(model string) []string {
	primary := chatGPTModel(model)
	candidates := []string{primary}
	for _, fallback := range chatGPTCodexModelFallbacks {
		if fallback != primary {
			candidates = append(candidates, fallback)
		}
	}
	return candidates
}

func isChatGPTUnsupportedModelError(err error) bool {
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusBadRequest {
		return false
	}
	text := strings.ToLower(providerErr.Message + " " + providerErr.Body)
	return strings.Contains(text, "not supported") && strings.Contains(text, "chatgpt account")
}

var _ Client = (*ChatGPTClient)(nil)
