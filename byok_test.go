package agentd

import (
	"net/http"
	"testing"
)

func TestResolveProviderKeys_DefaultsOnly(t *testing.T) {
	defaults := ProviderKeys{
		GeminiAPIKey:    "env-gemini",
		AnthropicAPIKey: "env-anthropic",
		OpenAIAPIKey:    "env-openai",
		TavilyAPIKey:    "env-tavily",
	}

	got := resolveProviderKeys(defaults, http.Header{})

	if got != defaults {
		t.Errorf("expected defaults to pass through unchanged, got %+v", got)
	}
}

func TestResolveProviderKeys_HeaderOverridesDefault(t *testing.T) {
	defaults := ProviderKeys{
		GeminiAPIKey:    "env-gemini",
		AnthropicAPIKey: "env-anthropic",
		OpenAIAPIKey:    "env-openai",
		TavilyAPIKey:    "env-tavily",
	}

	headers := http.Header{}
	headers.Set(HeaderGeminiAPIKey, "client-gemini")
	headers.Set(HeaderOpenAIAPIKey, "client-openai")

	got := resolveProviderKeys(defaults, headers)

	if got.GeminiAPIKey != "client-gemini" {
		t.Errorf("GeminiAPIKey = %q, want %q", got.GeminiAPIKey, "client-gemini")
	}
	if got.AnthropicAPIKey != "env-anthropic" {
		t.Errorf("AnthropicAPIKey should remain default, got %q", got.AnthropicAPIKey)
	}
	if got.OpenAIAPIKey != "client-openai" {
		t.Errorf("OpenAIAPIKey = %q, want %q", got.OpenAIAPIKey, "client-openai")
	}
	if got.TavilyAPIKey != "env-tavily" {
		t.Errorf("TavilyAPIKey should remain default, got %q", got.TavilyAPIKey)
	}
}

func TestResolveProviderKeys_EmptyHeaderIgnored(t *testing.T) {
	defaults := ProviderKeys{
		GeminiAPIKey: "env-gemini",
	}

	headers := http.Header{}
	headers.Set(HeaderGeminiAPIKey, "")

	got := resolveProviderKeys(defaults, headers)

	if got.GeminiAPIKey != "env-gemini" {
		t.Errorf("empty header should not override, got %q", got.GeminiAPIKey)
	}
}

func TestResolveProviderKeys_AllFromHeaders(t *testing.T) {
	defaults := ProviderKeys{}

	headers := http.Header{}
	headers.Set(HeaderGeminiAPIKey, "h-gemini")
	headers.Set(HeaderAnthropicAPIKey, "h-anthropic")
	headers.Set(HeaderOpenAIAPIKey, "h-openai")
	headers.Set(HeaderTavilyAPIKey, "h-tavily")

	got := resolveProviderKeys(defaults, headers)

	want := ProviderKeys{
		GeminiAPIKey:    "h-gemini",
		AnthropicAPIKey: "h-anthropic",
		OpenAIAPIKey:    "h-openai",
		TavilyAPIKey:    "h-tavily",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolveProviderKeys_NilHeaders(t *testing.T) {
	defaults := ProviderKeys{
		GeminiAPIKey: "env-gemini",
	}

	got := resolveProviderKeys(defaults, nil)

	if got.GeminiAPIKey != "env-gemini" {
		t.Errorf("nil headers should use defaults, got %q", got.GeminiAPIKey)
	}
}

func TestCoalesce(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"first non-empty wins", []string{"a", "b"}, "a"},
		{"skip empty", []string{"", "b"}, "b"},
		{"all empty", []string{"", ""}, ""},
		{"single value", []string{"x"}, "x"},
		{"no values", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coalesce(tt.values...)
			if got != tt.want {
				t.Errorf("coalesce(%v) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}
