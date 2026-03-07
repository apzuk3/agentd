package agentd

type SessionOption func(*Session)

func WithGeminiAPIKey(key string) SessionOption {
	return func(s *Session) {
		s.geminiAPIKey = key
	}
}

func WithAnthropicAPIKey(key string) SessionOption {
	return func(s *Session) {
		s.anthropicAPIKey = key
	}
}

func WithOpenAIAPIKey(key string) SessionOption {
	return func(s *Session) {
		s.openaiAPIKey = key
	}
}

func WithTavilyAPIKey(key string) SessionOption {
	return func(s *Session) {
		s.tavilyAPIKey = key
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
