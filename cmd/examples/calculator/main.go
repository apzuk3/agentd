package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strings"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"

	"github.com/apzuk3/agentd/client"
)

type MathInput struct {
	A float64 `json:"a" jsonschema:"description=First operand"`
	B float64 `json:"b" jsonschema:"description=Second operand"`
}

func main() {
	prompt := "What is (12.5 * 4) + (100 / 8) - 3.75?"
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	c := client.New("http://localhost:8080")

	client.AddTool(c, "add", "Add two numbers and return the sum", func(_ context.Context, in MathInput) (any, error) {
		result := in.A + in.B
		log.Printf("[tool] add(%g, %g) = %g", in.A, in.B, result)
		return result, nil
	})

	client.AddTool(c, "subtract", "Subtract b from a and return the difference", func(_ context.Context, in MathInput) (any, error) {
		result := in.A - in.B
		log.Printf("[tool] subtract(%g, %g) = %g", in.A, in.B, result)
		return result, nil
	})

	client.AddTool(c, "multiply", "Multiply two numbers and return the product", func(_ context.Context, in MathInput) (any, error) {
		result := in.A * in.B
		log.Printf("[tool] multiply(%g, %g) = %g", in.A, in.B, result)
		return result, nil
	})

	client.AddTool(c, "divide", "Divide a by b and return the quotient", func(_ context.Context, in MathInput) (any, error) {
		if in.B == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		result := in.A / in.B
		log.Printf("[tool] divide(%g, %g) = %g", in.A, in.B, result)
		return result, nil
	})

	type SqrtInput struct {
		X float64 `json:"x" jsonschema:"description=The number to take the square root of"`
	}
	client.AddTool(c, "sqrt", "Return the square root of a number", func(_ context.Context, in SqrtInput) (any, error) {
		if in.X < 0 {
			return nil, fmt.Errorf("cannot take square root of negative number %g", in.X)
		}
		result := math.Sqrt(in.X)
		log.Printf("[tool] sqrt(%g) = %g", in.X, result)
		return result, nil
	})

	agent := &agentdv1.Agent{
		Name:        "calculator",
		Description: "A calculator agent that solves math problems step by step",
		AgentType: &agentdv1.Agent_Llm{
			Llm: &agentdv1.LlmAgent{
				Model: "gemini-2.5-flash",
				Instruction: `You are a precise calculator assistant. 
You MUST use the provided math tools to compute every arithmetic operation — never calculate in your head.
Break complex expressions into individual operations and use tools for each step.
Show your work clearly.`,
				Tools: []*agentdv1.Tool{
					c.Tool("add"),
					c.Tool("subtract"),
					c.Tool("multiply"),
					c.Tool("divide"),
					c.Tool("sqrt"),
				},
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
