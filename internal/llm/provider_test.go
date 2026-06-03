package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
)

func TestOpenAIClientUsesResponsesAPIWithStructuredOutput(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	cfg := config.Defaults()
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIModel = "gpt-test"
	client := NewOpenAIClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://api.openai.com/v1/responses" {
			t.Fatalf("unexpected OpenAI request %s %s", req.Method, req.URL.String())
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-openai-key" {
			t.Fatalf("Authorization header = %q", got)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "gpt-test" {
			t.Fatalf("model = %#v", body["model"])
		}
		if _, ok := body["store"]; ok {
			t.Fatalf("OpenAI API-key request should not set store by default: %#v", body["store"])
		}
		if tools, ok := body["tools"].([]interface{}); !ok || len(tools) != 0 {
			t.Fatalf("OpenAI tools should default to empty, got %#v", body["tools"])
		}
		text, ok := body["text"].(map[string]interface{})
		if !ok {
			t.Fatalf("missing text structured output config: %#v", body)
		}
		format, ok := text["format"].(map[string]interface{})
		if !ok || format["type"] != "json_schema" || format["strict"] != true {
			t.Fatalf("unexpected text format = %#v", text["format"])
		}
		return jsonResponse(t, http.StatusOK, map[string]string{"output_text": validDiagnosisJSON(t)})
	})}

	diagnosis, err := client.Diagnose(context.Background(), sampleLLMRequest())
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.RecommendedActionID != "none" || diagnosis.Severity != models.SeverityNone {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
}

func TestOpenAIClientRunsAllowedReadOnlyAgentTool(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	cfg := config.Defaults()
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIModel = "gpt-test"
	client := NewOpenAIClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	requests := 0
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch requests {
		case 1:
			tools := body["tools"].([]interface{})
			if len(tools) == 0 {
				t.Fatalf("agent-enabled request did not advertise tools: %#v", body["tools"])
			}
			return jsonResponse(t, http.StatusOK, map[string]interface{}{
				"output": []map[string]interface{}{
					{
						"type":      "function_call",
						"id":        "fc_1",
						"call_id":   "call_1",
						"name":      "noobboard_network_status",
						"arguments": `{}`,
					},
				},
			})
		case 2:
			input := fmt.Sprint(body["input"])
			if !strings.Contains(input, "fresh live unifi") {
				t.Fatalf("second request did not include fresh tool output: %s", input)
			}
			return jsonResponse(t, http.StatusOK, map[string]string{"output_text": validDiagnosisJSON(t)})
		default:
			t.Fatalf("unexpected extra request %d", requests)
		}
		return nil, nil
	})}

	req := sampleLLMRequest()
	req.Policy.AgentToolsEnabled = true
	req.Policy.AgentMaxToolCalls = 2
	req.Policy.AgentToolRules = []models.LLMAgentToolRule{{Tool: "noobboard_network_status", Action: "allow"}}
	req.LiveSnapshot = func(context.Context) (models.Snapshot, error) {
		snapshot := req.Snapshot
		snapshot.IntegrationMode = "live"
		snapshot.Infrastructure.SourceHealth.UniFi = "fresh live unifi"
		snapshot.Infrastructure.UniFiWANUp = true
		snapshot.Infrastructure.UniFiGatewayReachable = true
		return snapshot, nil
	}
	diagnosis, err := client.Diagnose(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.RecommendedActionID != "none" {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestGeneralUserCannotAdvertiseAgentToolsEvenIfPolicyIsMisconfigured(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	cfg := config.Defaults()
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIModel = "gpt-test"
	client := NewOpenAIClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if tools, ok := body["tools"].([]interface{}); !ok || len(tools) != 0 {
			t.Fatalf("general-user request advertised tools: %#v", body["tools"])
		}
		return jsonResponse(t, http.StatusOK, map[string]string{"output_text": validDiagnosisJSON(t)})
	})}
	req := sampleLLMRequest()
	req.Mode = ModeGeneralUserRequested
	req.Policy.RecipientRole = models.RoleGeneralUser
	req.Policy.IncludeLogs = false
	req.Policy.MaxContextBytes = 12000
	req.Policy.AgentToolsEnabled = true
	req.Policy.AgentMaxToolCalls = 2
	req.Policy.AgentToolRules = []models.LLMAgentToolRule{{Tool: "*", Action: "allow"}}
	req.LiveSnapshot = func(context.Context) (models.Snapshot, error) {
		return req.Snapshot, nil
	}
	if _, err := client.Diagnose(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestAnthropicClientUsesMessagesAPIWithDiagnosisTool(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	cfg := config.Defaults()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.AnthropicModel = "claude-test"
	client := NewAnthropicClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://api.anthropic.com/v1/messages" {
			t.Fatalf("unexpected Anthropic request %s %s", req.Method, req.URL.String())
		}
		if got := req.Header.Get("x-api-key"); got != "test-anthropic-key" {
			t.Fatalf("x-api-key header = %q", got)
		}
		if got := req.Header.Get("anthropic-version"); got == "" {
			t.Fatal("missing anthropic-version header")
		}
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "claude-test" {
			t.Fatalf("model = %#v", body["model"])
		}
		toolChoice, ok := body["tool_choice"].(map[string]interface{})
		if !ok || toolChoice["name"] != "record_diagnosis" {
			t.Fatalf("unexpected tool_choice = %#v", body["tool_choice"])
		}
		return jsonResponse(t, http.StatusOK, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "tool_use", "name": "record_diagnosis", "input": validDiagnosisMap()},
			},
		})
	})}

	diagnosis, err := client.Diagnose(context.Background(), sampleLLMRequest())
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.RecommendedActionID != "none" || diagnosis.Severity != models.SeverityNone {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
}

