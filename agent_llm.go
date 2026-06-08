package agentd

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
)

type Model string

type ModelProvider string

const (
	ModelProviderAnthropic ModelProvider = "anthropic"
	ModelProviderOpenAI    ModelProvider = "openai"
	ModelProviderGemini    ModelProvider = "gemini"
)

const (
	// Anthropic Claude
	ModelClaudeOpus48   Model = "claude-opus-4-8"
	ModelClaudeOpus47   Model = "claude-opus-4-7"
	ModelClaudeSonnet46 Model = "claude-sonnet-4-6"
	ModelClaudeSonnet45 Model = "claude-sonnet-4-5"
	ModelClaudeHaiku45  Model = "claude-haiku-4-5"

	// OpenAI GPT
	ModelOpenAIGPT55     Model = "gpt-5.5"
	ModelOpenAIGPT55Pro  Model = "gpt-5.5-pro"
	ModelOpenAIGPT55Mini Model = "gpt-5.5-mini"
	ModelOpenAIGPT55Nano Model = "gpt-5.5-nano"
	ModelOpenAIGPT54     Model = "gpt-5.4"
	ModelOpenAIGPT54Mini Model = "gpt-5.4-mini"
	ModelOpenAIGPT54Nano Model = "gpt-5.4-nano"

	// Google Gemini
	ModelGeminiFlash35     Model = "gemini-3.5-flash"
	ModelGeminiPro31       Model = "gemini-3.1-pro-preview"
	ModelGeminiFlashLite31 Model = "gemini-3.1-flash-lite"
	ModelGeminiFlash3      Model = "gemini-3-flash-preview"
	ModelGeminiPro25       Model = "gemini-2.5-pro"
)

func (m Model) Provider() ModelProvider {
	switch m {
	case ModelClaudeOpus48, ModelClaudeOpus47, ModelClaudeSonnet46, ModelClaudeSonnet45, ModelClaudeHaiku45:
		return ModelProviderAnthropic
	case ModelOpenAIGPT55, ModelOpenAIGPT55Pro, ModelOpenAIGPT55Mini, ModelOpenAIGPT55Nano, ModelOpenAIGPT54, ModelOpenAIGPT54Mini, ModelOpenAIGPT54Nano:
		return ModelProviderOpenAI
	case ModelGeminiFlash35, ModelGeminiPro31, ModelGeminiFlashLite31, ModelGeminiFlash3, ModelGeminiPro25:
		return ModelProviderGemini
	}
	return ""
}

type LLMAgentOption func(*llmagent.Config) error

func WithLLMAgentDescription(description string) LLMAgentOption {
	return func(cfg *llmagent.Config) error {
		cfg.Description = description

		return nil
	}
}

func WithLLMAgentModel(model Model) LLMAgentOption {
	return func(cfg *llmagent.Config) error {
		return nil
	}
}

func WithLLMAgentInstruction(instruction string) LLMAgentOption {
	return func(cfg *llmagent.Config) error {
		cfg.Instruction = instruction

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
