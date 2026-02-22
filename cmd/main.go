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
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY environment variable is required")
	}

	svc := &agentd.Service{APIKey: apiKey}

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
