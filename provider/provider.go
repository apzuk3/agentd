package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"

	"github.com/apzuk3/agentd/model/anthropic"
	"github.com/apzuk3/agentd/model/openai"
)

// provider identifies a supported LLM vendor, inferred from the model name.
type provider string

const (
	providerOpenAI    provider = "openai"
	providerAnthropic provider = "anthropic"
	providerGemini    provider = "gemini"
)

// New initializes an ADK-compatible model.LLM, inferring the provider from modelName.
// If apiKey is empty, it falls back to standard environment variables.
func New(modelName, apiKey string) (model.LLM, error) {
	ctx := context.Background()

	p, err := inferProvider(modelName)
	if err != nil {
		return nil, err
	}

	if apiKey == "" {
		apiKey = resolveEnvKey(p)
	}

	switch p {
	case providerOpenAI:
		if apiKey == "" {
			return nil, fmt.Errorf("api key must be provided to use openai provider")
		}
		return openai.NewModel(ctx, modelName, &openai.Config{
			APIKey: apiKey,
		})
	case providerAnthropic:
		if apiKey == "" {
			return nil, fmt.Errorf("api key must be provided to use anthropic provider")
		}
		return anthropic.NewModel(ctx, modelName, &anthropic.Config{
			Provider: anthropic.ProviderAnthropic,
			APIKey:   apiKey,
		})
	case providerGemini:
		cfg := &genai.ClientConfig{APIKey: apiKey}
		return gemini.NewModel(ctx, modelName, cfg)
	default:
		return nil, fmt.Errorf("unsupported provider %q", p)
	}
}

// inferProvider determines the LLM vendor from a model name's prefix.
func inferProvider(modelName string) (provider, error) {
	name := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case name == "":
		return "", fmt.Errorf("model name must be provided")
	case strings.HasPrefix(name, "gpt"), strings.HasPrefix(name, "o1"), strings.HasPrefix(name, "o3"), strings.HasPrefix(name, "o4"):
		return providerOpenAI, nil
	case strings.HasPrefix(name, "claude"):
		return providerAnthropic, nil
	case strings.HasPrefix(name, "gemini"):
		return providerGemini, nil
	default:
		return "", fmt.Errorf("could not infer provider from model name %q", modelName)
	}
}

func resolveEnvKey(p provider) string {
	var envs []string
	switch p {
	case providerOpenAI:
		envs = []string{"OPENAI_API_KEY", "OPENAI_KEY", "OPENAI_SECRET_KEY", "OPENAI_SECRET", "OPENAI_APIKEY"}
	case providerAnthropic:
		envs = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_KEY", "ANTHROPIC_SECRET_KEY", "ANTHROPIC_SECRET", "ANTHROPIC_APIKEY"}
	case providerGemini:
		envs = []string{"GOOGLE_API_KEY", "GEMINI_API_KEY", "GEMINI_KEY", "GEMINI_SECRET_KEY", "GEMINI_SECRET", "GEMINI_APIKEY"}
	}
	for _, env := range envs {
		if val := os.Getenv(env); val != "" {
			return val
		}
	}
	return ""
}
