package main

import (
	"log"
	"net/http"
	"os"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/apzuk3/agentd"
	"github.com/apzuk3/agentd/gen/proto/go/agentd/v1/agentdv1connect"
)

func main() {
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	anthropicAPIKey := os.Getenv("ANTHROPIC_API_KEY")
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")

	if geminiAPIKey == "" && anthropicAPIKey == "" && openaiAPIKey == "" {
		log.Fatal("at least one of GEMINI_API_KEY, ANTHROPIC_API_KEY, or OPENAI_API_KEY must be set")
	}

	svc := &agentd.Service{
		GeminiAPIKey:    geminiAPIKey,
		AnthropicAPIKey: anthropicAPIKey,
		OpenAIAPIKey:    openaiAPIKey,
	}

	mux := http.NewServeMux()

	path, handler := agentdv1connect.NewAgentdHandler(svc)
	mux.Handle(path, handler)

	addr := ":8080"
	log.Printf("Starting ConnectRPC server on %s", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
