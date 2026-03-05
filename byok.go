package agentd

import "net/http"

// BYOK (Bring Your Own Key) header names.
// Clients set these on the Run stream request to override server-configured
// defaults for a single session. Empty values are ignored; the server default
// is used instead.
const (
	HeaderGeminiAPIKey    = "X-Agentd-Gemini-Api-Key"
	HeaderAnthropicAPIKey = "X-Agentd-Anthropic-Api-Key"
	HeaderOpenAIAPIKey    = "X-Agentd-Openai-Api-Key"
	HeaderTavilyAPIKey    = "X-Agentd-Tavily-Api-Key"
)

// ProviderKeys holds API keys for all supported LLM providers and auxiliary
// services. It is used both as the server-level default and the per-session
// effective key set.
type ProviderKeys struct {
	GeminiAPIKey    string
	AnthropicAPIKey string
	OpenAIAPIKey    string
	TavilyAPIKey    string
}

// resolveProviderKeys merges server-configured default keys with per-request
// keys extracted from HTTP headers. Header values take precedence when
// non-empty. Keys are held in memory only for the lifetime of the session.
func resolveProviderKeys(defaults ProviderKeys, headers http.Header) ProviderKeys {
	return ProviderKeys{
		GeminiAPIKey:    coalesce(headers.Get(HeaderGeminiAPIKey), defaults.GeminiAPIKey),
		AnthropicAPIKey: coalesce(headers.Get(HeaderAnthropicAPIKey), defaults.AnthropicAPIKey),
		OpenAIAPIKey:    coalesce(headers.Get(HeaderOpenAIAPIKey), defaults.OpenAIAPIKey),
		TavilyAPIKey:    coalesce(headers.Get(HeaderTavilyAPIKey), defaults.TavilyAPIKey),
	}
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
