package agentd

import "log/slog"

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

func WithLogger(logger *slog.Logger) SessionOption {
	return func(s *Session) {
		s.log = logger
	}
}
