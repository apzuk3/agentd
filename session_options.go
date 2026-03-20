package agentd

type SessionOption func(*Session)

func WithProviderKeys(keys ProviderKeys) SessionOption {
	return func(s *Session) {
		s.providerKeys = keys
	}
}

func WithGeminiAPIKey(key string) SessionOption {
	return func(s *Session) {
		s.providerKeys.GeminiAPIKey = key
	}
}

func WithAnthropicAPIKey(key string) SessionOption {
	return func(s *Session) {
		s.providerKeys.AnthropicAPIKey = key
	}
}

func WithOpenAIAPIKey(key string) SessionOption {
	return func(s *Session) {
		s.providerKeys.OpenAIAPIKey = key
	}
}

func WithTavilyAPIKey(key string) SessionOption {
	return func(s *Session) {
		s.providerKeys.TavilyAPIKey = key
	}
}

// WithPlugin registers a single SessionPlugin with the session's plugin chain.
func WithPlugin(p SessionPlugin) SessionOption {
	return func(s *Session) {
		s.plugins.Register(p)
	}
}

// WithPlugins registers multiple SessionPlugins with the session's plugin chain.
func WithPlugins(plugins ...SessionPlugin) SessionOption {
	return func(s *Session) {
		for _, p := range plugins {
			s.plugins.Register(p)
		}
	}
}
