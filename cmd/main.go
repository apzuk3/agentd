// Command agentd-example demonstrates how to build a simple multi-agent
// system on top of the agentd package.
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

	"github.com/apzuk3/agentd"
	"google.golang.org/adk/agent"
)

func main() {
	agentd.AddTool("check_domain_availability", "Check if a domain is available", lookupDomain)

	model, err := agentd.NewModel(context.Background(), agentd.ModelGeminiFlash35)
	if err != nil {
		log.Fatal(err)
	}

	const numAgents = 10

	agents := []agent.Agent{}
	outputKeys := make([]string, 0, numAgents)

	for i := range numAgents {
		// Each agent writes its answer to a distinct state key so the
		// synthesizer below can read them all back and combine them.
		outputKey := fmt.Sprintf("domain_%d", i)
		outputKeys = append(outputKeys, outputKey)

		brainstormer, err := agentd.LLMAgent(fmt.Sprintf("idea_brainstormer_%d", i), model,
			agentd.WithLLMAgentTools("check_domain_availability"),
			agentd.WithLLMAgentDescription("Agent to convert an idea into a domain name"),
			agentd.WithLLMAgentInstruction("Your sole purpose is to convert an idea into a domain name."),
			agentd.WithLLMAgentInstruction("You MUST check if the domain is available using the check_domain_availability tool."),
			agentd.WithLLMAgentInstruction("You MUST return the response as JSON where the key is 'domain' and the value is the domain name, and 'reason' is the reason you came up with the domain name."),
			agentd.WithLLMAgentOutputKey(outputKey),
		)
		if err != nil {
			log.Fatal(err)
		}
		agents = append(agents, brainstormer)
	}

	parallel, err := agentd.ParallelAgent(agents...)
	if err != nil {
		log.Fatal(err)
	}

	// Build a list of {domain_0} ... {domain_9} placeholders. ADK resolves
	// these from session state, injecting each brainstormer's reply.
	placeholders := make([]string, 0, len(outputKeys))
	for _, key := range outputKeys {
		placeholders = append(placeholders, fmt.Sprintf("{%s}", key))
	}

	synthesizer, err := agentd.LLMAgent("domain_synthesizer", model,
		agentd.WithLLMAgentDescription("Combines the domain suggestions from all brainstormers into one result"),
		agentd.WithLLMAgentInstruction("You are given the JSON domain suggestions produced by several brainstorming agents."),
		agentd.WithLLMAgentInstruction("Combine them into a single JSON array under the key 'domains', where each item has 'domain' and 'reason'. Remove duplicates and keep only available domains."),
		agentd.WithLLMAgentInstruction("Here are the suggestions:\n"+strings.Join(placeholders, "\n")),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Run the fan-out first, then the synthesizer that merges every result.
	pipeline, err := agentd.SequentialAgent(parallel, synthesizer)
	if err != nil {
		log.Fatal(err)
	}

	agentd.LaunchCLI(pipeline)
}

type domainLookupArgs struct {
	Domain string `json:"domain"`
}

// lookupDomain reports whether a domain is available to register, using
// Fastly's Domain Research status API. It returns true when the domain is
// available and false when it is taken. The Fastly API token is read from the
// FASTLY_API_TOKEN environment variable.
func lookupDomain(_ agentd.ToolContext, args domainLookupArgs) (bool, error) {
	domain := args.Domain

	token := os.Getenv("FASTLY_API_TOKEN")
	if token == "" {
		return false, fmt.Errorf("FASTLY_API_TOKEN environment variable is not set")
	}

	endpoint := "https://api.fastly.com/domain-management/v1/tools/status"
	q := url.Values{}
	q.Set("domain", domain)
	reqURL := endpoint + "?" + q.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return false, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Fastly-Key", token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("calling Fastly domain status API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("Fastly domain status API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Domain string `json:"domain"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("decoding response: %w", err)
	}

	// status is a space-delimited string of Domainr-style statuses. A domain is
	// available to register when its status set includes "inactive" or
	// "undelegated".
	statuses := strings.Fields(result.Status)
	available := slices.Contains(statuses, "inactive") || slices.Contains(statuses, "undelegated")

	return available, nil
}
