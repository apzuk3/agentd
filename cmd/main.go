// Command agentd-example demonstrates how to build a simple multi-agent
// system on top of the agentd package.
package main

import (
	"context"
	"log"
	"os"

	"github.com/apzuk3/agentd"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
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
		agentd.WithLLMAgentDescription("Executes approved local actions"),
		agentd.WithLLMAgentInstruction("Executes approved local actions"),
	)
	if err != nil {
		log.Fatal(err)
	}

	config := &launcher.Config{
		AgentLoader: agent.NewSingleLoader(rootAgent),
	}

	l := full.NewLauncher()
	if err = l.Execute(context.Background(), config, os.Args[1:]); err != nil {
		log.Fatalf("Run failed: %v\n\n%s", err, l.CommandLineSyntax())
	}
}
