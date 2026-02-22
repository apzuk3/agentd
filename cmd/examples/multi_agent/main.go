package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"

	"github.com/apzuk3/agentd/client"
)

func main() {
	topic := "the future of quantum computing"
	if len(os.Args) > 1 {
		topic = strings.Join(os.Args[1:], " ")
	}

	c := client.New("http://localhost:8080")

	type SearchInput struct {
		Query string `json:"query" jsonschema:"description=The search query to look up"`
	}
	client.AddTool(c, "web_search", "Search the web for information on a topic", func(_ context.Context, in SearchInput) (any, error) {
		log.Printf("[tool] web_search(%q)", in.Query)
		time.Sleep(200 * time.Millisecond)
		return map[string]any{
			"results": []map[string]string{
				{"title": "Quantum Computing Breakthroughs 2026", "snippet": "Recent advances in error correction have brought fault-tolerant quantum computing closer to reality. IBM and Google both achieved milestones in qubit coherence times."},
				{"title": "Practical Applications of Quantum Computing", "snippet": "Drug discovery, cryptography, and materials science are the three most promising near-term application areas for quantum computers."},
				{"title": "Quantum vs Classical Computing", "snippet": "While quantum computers excel at specific problem classes like optimization and simulation, they are unlikely to replace classical computers for general-purpose tasks."},
			},
		}, nil
	})

	type FactCheckInput struct {
		Claim string `json:"claim" jsonschema:"description=A factual claim to verify"`
	}
	client.AddTool(c, "fact_check", "Verify whether a factual claim is accurate", func(_ context.Context, in FactCheckInput) (any, error) {
		log.Printf("[tool] fact_check(%q)", in.Claim)
		time.Sleep(100 * time.Millisecond)
		return map[string]any{
			"verdict":    "plausible",
			"confidence": 0.85,
			"note":       "Claim aligns with current published research as of early 2026.",
		}, nil
	})

	agent := &agentdv1.Agent{
		Name:        "article_pipeline",
		Description: "A multi-agent pipeline that researches a topic and writes an article",
		AgentType: &agentdv1.Agent_Llm{
			Llm: &agentdv1.LlmAgent{
				Model:       "gemini-2.5-flash",
				Instruction: "You are an orchestrator. Delegate research to the researcher sub-agent, then write a polished short article based on the gathered facts. Keep it under 300 words.",
				Tools: []*agentdv1.Tool{
					c.Tool("web_search"),
					c.Tool("fact_check"),
				},
				SubAgents: []*agentdv1.Agent{
					{
						Name:        "researcher",
						Description: "Gathers information by searching the web and fact-checking claims",
						AgentType: &agentdv1.Agent_Llm{
							Llm: &agentdv1.LlmAgent{
								Model:       "gemini-2.5-flash",
								Instruction: "You are a research assistant. Use the web_search tool to find relevant information about the given topic. Use the fact_check tool to verify key claims. Summarize your findings clearly.",
								Tools: []*agentdv1.Tool{
									c.Tool("web_search"),
									c.Tool("fact_check"),
								},
							},
						},
					},
				},
			},
		},
	}

	userPrompt := fmt.Sprintf("Write a short article about %s", topic)
	fmt.Printf("Prompt: %s\n\n", userPrompt)

	for ev, err := range c.Run(context.Background(), agent, userPrompt) {
		if err != nil {
			log.Fatalf("stream error: %v", err)
		}

		switch {
		case ev.OutputChunk != nil:
			if len(ev.OutputChunk.AgentPath) > 0 {
				agent := ev.OutputChunk.AgentPath[len(ev.OutputChunk.AgentPath)-1]
				if ev.OutputChunk.Last {
					fmt.Printf("\n--- [%s done] ---\n\n", agent)
					continue
				}
				_ = agent
			}
			fmt.Print(ev.OutputChunk.Content)

		case ev.Error != nil:
			log.Fatalf("agent error [%s]: %s", ev.Error.Code, ev.Error.Message)

		case ev.End != nil:
			fmt.Println()
			if u := ev.End.UsageSummary; u != nil {
				fmt.Printf("--- Usage: %d LLM calls, %d total tokens, $%.6f ---\n",
					u.LlmCalls, u.TotalUsage.GetTotalTokens(), u.EstimatedCost)
			}
		}
	}
}
