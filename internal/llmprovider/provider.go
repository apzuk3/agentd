package llmprovider

import (
	domainv1 "github.com/syss-io/executor/gen/proto/go/domain/v1"
)

// Model represents an LLM model with its capabilities and pricing
type Model struct {
	ID                    string  // Model identifier (e.g., "gemini-2.5-pro")
	Name                  string  // Human-readable name
	CostPer1MInput        float64 // USD per 1M input tokens
	CostPer1MOutput       float64 // USD per 1M output tokens
	CostPer1MInputCached  float64 // USD per 1M cached input tokens
	CostPer1MOutputCached float64 // USD per 1M cached output tokens
	ContextWindow         int     // Maximum context window size in tokens
	DefaultMaxTokens      int     // Default max output tokens
	CanReason             bool    // Whether the model supports reasoning
	HasReasoningEfforts   bool    // Whether reasoning efforts can be configured
	SupportsAttachments   bool    // Whether the model supports file attachments
}

// ModelTable maps model enum to model configuration
var ModelTable = map[domainv1.Model]Model{
	domainv1.Model_MODEL_GEMINI_3_PRO: {
		ID:                    "gemini-3-pro-preview",
		Name:                  "Gemini 3 Pro (Preview)",
		CostPer1MInput:        2.0,
		CostPer1MOutput:       12.0,
		CostPer1MInputCached:  0,
		CostPer1MOutputCached: 0.2,
		ContextWindow:         1048576,
		DefaultMaxTokens:      64000,
		CanReason:             true,
		HasReasoningEfforts:   false,
		SupportsAttachments:   true,
	},
	domainv1.Model_MODEL_GEMINI_3_FLASH: {
		ID:                    "gemini-3-flash-preview",
		Name:                  "Gemini 3 Flash (Preview)",
		CostPer1MInput:        0.5,
		CostPer1MOutput:       3.0,
		CostPer1MInputCached:  0,
		CostPer1MOutputCached: 0.05,
		ContextWindow:         1048576,
		DefaultMaxTokens:      50000,
		CanReason:             true,
		HasReasoningEfforts:   false,
		SupportsAttachments:   true,
	},
	domainv1.Model_MODEL_GEMINI_2_5_PRO: {
		ID:                    "gemini-2.5-pro",
		Name:                  "Gemini 2.5 Pro",
		CostPer1MInput:        1.25,
		CostPer1MOutput:       10.0,
		CostPer1MInputCached:  0,
		CostPer1MOutputCached: 0.125,
		ContextWindow:         1048576,
		DefaultMaxTokens:      50000,
		CanReason:             true,
		HasReasoningEfforts:   false,
		SupportsAttachments:   true,
	},
	domainv1.Model_MODEL_GEMINI_2_5_FLASH: {
		ID:                    "gemini-2.5-flash",
		Name:                  "Gemini 2.5 Flash",
		CostPer1MInput:        0.3,
		CostPer1MOutput:       2.5,
		CostPer1MInputCached:  0,
		CostPer1MOutputCached: 0.03,
		ContextWindow:         1048576,
		DefaultMaxTokens:      50000,
		CanReason:             true,
		HasReasoningEfforts:   false,
		SupportsAttachments:   true,
	},
	domainv1.Model_MODEL_GEMINI_2_5_FLASH_LITE: {
		ID:                    "gemini-2.5-flash-lite",
		Name:                  "Gemini 2.5 Flash Lite",
		CostPer1MInput:        0.15,
		CostPer1MOutput:       1.25,
		CostPer1MInputCached:  0,
		CostPer1MOutputCached: 0.015,
		ContextWindow:         1048576,
		DefaultMaxTokens:      50000,
		CanReason:             false,
		HasReasoningEfforts:   false,
		SupportsAttachments:   true,
	},
}

// CalculateCost returns the cost in USD for given token counts
func CalculateCost(model domainv1.Model, inputTokens, outputTokens int32) float64 {
	m, ok := ModelTable[model]
	if !ok {
		m = ModelTable[domainv1.Model_MODEL_GEMINI_2_5_PRO]
	}

	inputCost := (float64(inputTokens) / 1_000_000) * m.CostPer1MInput
	outputCost := (float64(outputTokens) / 1_000_000) * m.CostPer1MOutput

	return inputCost + outputCost
}

// CalculateCostBreakdown returns separate input and output costs in USD
func CalculateCostBreakdown(model domainv1.Model, inputTokens, outputTokens int32) (inputCost, outputCost float64) {
	m, ok := ModelTable[model]
	if !ok {
		m = ModelTable[domainv1.Model_MODEL_GEMINI_2_5_PRO]
	}

	inputCost = (float64(inputTokens) / 1_000_000) * m.CostPer1MInput
	outputCost = (float64(outputTokens) / 1_000_000) * m.CostPer1MOutput
	return
}

// GetModel returns the model configuration for a specific model enum
func GetModel(model domainv1.Model) (Model, bool) {
	m, ok := ModelTable[model]
	return m, ok
}

// GetModelID returns the API model ID string for a model enum
func GetModelID(model domainv1.Model) string {
	m, ok := ModelTable[model]
	if !ok {
		return "gemini-2.5-flash"
	}
	return m.ID
}
