package agentd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"

	"github.com/apzuk3/agentd/model/anthropic"
	"github.com/apzuk3/agentd/model/openai"
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
	baseURL    string
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

func WithModelBaseURL(baseURL string) ModelOption {
	return func(m *modelPlaceholder) error {
		m.baseURL = baseURL
		return nil
	}
}

func providerForModel(name string) string {
	switch {
	case strings.HasPrefix(name, "claude-"):
		return "anthropic"
	case strings.HasPrefix(name, "gpt-"):
		return "openai"
	case strings.HasPrefix(name, "gemini-"):
		return "gemini"
	}
	return ""
}

func NewModel(ctx context.Context, modelName string, options ...ModelOption) (model.LLM, error) {
	mp := &modelPlaceholder{}
	for _, option := range options {
		if err := option(mp); err != nil {
			return nil, err
		}
	}

	prov := providerForModel(modelName)
	apiKeyEnvs := append(mp.apiKeyEnvs, defaultEnvVars[prov]...)

	key := mp.apiKey
	if key == "" {
		for _, env := range apiKeyEnvs {
			key = os.Getenv(env)
			if key != "" {
				break
			}
		}
	}

	switch modelName {
	case ModelClaudeOpus48, ModelClaudeOpus47, ModelClaudeSonnet46, ModelClaudeSonnet45, ModelClaudeHaiku45:
		if key == "" {
			return nil, fmt.Errorf("api key not found")
		}
		return anthropic.NewModel(ctx, modelName, &anthropic.Config{
			APIKey:  key,
			BaseURL: mp.baseURL,
		})
	case ModelOpenAIGPT55, ModelOpenAIGPT55Pro, ModelOpenAIGPT55Mini, ModelOpenAIGPT55Nano, ModelOpenAIGPT54, ModelOpenAIGPT54Mini, ModelOpenAIGPT54Nano:
		if key == "" {
			return nil, fmt.Errorf("api key not found")
		}

		return openai.NewModel(ctx, modelName, &openai.Config{
			APIKey:  key,
			BaseURL: mp.baseURL,
		})
	case ModelGeminiFlash35, ModelGeminiPro31, ModelGeminiFlashLite31, ModelGeminiFlash3, ModelGeminiPro25:
		cfg := &genai.ClientConfig{APIKey: key}
		if mp.baseURL != "" {
			cfg.HTTPOptions = genai.HTTPOptions{BaseURL: mp.baseURL}
		}
		return gemini.NewModel(ctx, modelName, cfg)
	}

	return nil, fmt.Errorf("model %s not found", modelName)
}
