// Command agentd-example demonstrates how to build a simple multi-agent
// system on top of the agentd package.
package main

import (
	"context"
	"log"

	"github.com/apzuk3/agentd"
	"google.golang.org/adk/tool"
)

// DeployInput is the input schema for the example "app.deploy" tool.
type DeployInput struct {
	Environment string `json:"environment" jsonschema_description:"Deployment environment"`
	Ref         string `json:"ref" jsonschema_description:"Git ref to deploy"`
}

// DeployOutput is the output schema for the example "app.deploy" tool.
type DeployOutput struct {
	URL string `json:"url" jsonschema_description:"URL of the deployed application"`
}

func main() {
	agentd.AddTool("app.deploy", "Deploys the current app to production", func(_ tool.Context, _ DeployInput) (DeployOutput, error) {
		return DeployOutput{URL: "https://example.com"}, nil
	})

	model, err := agentd.NewModel(context.Background(), agentd.ModelGeminiFlash35)
	if err != nil {
		log.Fatal(err)
	}

	rootAgent, err := agentd.LLMAgent(
		"executor",
		model,
		agentd.WithLLMAgentTools("app.deploy"),
		agentd.WithLLMAgentDescription("Agent to answer questions about the time and weather in a city."),
		agentd.WithLLMAgentInstruction("Your SOLE purpose is to answer questions about the current time and weather in a specific city. You MUST refuse to answer any questions unrelated to time or weather."),
	)
	if err != nil {
		log.Fatal(err)
	}

	agentd.CLI(rootAgent)
}
