package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
		if _, ok := body["stream"]; ok {
			t.Fatalf("OpenAI API-key request should not set stream by default: %#v", body["stream"])
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

func TestOpenAIResponsesUsageLimitErrorIsClassified(t *testing.T) {
	err := openAIProviderError("openai responses api", http.StatusTooManyRequests, []byte(`{
		"error": {
			"code": "insufficient_quota",
			"message": "You exceeded your current quota, please check your plan and billing details."
		}
	}`))
	if !IsOpenAIUsageLimitError(err) {
		t.Fatalf("expected usage limit classification for %v", err)
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if providerErr.Code != OpenAIUsageLimitCode {
		t.Fatalf("ProviderError code = %q, want %q", providerErr.Code, OpenAIUsageLimitCode)
	}
}

func TestOpenAIResponsesStreamUsageLimitErrorIsClassified(t *testing.T) {
	_, err := normalizeOpenAIResponsesStream([]byte(strings.Join([]string{
		"event: response.failed",
		`data: {"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"Usage limit reached. Try again later."}}}`,
		"",
	}, "\n")), "chatgpt codex responses api")
	if err == nil {
		t.Fatal("expected streamed usage limit error")
	}
	if !IsOpenAIUsageLimitError(err) {
		t.Fatalf("expected usage limit classification for %v", err)
	}
	if !strings.Contains(err.Error(), "stream error") {
		t.Fatalf("stream error was not labeled clearly: %v", err)
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
	cfg.LLM.OpenAIModel = "unsupported-codex-model"
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
		if stream, ok := body["stream"].(bool); !ok || !stream {
			t.Fatalf("ChatGPT Codex stream = %#v, want true", body["stream"])
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
		return streamResponse(t,
			map[string]interface{}{
				"type":  "response.output_text.delta",
				"delta": validDiagnosisJSON(t),
			},
		)
	})}
	if _, err := client.Diagnose(context.Background(), sampleLLMRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestChatGPTConnectorReviewActionUsesCodexListInput(t *testing.T) {
	cfg := config.Defaults()
	cfg.LLM.Provider = "openai"
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodChatGPTBrowser
	cfg.LLM.ChatGPTRefreshToken = "refresh-token"
	cfg.LLM.ChatGPTAccessToken = "access-token"
	cfg.LLM.ChatGPTAccountID = "account-123"
	cfg.LLM.ChatGPTTokenExpiresAt = time.Now().UTC().Add(time.Hour)
	client := NewChatGPTClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != OpenAIChatGPTCodexEndpoint {
			t.Fatalf("unexpected ChatGPT review request %s %s", req.Method, req.URL.String())
		}
		if got := req.Header.Get("ChatGPT-Account-Id"); got != "account-123" {
			t.Fatalf("ChatGPT-Account-Id header = %q", got)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		input, ok := body["input"].([]interface{})
		if !ok || len(input) != 1 {
			t.Fatalf("ChatGPT action review input = %#v, want one-item list", body["input"])
		}
		message, ok := input[0].(map[string]interface{})
		if !ok || message["role"] != "user" {
			t.Fatalf("ChatGPT action review input message = %#v", input[0])
		}
		if content, _ := message["content"].(string); !strings.Contains(content, "ACTION AUTO-REVIEW") || !strings.Contains(content, "target_label: Emby") {
			t.Fatalf("ChatGPT action review content missing prompt details: %s", content)
		}
		if store, ok := body["store"].(bool); !ok || store {
			t.Fatalf("ChatGPT action review store = %#v, want false", body["store"])
		}
		if stream, ok := body["stream"].(bool); !ok || !stream {
			t.Fatalf("ChatGPT action review stream = %#v, want true", body["stream"])
		}
		include, ok := body["include"].([]interface{})
		if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("ChatGPT action review include = %#v", body["include"])
		}
		return streamResponse(t,
			map[string]interface{}{
				"type":  "response.output_text.delta",
				"delta": validActionReviewJSON(t),
			},
		)
	})}
	decision, err := client.ReviewAction(context.Background(), sampleActionReviewRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allow {
		t.Fatalf("decision = %#v, want allow", decision)
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
		if stream, ok := body["stream"].(bool); !ok || !stream {
			t.Fatalf("ChatGPT Codex request %d stream = %#v, want true", requests, body["stream"])
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
			call := map[string]interface{}{
				"type":      "function_call",
				"id":        "fc_1",
				"call_id":   "call_1",
				"name":      "noobboard_network_status",
				"arguments": `{}`,
			}
			return streamResponse(t, map[string]interface{}{
				"type": "response.output_item.done",
				"item": call,
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
			return streamResponse(t,
				map[string]interface{}{
					"type": "response.completed",
					"response": map[string]interface{}{
						"output_text": validDiagnosisJSON(t),
					},
				},
			)
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

func TestChatGPTModelNormalizesUnsupportedChatGPTModels(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "empty", model: "", want: DefaultChatGPTCodexModel},
		{name: "unsupported saved model", model: "unsupported-codex-model", want: DefaultChatGPTCodexModel},
		{name: "allowed default", model: "gpt-5.5", want: "gpt-5.5"},
		{name: "allowed current model", model: "gpt-5.4", want: "gpt-5.4"},
		{name: "unsupported chatgpt account model", model: "gpt-5.3-codex", want: DefaultChatGPTCodexModel},
		{name: "unsupported older codex model", model: "gpt-5.1-codex", want: DefaultChatGPTCodexModel},
		{name: "future non-allowlisted model", model: "codex-preview-future", want: DefaultChatGPTCodexModel},
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

func TestChatGPTConnectorFallsBackWhenChatGPTAccountRejectsModel(t *testing.T) {
	cfg := config.Defaults()
	cfg.LLM.OpenAIAuthMethod = config.OpenAIAuthMethodChatGPTBrowser
	cfg.LLM.ChatGPTRefreshToken = "refresh-token"
	cfg.LLM.ChatGPTAccessToken = "access-token"
	cfg.LLM.ChatGPTAccountID = "account-123"
	cfg.LLM.ChatGPTTokenExpiresAt = time.Now().UTC().Add(time.Hour)
	cfg.LLM.OpenAIModel = "gpt-5.5"
	wantFallback := chatGPTCodexModelFallbacks[0]
	if wantFallback == "gpt-5.5" {
		wantFallback = chatGPTCodexModelFallbacks[1]
	}
	client := NewChatGPTClient(cfg.LLM, privacy.NewRedactor(config.PrivacyConfig{}))
	var models []string
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		model, _ := body["model"].(string)
		models = append(models, model)
		if model == "gpt-5.5" {
			return jsonResponse(t, http.StatusBadRequest, map[string]interface{}{
				"message": "The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account.",
			})
		}
		// Asserted against the head of the chain rather than a literal, so a
		// model refresh updates one list instead of also editing this test.
		if model != wantFallback {
			t.Fatalf("fallback model = %q, want %q", model, wantFallback)
		}
		return streamResponse(t,
			map[string]interface{}{
				"type":  "response.output_text.delta",
				"delta": validDiagnosisJSON(t),
			},
		)
	})}
	if _, err := client.Diagnose(context.Background(), sampleLLMRequest()); err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "gpt-5.5" || models[1] != wantFallback {
		t.Fatalf("models = %#v, want gpt-5.5 then %q", models, wantFallback)
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

func streamResponse(t *testing.T, events ...map[string]interface{}) (*http.Response, error) {
	t.Helper()
	var body strings.Builder
	for _, event := range events {
		eventType, _ := event["type"].(string)
		if eventType != "" {
			body.WriteString("event: ")
			body.WriteString(eventType)
			body.WriteString("\n")
		}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		body.WriteString("data: ")
		body.Write(data)
		body.WriteString("\n\n")
	}
	body.WriteString("data: [DONE]\n\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body.String())),
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

func sampleActionReviewRequest() ActionReviewRequest {
	return ActionReviewRequest{
		ActionID:      "ask_admin_to_restart_container",
		ActionTitle:   "App fix recommendation",
		TargetID:      "emby",
		TargetLabel:   "Emby",
		CurrentStatus: models.StatusOffline,
		ActorRole:     models.RoleAdmin,
		Via:           "admin_approval",
		Reasoning:     "high",
		Snapshot: models.Snapshot{
			GeneratedAt: time.Now().UTC(),
			Apps: []models.AppStatus{
				{
					AppID:              "emby",
					DisplayName:        "Emby",
					ContainerName:      "EmbyServer",
					CurrentStatus:      models.StatusOffline,
					DockerState:        models.DockerExited,
					AgentRepairAllowed: true,
				},
			},
			Infrastructure: models.InfrastructureStatus{
				InternetReachable:      true,
				DNSOK:                  true,
				RouterReachable:        true,
				NASReachable:           true,
				UnraidAPIReachable:     true,
				UnraidArrayHealthy:     true,
				DockerServiceAvailable: true,
			},
		},
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

func validActionReviewJSON(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{
		"allow":      true,
		"confidence": 0.92,
		"summary":    "The proposed app fix is target-specific and consistent with the current app state.",
		"issues":     []string{},
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	})
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
		"recommended_action_target": map[string]interface{}{
			"kind":       "none",
			"id_or_name": "",
		},
		"should_notify_admin": false,
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

func TestAnthropicRefusalAndTruncationReportClearly(t *testing.T) {
	// Both come back as HTTP 200 with no usable tool call, so the transport-level
	// status check cannot catch them. Without an explicit stop_reason check they
	// fall through to scanning the body for JSON and surface as a parse error,
	// which reads like a NoobBoard bug rather than an answer the provider
	// declined or cut short.
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "refusal with category",
			body: `{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber"},"content":[]}`,
			want: "declined this request (cyber)",
		},
		{
			name: "refusal without category",
			body: `{"stop_reason":"refusal","content":[]}`,
			want: "declined this request;",
		},
		{
			name: "truncated before the tool call completed",
			body: `{"stop_reason":"max_tokens","content":[{"type":"text","text":"partial"}]}`,
			want: "hit the token limit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := diagnosisFromAnthropic([]byte(tt.body)); err == nil {
				t.Fatal("diagnosisFromAnthropic returned no error")
			} else if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("diagnosis error = %q, want it to contain %q", err.Error(), tt.want)
			}
			if _, err := actionReviewFromAnthropic([]byte(tt.body)); err == nil {
				t.Fatal("actionReviewFromAnthropic returned no error")
			} else if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("action review error = %q, want it to contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestAnthropicToolCallStillWinsOverStopReason(t *testing.T) {
	// A complete tool call must be honoured even when stop_reason is set, so the
	// new check cannot reject a good answer.
	body := `{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"record_diagnosis","input":` + validDiagnosisJSON(t) + `}]}`
	if _, err := diagnosisFromAnthropic([]byte(body)); err != nil {
		t.Fatalf("diagnosisFromAnthropic returned %v, want the tool call to be accepted", err)
	}
}

// --- agent log and history tools ---------------------------------------------

func agentToolRequest(t *testing.T, apps []models.AppStatus) Request {
	t.Helper()
	req := sampleLLMRequest()
	req.Policy.AgentToolsEnabled = true
	req.Policy.AgentMaxToolCalls = 4
	req.Policy.AgentToolRules = []models.LLMAgentToolRule{{Tool: "*", Action: "allow"}}
	req.LiveSnapshot = func(context.Context) (models.Snapshot, error) {
		return models.Snapshot{GeneratedAt: time.Now().UTC(), Apps: apps}, nil
	}
	return req
}

func visibleTestApp() models.AppStatus {
	return models.AppStatus{
		AppID:                 "emby",
		DisplayName:           "Emby",
		ContainerName:         "emby",
		CurrentStatus:         models.StatusOffline,
		DockerState:           models.DockerExited,
		VisibleToGeneralUsers: true,
		LLMVisibleAdmin:       true,
		LLMVisibleGeneral:     true,
	}
}

func TestAgentLogToolIsNotOfferedWithoutAFetcher(t *testing.T) {
	// A nil fetcher must mean "tool absent", not "tool present and failing", so
	// the model never plans around a capability the server did not wire.
	req := agentToolRequest(t, []models.AppStatus{visibleTestApp()})
	tools := agentToolsForRequest(req, privacy.NewRedactor(config.PrivacyConfig{}))
	if _, ok := tools[agentToolAppLogs]; ok {
		t.Fatal("log tool was offered with no AppLogs fetcher")
	}
	if _, ok := tools[agentToolAppHistory]; ok {
		t.Fatal("history tool was offered with no AppHistory fetcher")
	}
}

func TestAgentLogToolCapsLinesRegardlessOfRequest(t *testing.T) {
	// The model's limit is a hint; the cap is not. Logs are the most likely
	// place for a credential to surface, so the budget is enforced server-side.
	var asked int
	req := agentToolRequest(t, []models.AppStatus{visibleTestApp()})
	req.AppLogs = func(_ context.Context, _ string, limit int) ([]models.LogLine, error) {
		asked = limit
		return []models.LogLine{{Line: "boom"}}, nil
	}
	tools := agentToolsForRequest(req, privacy.NewRedactor(config.PrivacyConfig{}))
	tool, ok := tools[agentToolAppLogs]
	if !ok {
		t.Fatal("log tool was not offered")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"app_id_or_name": "Emby", "limit": float64(100000)}); err != nil {
		t.Fatal(err)
	}
	if asked != agentLogLineCap {
		t.Fatalf("requested limit = %d, want the cap %d", asked, agentLogLineCap)
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"app_id_or_name": "Emby", "limit": float64(-5)}); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Fatalf("requested limit = %d, want 1 for a negative ask", asked)
	}
}

func TestAgentLogToolRefusesAppsTheRoleCannotSee(t *testing.T) {
	// Resolution goes through the role-filtered snapshot, so an app absent from
	// it is unreachable even if the caller names it exactly. The fetcher must
	// never be reached.
	called := false
	req := agentToolRequest(t, []models.AppStatus{visibleTestApp()})
	req.AppLogs = func(context.Context, string, int) ([]models.LogLine, error) {
		called = true
		return nil, nil
	}
	tools := agentToolsForRequest(req, privacy.NewRedactor(config.PrivacyConfig{}))
	result, err := tools[agentToolAppLogs].Execute(context.Background(), map[string]interface{}{"app_id_or_name": "secret-app", "limit": float64(10)})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("log fetcher ran for an app outside the filtered snapshot")
	}
	payload, _ := result.(map[string]interface{})
	if found, _ := payload["found"].(bool); found {
		t.Fatalf("result = %#v, want found=false", payload)
	}
}

func TestAgentHistoryToolTrimsToTheMostRecentEvents(t *testing.T) {
	events := make([]models.StatusEvent, 0, agentHistoryCap+10)
	for i := 0; i < agentHistoryCap+10; i++ {
		events = append(events, models.StatusEvent{ID: fmt.Sprintf("e%d", i), SubjectID: "emby"})
	}
	req := agentToolRequest(t, []models.AppStatus{visibleTestApp()})
	req.AppHistory = func(context.Context, string) (models.StatusHistory, error) {
		return models.StatusHistory{DisplayName: "Emby", Current: models.StatusOffline, Events: events}, nil
	}
	tools := agentToolsForRequest(req, privacy.NewRedactor(config.PrivacyConfig{}))
	result, err := tools[agentToolAppHistory].Execute(context.Background(), map[string]interface{}{"app_id_or_name": "emby"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := result.(map[string]interface{})
	got, _ := payload["events"].([]models.StatusEvent)
	if len(got) != agentHistoryCap {
		t.Fatalf("event count = %d, want the cap %d", len(got), agentHistoryCap)
	}
	// Trimming keeps the newest, which is the end of the slice.
	if got[len(got)-1].ID != events[len(events)-1].ID {
		t.Fatalf("last event = %s, want the most recent %s", got[len(got)-1].ID, events[len(events)-1].ID)
	}
}

func TestAgentToolsStayAdminOnly(t *testing.T) {
	req := agentToolRequest(t, []models.AppStatus{visibleTestApp()})
	req.AppLogs = func(context.Context, string, int) ([]models.LogLine, error) { return nil, nil }
	req.AppHistory = func(context.Context, string) (models.StatusHistory, error) {
		return models.StatusHistory{}, nil
	}
	req.Policy.RecipientRole = models.RoleGeneralUser
	if tools := agentToolsForRequest(req, privacy.NewRedactor(config.PrivacyConfig{})); len(tools) != 0 {
		t.Fatalf("general-user request was offered %d tools, want none", len(tools))
	}
}
