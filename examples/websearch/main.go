package main

import (
	"context"
	"fmt"
	"log"

	"github.com/apzuk3/agentd/pkg/client"
	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

func main() {
	clnt := client.New("http://localhost:8080")

	agent := &agentdv1.Agent{
		Name:        "researcher",
		Description: "A research assistant that can search the web for current information",
		AgentType: &agentdv1.Agent_Llm{
			Llm: &agentdv1.LlmAgent{
				Model:        "gemini-2.5-flash",
				BuiltinTools: []string{"web_search"},
				Instruction:  "You are a helpful research assistant. Use web search to find current, accurate information. Always cite your sources with URLs.",
			},
		},
	}

	iter := clnt.RunAsync(context.Background(), agent, "What are the latest developments in Go 1.26?")
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
