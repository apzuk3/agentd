package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/apzuk3/agentd/provider"
	"github.com/apzuk3/agentd/terminal"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// fastlyStatusURL is the Fastly Domain Research "status" endpoint. It is a
// package variable so tests can point it at an httptest server.
var fastlyStatusURL = "https://api.fastly.com/domain-management/v1/tools/status"

func main() {
	modelGemini35Flash, err := provider.New("gemini-3.5-flash", "")
	if err != nil {
		log.Fatal(err)
	}

	modelGemini35Pro, err := provider.New("gemini-3.1-pro-preview", "")
	if err != nil {
		log.Fatal(err)
	}

	domainTool, err := newDomainAvailabilityTool()
	if err != nil {
		log.Fatal(err)
	}

	cfg := llmagent.Config{
		Description: "A domain suggester agent that suggests domain names for a given company name",
		Instruction: "You are a domain suggester agent that suggests domain names given a user idea. " +
			"When the user asks whether a domain is available, or after proposing names, use the " +
			"check_domain_availability tool to verify registration availability before reporting back. " +
			"You MUST refuse to discuss anything other than domain names.",
		Tools: []tool.Tool{domainTool},
	}

	cfg.Model = modelGemini35Flash
	agent1, err := llmagent.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	cfg.Model = modelGemini35Pro
	agent2, err := llmagent.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	pipeline, err := newSuggesterPipeline(modelGemini35Flash, domainTool)
	if err != nil {
		log.Fatal(err)
	}

	ui := terminal.NewUI(agent1, agent2, pipeline)
	err = ui.StartChat(context.Background())
	if err != nil {
		log.Fatal(err)
	}
}

// newSuggesterPipeline builds a two-stage sequential agent: a "brainstormer"
// proposes names, then a "verifier" checks their availability. Because the
// sequential agent delegates to named sub-agents, each stage renders as its own
// sub-agent run block in the terminal UI.
func newSuggesterPipeline(model model.LLM, domainTool tool.Tool) (agent.Agent, error) {
	brainstormer, err := llmagent.New(llmagent.Config{
		Name:        "brainstormer",
		Model:       model,
		Description: "Brainstorms short, brandable domain names",
		Instruction: "Suggest 5 short, brandable .com domain name ideas for the user's idea. " +
			"Output only the domain names, one per line. Do not check availability.",
	})
	if err != nil {
		return nil, err
	}

	verifier, err := llmagent.New(llmagent.Config{
		Name:        "verifier",
		Model:       model,
		Description: "Verifies domain availability",
		Instruction: "For each .com domain proposed earlier in the conversation, call " +
			"check_domain_availability and report which ones are available to register.",
		Tools: []tool.Tool{domainTool},
	})
	if err != nil {
		return nil, err
	}

	return sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        "pipeline",
			Description: "Brainstorm domain names, then verify their availability",
			SubAgents:   []agent.Agent{brainstormer, verifier},
		},
	})
}

// checkDomainArgs is the input the model supplies when invoking the domain
// availability tool.
type checkDomainArgs struct {
	Domain string `json:"domain" jsonschema_description:"Fully-qualified domain name to check, e.g. example.com"`
}

// newDomainAvailabilityTool wraps checkDomainAvailable as an ADK function tool
// so the agent can verify domain registration availability during a chat. The
// Fastly API key is resolved from the FASTLY_API_TOKEN environment variable.
func newDomainAvailabilityTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "check_domain_availability",
		Description: "Check whether a domain name is available for registration using Fastly's Domain Research API. Returns whether the domain is available along with the raw registry status flags.",
	}, func(ctx tool.Context, args checkDomainArgs) (DomainStatus, error) {
		return checkDomainAvailable(ctx, "", args.Domain)
	})
}

// DomainStatus is the result of a Fastly domain availability check. It exposes
// the raw status flags alongside a derived Available boolean.
type DomainStatus struct {
	Domain    string
	Zone      string
	Status    string // space-delimited status flags from Fastly
	Tags      string
	Available bool // true when the registry reports the domain as inactive (free to register)
}

// checkDomainAvailable reports whether domain is available for registration via
// Fastly's Domain Research Status API. A Precise (registry-level) check is
// performed. If apiKey is empty it falls back to the FASTLY_API_TOKEN env var.
func checkDomainAvailable(ctx context.Context, apiKey, domain string) (DomainStatus, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return DomainStatus{}, fmt.Errorf("domain must be provided")
	}

	if apiKey == "" {
		apiKey = os.Getenv("FASTLY_API_TOKEN")
	}
	if apiKey == "" {
		return DomainStatus{}, fmt.Errorf("fastly api key must be provided via argument or FASTLY_API_TOKEN")
	}

	endpoint, err := url.Parse(fastlyStatusURL)
	if err != nil {
		return DomainStatus{}, fmt.Errorf("invalid fastly status url: %w", err)
	}
	q := endpoint.Query()
	q.Set("domain", domain)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return DomainStatus{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Fastly-Key", apiKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return DomainStatus{}, fmt.Errorf("fastly request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return DomainStatus{}, fmt.Errorf("fastly status check returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Domain string `json:"domain"`
		Zone   string `json:"zone"`
		Status string `json:"status"`
		Tags   string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return DomainStatus{}, fmt.Errorf("decoding fastly response: %w", err)
	}

	return DomainStatus{
		Domain:    payload.Domain,
		Zone:      payload.Zone,
		Status:    payload.Status,
		Tags:      payload.Tags,
		Available: slices.Contains(strings.Fields(payload.Status), "inactive"),
	}, nil
}
