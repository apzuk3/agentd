package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/apzuk3/agentd/client"
	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

type DomainNameCheckerInput struct {
	DomainName string `json:"domain_name"`
}

type DomainNameCheckerOutput struct {
	Available bool `json:"available"`
}

var whoisClient = &http.Client{Timeout: 10 * time.Second}

func checkDomainAvailability(domain string) (bool, error) {
	token := os.Getenv("WHOIS_API_TOKEN")
	if token == "" {
		return false, fmt.Errorf("WHOIS_API_TOKEN environment variable is not set")
	}

	req, err := http.NewRequest(http.MethodGet, "https://whoisjson.com/api/v1/domain-availability?domain="+domain, nil)
	if err != nil {
		return false, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "TOKEN="+token)

	resp, err := whoisClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("requesting domain availability: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Available bool `json:"available"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decoding response: %w", err)
	}

	return result.Available, nil
}

func main() {
	clnt := client.New("http://localhost:8080")

	err := client.AddTool(clnt, "domainnamechecker", "Checks if a domain name is available", func(ctx context.Context, input DomainNameCheckerInput) (any, error) {
		slog.Info("Checking domain name", "domain_name", input.DomainName)

		available, err := checkDomainAvailability(input.DomainName)
		if err != nil {
			return nil, fmt.Errorf("checking domain availability: %w", err)
		}

		return DomainNameCheckerOutput{
			Available: available,
		}, nil
	})

	if err != nil {
		log.Fatalf("failed to add tool: %v", err)
	}

	agent := &agentdv1.Agent{
		Name:        "domainnamechecker",
		Description: "Checks if a domain name is available",
		AgentType: &agentdv1.Agent_Llm{
			Llm: &agentdv1.LlmAgent{
				Model:     "gemini-2.5-flash",
				ToolNames: []string{"domainnamechecker"},
			},
		},
	}

	iter := clnt.Run(context.Background(), agent, "is agentd.run available?")
	for event, err := range iter {
		if err != nil {
			log.Fatalf("failed to run agent: %v", err)
		}

		if event != nil && event.Error != nil {
			fmt.Println("Error:", event.Error.Message)
			break
		}

		if event != nil && event.OutputChunk != nil {
			fmt.Print(event.OutputChunk.Content)
		}

		if event != nil && event.End != nil {
			fmt.Println()
			break
		}
	}
}