func TestProviderUsesConfigKeys(t *testing.T) {
	cfg := config.Defaults()
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAPIKey = "config-openai-key"
	cfg.LLM.OpenAIModel = "gpt-test"
	if !ProviderAvailable(cfg.LLM) {
		t.Fatal("OpenAI provider should be available with a config API key")
	}
	openaiClient := NewOpenAIClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	openaiClient.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer config-openai-key" {
			t.Fatalf("Authorization header = %q", got)
		}
		return jsonResponse(t, http.StatusOK, map[string]string{"output_text": validDiagnosisJSON(t)})
	})}
	if _, err := openaiClient.Diagnose(context.Background(), sampleLLMRequest()); err != nil {
		t.Fatal(err)
	}

	cfg = config.Defaults()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.AnthropicAPIKey = "config-anthropic-key"
	cfg.LLM.AnthropicModel = "claude-test"
	if !ProviderAvailable(cfg.LLM) {
		t.Fatal("Anthropic provider should be available with a config API key")
	}
	anthropicClient := NewAnthropicClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	anthropicClient.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("x-api-key"); got != "config-anthropic-key" {
			t.Fatalf("x-api-key header = %q", got)
		}
		return jsonResponse(t, http.StatusOK, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "tool_use", "name": "record_diagnosis", "input": validDiagnosisMap()},
			},
		})
	})}
	if _, err := anthropicClient.Diagnose(context.Background(), sampleLLMRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestChatGPTConnectorUsesCodexResponsesEndpoint(t *testing.T) {
	cfg := config.Defaults()
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodChatGPTBrowser
	cfg.LLM.ChatGPTRefreshToken = "refresh-token"
	cfg.LLM.ChatGPTAccessToken = "access-token"
	cfg.LLM.ChatGPTAccountID = "account-123"
	cfg.LLM.ChatGPTTokenExpiresAt = time.Now().UTC().Add(time.Hour)
	cfg.LLM.OpenAIModel = "gpt-5"
	if !ProviderAvailable(cfg.LLM) {
		t.Fatal("ChatGPT connector should be available with refresh token and account id")
	}
	client, ok := NewClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{})).(*ChatGPTClient)
	if !ok {
		t.Fatalf("NewClient returned %T, want *ChatGPTClient", NewClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{})))
	}
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != OpenAIChatGPTCodexEndpoint {
			t.Fatalf("unexpected ChatGPT request %s %s", req.Method, req.URL.String())
		}
		if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization header = %q", got)
		}
		if got := req.Header.Get("ChatGPT-Account-Id"); got != "account-123" {
			t.Fatalf("ChatGPT-Account-Id header = %q", got)
		}
		if got := req.Header.Get("Session-Id"); !strings.HasPrefix(got, "noobboard-") {
			t.Fatalf("Session-Id header = %q", got)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != DefaultChatGPTCodexModel {
			t.Fatalf("model = %#v", body["model"])
		}
		reasoning, ok := body["reasoning"].(map[string]interface{})
		if !ok || reasoning["effort"] != ChatGPTCodexReasoningHigh {
			t.Fatalf("reasoning = %#v", body["reasoning"])
		}
		if store, ok := body["store"].(bool); !ok || store {
			t.Fatalf("ChatGPT Codex store = %#v, want false", body["store"])
		}
		include, ok := body["include"].([]interface{})
		if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("ChatGPT Codex include = %#v", body["include"])
		}
		input, ok := body["input"].([]interface{})
		if !ok || len(input) != 1 {
			t.Fatalf("ChatGPT Codex input = %#v, want one-item list", body["input"])
		}
		message, ok := input[0].(map[string]interface{})
		if !ok || message["role"] != "user" {
			t.Fatalf("ChatGPT Codex input message = %#v", input[0])
		}
		if content, _ := message["content"].(string); !strings.Contains(content, "Sanitized diagnostic context") {
			t.Fatalf("ChatGPT Codex input content did not include prompt: %s", content)
		}
		return jsonResponse(t, http.StatusOK, map[string]string{"output_text": validDiagnosisJSON(t)})
	})}
	if _, err := client.Diagnose(context.Background(), sampleLLMRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestChatGPTConnectorRunsAllowedReadOnlyAgentToolStateless(t *testing.T) {
	cfg := config.Defaults()
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodChatGPTHeadless
	cfg.LLM.ChatGPTRefreshToken = "refresh-token"
	cfg.LLM.ChatGPTAccessToken = "access-token"
	cfg.LLM.ChatGPTAccountID = "account-123"
	cfg.LLM.ChatGPTTokenExpiresAt = time.Now().UTC().Add(time.Hour)
	client := NewChatGPTClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	requests := 0
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if store, ok := body["store"].(bool); !ok || store {
			t.Fatalf("ChatGPT Codex request %d store = %#v, want false", requests, body["store"])
		}
		include, ok := body["include"].([]interface{})
		if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("ChatGPT Codex request %d include = %#v", requests, body["include"])
		}
		switch requests {
		case 1:
			tools := body["tools"].([]interface{})
			if len(tools) == 0 {
				t.Fatalf("agent-enabled ChatGPT request did not advertise tools: %#v", body["tools"])
			}
			return jsonResponse(t, http.StatusOK, map[string]interface{}{
				"output": []map[string]interface{}{
					{
						"type":      "function_call",
						"id":        "fc_1",
						"call_id":   "call_1",
						"name":      "noobboard_network_status",
						"arguments": `{}`,
					},
				},
			})
		case 2:
			input, ok := body["input"].([]interface{})
			if !ok {
				t.Fatalf("second request input = %#v", body["input"])
			}
			for _, item := range input {
				object, ok := item.(map[string]interface{})
				if ok && object["id"] == "fc_1" {
					t.Fatalf("second request kept stored response id: %#v", object)
				}
			}
			inputText := fmt.Sprint(input)
			if !strings.Contains(inputText, "fresh live unifi") {
				t.Fatalf("second request did not include fresh tool output: %s", inputText)
			}
			return jsonResponse(t, http.StatusOK, map[string]string{"output_text": validDiagnosisJSON(t)})
		default:
			t.Fatalf("unexpected extra request %d", requests)
		}
		return nil, nil
	})}

	req := sampleLLMRequest()
	req.Policy.AgentToolsEnabled = true
	req.Policy.AgentMaxToolCalls = 2
	req.Policy.AgentToolRules = []models.LLMAgentToolRule{{Tool: "noobboard_network_status", Action: "allow"}}
	req.LiveSnapshot = func(context.Context) (models.Snapshot, error) {
		snapshot := req.Snapshot
		snapshot.IntegrationMode = "live"
		snapshot.Infrastructure.SourceHealth.UniFi = "fresh live unifi"
		snapshot.Infrastructure.UniFiWANUp = true
		snapshot.Infrastructure.UniFiGatewayReachable = true
		return snapshot, nil
	}
	diagnosis, err := client.Diagnose(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.RecommendedActionID != "none" {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestChatGPTModelNormalizesUnsupportedCodexModels(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "empty", model: "", want: DefaultChatGPTCodexModel},
		{name: "legacy gpt 5", model: "gpt-5", want: DefaultChatGPTCodexModel},
		{name: "allowed default", model: "gpt-5.5", want: "gpt-5.5"},
		{name: "allowed mini", model: "gpt-5.4-mini", want: "gpt-5.4-mini"},
		{name: "future codex model", model: "gpt-5.6", want: "gpt-5.6"},
		{name: "unknown", model: "gpt-chatgpt-test", want: DefaultChatGPTCodexModel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chatGPTModel(tt.model); got != tt.want {
				t.Fatalf("chatGPTModel(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestExtractChatGPTAccountIDFromTokenClaims(t *testing.T) {
	token := testJWT(t, map[string]interface{}{
		"https://api.openai.com/auth": map[string]interface{}{
			"chatgpt_account_id": "account-from-claim",
		},
	})
	accountID := ExtractChatGPTAccountID(ChatGPTTokenResponse{IDToken: token})
	if accountID != "account-from-claim" {
		t.Fatalf("account id = %q", accountID)
	}
}

func TestDefaultClientDoesNotReturnMockDiagnosis(t *testing.T) {
	cfg := config.Defaults()
	client := NewClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	if ProviderAvailable(cfg.LLM) {
		t.Fatal("default LLM provider should not be available")
	}
	if _, err := client.Diagnose(context.Background(), sampleLLMRequest()); err == nil {
		t.Fatal("expected disabled default provider to fail instead of returning a diagnosis")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(t *testing.T, status int, value interface{}) (*http.Response, error) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}, nil
}

func sampleLLMRequest() Request {
	return Request{
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
		Snapshot: models.Snapshot{
			GeneratedAt:   time.Now().UTC(),
			OverallStatus: models.StatusOnline,
			ServerSummary: "All systems online.",
			Infrastructure: models.InfrastructureStatus{
				InternetReachable:      true,
				DNSOK:                  true,
				RouterReachable:        true,
				NASReachable:           true,
				UnraidAPIReachable:     true,
				UnraidArrayState:       "started",
				UnraidArrayHealthy:     true,
				DockerServiceAvailable: true,
			},
		},
		Question: "What is wrong right now?",
		ActorID:  "admin-1",
	}
}

func validDiagnosisJSON(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(validDiagnosisMap())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func validDiagnosisMap() map[string]interface{} {
	return map[string]interface{}{
		"severity":              "none",
		"confidence":            0.9,
		"incident_type":         "unknown",
		"affected_services":     []string{},
		"diagnosis":             "No active incident is visible in the current telemetry.",
		"evidence":              []string{"overall status online"},
		"general_user_summary":  "Everything looks online.",
		"admin_message":         "No action is needed.",
		"recommended_action_id": "none",
		"should_notify_admin":   false,
	}
}

func testJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "none"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
