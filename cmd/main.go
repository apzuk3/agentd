package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/apzuk3/agentd"
	"github.com/apzuk3/agentd/gen/proto/go/agentd/v1/agentdv1connect"
)

func main() {
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})))

	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	anthropicAPIKey := os.Getenv("ANTHROPIC_API_KEY")
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")
	tavilyAPIKey := os.Getenv("TAVILY_API_KEY")

	if geminiAPIKey == "" && anthropicAPIKey == "" && openaiAPIKey == "" {
		slog.Warn("no provider API keys configured via environment; clients must supply keys via BYOK headers")
	}

	emitter := agentd.NewSessionEventEmitter()
	emitter.Subscribe(agentd.NewSessionLogListener(slog.Default()))

	// TODO: replace with a real AuditStore implementation.
	emitter.Subscribe(agentd.NewSessionAudit(agentd.NoopAuditStore{}))

	svc := &agentd.Service{
		GeminiAPIKey:    geminiAPIKey,
		AnthropicAPIKey: anthropicAPIKey,
		OpenAIAPIKey:    openaiAPIKey,
		TavilyAPIKey:    tavilyAPIKey,
		EventEmitter:    emitter,
	}

	mux := http.NewServeMux()

	path, handler := agentdv1connect.NewAgentdHandler(svc)
	mux.Handle(path, handler)

	addr := ":8080"
	slog.Info("starting server", "addr", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	if err := server.ListenAndServe(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
