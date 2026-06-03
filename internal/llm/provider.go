package llm

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/privacy"
)

type DisabledClient struct {
	reason string
}

func NewClient(cfg config.LLMConfig, redactor *privacy.Redactor) Client {
	switch cfg.Provider {
	case "openai":
		if openAIAuthMethod(cfg) != config.OpenAIAuthMethodAPIKey {
			return NewChatGPTClient(cfg, redactor)
		}
		return NewOpenAIClient(cfg, redactor)
	case "anthropic":
		return NewAnthropicClient(cfg, redactor)
	default:
		return DisabledClient{reason: `llm provider is disabled; set NOOBBOARD_LLM_PROVIDER to "openai" or "anthropic" and configure the matching API key or ChatGPT connector`}
	}
}

func ProviderAvailable(cfg config.LLMConfig) bool {
	if !cfg.Enabled {
		return false
	}
	switch cfg.Provider {
	case "openai":
		if openAIAuthMethod(cfg) != config.OpenAIAuthMethodAPIKey {
			return chatGPTAuthAvailable(cfg)
		}
		return firstConfigured(cfg.OpenAIAPIKey, os.Getenv("OPENAI_API_KEY")) != ""
	case "anthropic":
		return firstConfigured(cfg.AnthropicAPIKey, os.Getenv("ANTHROPIC_API_KEY")) != ""
	default:
		return false
	}
}

func openAIAuthMethod(cfg config.LLMConfig) string {
	method := strings.TrimSpace(cfg.OpenAIAuthMethod)
	if method == "" {
		return config.OpenAIAuthMethodAPIKey
	}
	return method
}

func chatGPTAuthAvailable(cfg config.LLMConfig) bool {
	return firstConfigured(cfg.ChatGPTRefreshToken, os.Getenv("NOOBBOARD_CHATGPT_REFRESH_TOKEN"), os.Getenv("CHATGPT_REFRESH_TOKEN")) != "" &&
		firstConfigured(cfg.ChatGPTAccountID, os.Getenv("NOOBBOARD_CHATGPT_ACCOUNT_ID"), os.Getenv("CHATGPT_ACCOUNT_ID")) != ""
}

func firstConfigured(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (c DisabledClient) Diagnose(context.Context, Request) (Diagnosis, error) {
	if c.reason == "" {
		return Diagnosis{}, errors.New("llm provider is disabled")
	}
	return Diagnosis{}, errors.New(c.reason)
}

var _ Client = DisabledClient{}
