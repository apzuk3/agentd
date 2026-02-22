package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"

	"github.com/apzuk3/agentd/client"
)

func main() {
	prompt := "Explain the difference between concurrency and parallelism in two sentences."
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	c := client.New("http://localhost:8080")

	agent := &agentdv1.Agent{
		Name:        "assistant",
		Description: "A helpful general-purpose assistant",
		AgentType: &agentdv1.Agent_Llm{
			Llm: &agentdv1.LlmAgent{
				Model:       "gemini-2.5-flash",
				Instruction: "You are a helpful, concise assistant. Answer the user's question directly.",
			},
		},
	}

	fmt.Printf("Prompt: %s\n\n", prompt)

	for ev, err := range c.Run(context.Background(), agent, prompt) {
		if err != nil {
			log.Fatalf("stream error: %v", err)
		}

		switch {
		case ev.OutputChunk != nil:
			fmt.Print(ev.OutputChunk.Content)
			if ev.OutputChunk.Last {
				fmt.Println()
			}

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
