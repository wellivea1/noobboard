package llm

import (
	"context"
	"errors"
	"os"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/privacy"
)

type DisabledClient struct {
	reason string
}

func NewClient(cfg config.LLMConfig, redactor *privacy.Redactor) Client {
	switch cfg.Provider {
	case "openai":
		return NewOpenAIClient(cfg, redactor)
	case "anthropic":
		return NewAnthropicClient(cfg, redactor)
	default:
		return DisabledClient{reason: `llm provider is disabled; set NOOBBOARD_LLM_PROVIDER to "openai" or "anthropic" and configure the matching API key`}
	}
}

func ProviderAvailable(cfg config.LLMConfig) bool {
	if !cfg.Enabled {
		return false
	}
	switch cfg.Provider {
	case "openai":
		return os.Getenv("OPENAI_API_KEY") != ""
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY") != ""
	default:
		return false
	}
}

func (c DisabledClient) Diagnose(context.Context, Request) (Diagnosis, error) {
	if c.reason == "" {
		return Diagnosis{}, errors.New("llm provider is disabled")
	}
	return Diagnosis{}, errors.New(c.reason)
}

var _ Client = DisabledClient{}
