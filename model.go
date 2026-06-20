package agentd

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/apzuk3/agentd/model/anthropic"
	"github.com/apzuk3/agentd/model/openai"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"
)

var defaultEnvVars = map[string][]string{
	"anthropic": {"ANTHROPIC_API_KEY", "ANTHROPIC_KEY", "ANTHROPIC_SECRET_KEY", "ANTHROPIC_SECRET", "ANTHROPIC_APIKEY"},
	"openai":    {"OPENAI_API_KEY", "OPENAI_KEY", "OPENAI_SECRET_KEY", "OPENAI_SECRET", "OPENAI_APIKEY"},
	"gemini":    {"GOOGLE_API_KEY", "GEMINI_API_KEY", "GEMINI_KEY", "GEMINI_SECRET_KEY", "GEMINI_SECRET", "GEMINI_APIKEY"},
}

type modelPlaceholder struct {
	httpClient *http.Client
	apiKey     string
	apiKeyEnvs []string
}

type ModelOption func(*modelPlaceholder) error

func WithModelHTTPClient(httpClient *http.Client) ModelOption {
	return func(m *modelPlaceholder) error {
		m.httpClient = httpClient
		return nil
	}
}

func WithModelAPIKey(apiKey string) ModelOption {
	return func(m *modelPlaceholder) error {
		m.apiKey = apiKey
		return nil
	}
}

func WithModelAPIKeyEnv(apiKeyEnv ...string) ModelOption {
	return func(m *modelPlaceholder) error {
		m.apiKeyEnvs = apiKeyEnv
		return nil
	}
}

func NewModel(ctx context.Context, m Model, options ...ModelOption) (model.LLM, error) {
	mp := &modelPlaceholder{}
	for _, option := range options {
		if err := option(mp); err != nil {
			return nil, err
		}
	}

	apiKeyEnvs := append(mp.apiKeyEnvs, defaultEnvVars[string(m.Provider())]...)

	key := mp.apiKey
	if key == "" {
		for _, env := range apiKeyEnvs {
			key = os.Getenv(env)
			if key != "" {
				break
			}
		}
	}

	switch m {
	case ModelClaudeOpus48, ModelClaudeOpus47, ModelClaudeSonnet46, ModelClaudeSonnet45, ModelClaudeHaiku45:
		if key == "" {
			return nil, fmt.Errorf("api key not found")
		}
		return anthropic.NewModel(ctx, string(m), &anthropic.Config{
			APIKey: key,
		})
	case ModelOpenAIGPT55, ModelOpenAIGPT55Pro, ModelOpenAIGPT55Mini, ModelOpenAIGPT55Nano, ModelOpenAIGPT54, ModelOpenAIGPT54Mini, ModelOpenAIGPT54Nano:
		if key == "" {
			return nil, fmt.Errorf("api key not found")
		}

		return openai.NewModel(ctx, string(m), &openai.Config{
			APIKey: key,
		})
	case ModelGeminiFlash35, ModelGeminiPro31, ModelGeminiFlashLite31, ModelGeminiFlash3, ModelGeminiPro25:
		return gemini.NewModel(ctx, string(m), &genai.ClientConfig{
			APIKey: key,
		})
	}

	return nil, fmt.Errorf("model %s not found", m)
}
