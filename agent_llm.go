package agentd

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
)

type Agent agent.Agent

const (
	// Anthropic Claude
	ModelClaudeOpus48   = "claude-opus-4-8"
	ModelClaudeOpus47   = "claude-opus-4-7"
	ModelClaudeSonnet46 = "claude-sonnet-4-6"
	ModelClaudeSonnet45 = "claude-sonnet-4-5"
	ModelClaudeHaiku45  = "claude-haiku-4-5"

	// OpenAI GPT
	ModelOpenAIGPT55     = "gpt-5.5"
	ModelOpenAIGPT55Pro  = "gpt-5.5-pro"
	ModelOpenAIGPT55Mini = "gpt-5.5-mini"
	ModelOpenAIGPT55Nano = "gpt-5.5-nano"
	ModelOpenAIGPT54     = "gpt-5.4"
	ModelOpenAIGPT54Mini = "gpt-5.4-mini"
	ModelOpenAIGPT54Nano = "gpt-5.4-nano"

	// Google Gemini
	ModelGeminiFlash35     = "gemini-3.5-flash"
	ModelGeminiPro31       = "gemini-3.1-pro-preview"
	ModelGeminiFlashLite31 = "gemini-3.1-flash-lite"
	ModelGeminiFlash3      = "gemini-3-flash-preview"
	ModelGeminiPro25       = "gemini-2.5-pro"

	// Built-in tools. These are automatically registered on package import.
	// Configure them (e.g. API credentials) using ConfigureBuiltin* functions.

	// BuiltinTavily is the registered name of the built-in Tavily tool.
	//
	// It is registered by default into the global tool registry.
	// Reference it when creating agents:
	//
	//	root, _ := agentd.LLMAgent("researcher", model,
	//	    agentd.WithLLMAgentTools(agentd.BuiltinTavily),
	//	)
	//
	// Before creating agents (or at least before the tool is called),
	// configure credentials:
	//
	//	agentd.ConfigureBuiltinTavily(
	//	    agentd.WithTavilyAPIKey(os.Getenv("TAVILY_API_KEY")),
	//	)
	//
	// The tool will also read TAVILY_API_KEY (and a few common aliases)
	// from the environment automatically if no explicit key was configured.
	BuiltinTavily = "builtin:tavily"
)

type LLMAgentOption func(*llmagent.Config) error

func WithLLMAgentDescription(description string) LLMAgentOption {
	return func(cfg *llmagent.Config) error {
		cfg.Description = description

		return nil
	}
}

func WithLLMAgentModel(model string) LLMAgentOption {
	return func(cfg *llmagent.Config) error {
		return nil
	}
}

func WithLLMAgentInstruction(instruction string) LLMAgentOption {
	return func(cfg *llmagent.Config) error {
		if cfg.Instruction != "" {
			cfg.Instruction = cfg.Instruction + "\n\n" + instruction
		} else {
			cfg.Instruction = instruction
		}

		return nil
	}
}

// WithLLMAgentOutputKey stores the agent's final reply in session state under
// the given key.
//
// This sets llmagent.Config.OutputKey. It is the building block for combining
// the results of several agents: give each agent a unique output key, then have
// a downstream agent read those keys via {key} placeholders in its instruction
// (ADK resolves them from session state). This is the standard way to merge the
// outputs of a ParallelAgent fan-out into a single combined answer.
//
// Example:
//
//	agentd.LLMAgent("brainstormer_0", model,
//	    agentd.WithLLMAgentOutputKey("domain_0"),
//	)
func WithLLMAgentOutputKey(key string) LLMAgentOption {
	return func(cfg *llmagent.Config) error {
		cfg.OutputKey = key
		return nil
	}
}

func WithLLMAgentTools(tools ...string) LLMAgentOption {
	return func(cfg *llmagent.Config) error {
		for _, tool := range tools {
			tool, ok := defaultToolRegistry.tools[tool]
			if !ok {
				return fmt.Errorf("tool %s not found", tool)
			}
			cfg.Tools = append(cfg.Tools, tool)
		}

		return nil
	}
}

func LLMAgent(name string, model model.LLM, options ...LLMAgentOption) (agent.Agent, error) {
	cfg := llmagent.Config{
		Name:  name,
		Model: model,
	}
	for _, option := range options {
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}
	return llmagent.New(cfg)
}
