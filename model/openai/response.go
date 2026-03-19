package openai

import (
	"encoding/json"
	"fmt"
	"iter"

	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

type ResponseBuilder struct{}

func (builder *ResponseBuilder) FromResponse(resp *responses.Response) (*model.LLMResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("response must not be nil")
	}

	parts := make([]*genai.Part, 0)
	for _, item := range resp.Output {
		itemParts, err := partsFromOutputItem(item)
		if err != nil {
			return nil, err
		}
		parts = append(parts, itemParts...)
	}

	llmResp := &model.LLMResponse{
		Content:       genai.NewContentFromParts(parts, genai.RoleModel),
		FinishReason:  buildFinishReason(resp),
		UsageMetadata: extractUsage(resp),
	}

	custom := make(map[string]any)
	if resp.Status != "" {
		custom["status"] = resp.Status
	}
	if resp.IncompleteDetails.Reason != "" {
		custom["incomplete_reason"] = resp.IncompleteDetails.Reason
	}
	if len(custom) > 0 {
		llmResp.CustomMetadata = custom
	}

	if resp.Error.JSON.Message.Valid() {
		return nil, fmt.Errorf("openai response failed: %s", resp.Error.Message)
	}

	return llmResp, nil
}

func readStreamEvents(stream *ssestream.Stream[responses.ResponseStreamEventUnion]) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if stream == nil {
			yield(nil, fmt.Errorf("the stream is empty"))
			return
		}
		defer func() {
			_ = stream.Close()
		}()

		builder := ResponseBuilder{}

		for stream.Next() {
			event := stream.Current()

			switch e := event.AsAny().(type) {
			case responses.ResponseTextDeltaEvent:
				if e.Delta == "" {
					continue
				}
				if !yield(&model.LLMResponse{
					Content: genai.NewContentFromParts([]*genai.Part{{Text: e.Delta}}, genai.RoleModel),
					Partial: true,
				}, nil) {
					return
				}

			case responses.ResponseReasoningSummaryTextDeltaEvent:
				if e.Delta == "" {
					continue
				}
				if !yield(&model.LLMResponse{
					Content: genai.NewContentFromParts([]*genai.Part{{Text: e.Delta, Thought: true}}, genai.RoleModel),
					Partial: true,
				}, nil) {
					return
				}

			case responses.ResponseOutputItemDoneEvent:
				parts, err := partsFromOutputItem(e.Item)
				if err != nil {
					yield(nil, err)
					return
				}
				if len(parts) == 0 {
					continue
				}
				if !yield(&model.LLMResponse{
					Content: genai.NewContentFromParts(parts, genai.RoleModel),
				}, nil) {
					return
				}

			case responses.ResponseFailedEvent:
				msg := e.Response.Error.Message
				if msg == "" {
					msg = "response failed"
				}
				yield(nil, fmt.Errorf("openai stream failed: %s", msg))
				return

			case responses.ResponseErrorEvent:
				yield(nil, fmt.Errorf("openai stream error: %s", e.Message))
				return

			case responses.ResponseCompletedEvent:
				finalResp, err := builder.FromResponse(&e.Response)
				if err != nil {
					yield(nil, err)
					return
				}
				finalResp.TurnComplete = true
				yield(finalResp, nil)
				return
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, fmt.Errorf("got stream error: %w", err))
		}
	}
}

func partsFromOutputItem(item responses.ResponseOutputItemUnion) ([]*genai.Part, error) {
	switch v := item.AsAny().(type) {
	case responses.ResponseOutputMessage:
		parts := make([]*genai.Part, 0, len(v.Content))
		for _, content := range v.Content {
			switch contentPart := content.AsAny().(type) {
			case responses.ResponseOutputText:
				if contentPart.Text != "" {
					parts = append(parts, genai.NewPartFromText(contentPart.Text))
				}
			case responses.ResponseOutputRefusal:
				if contentPart.Refusal != "" {
					parts = append(parts, genai.NewPartFromText(contentPart.Refusal))
				}
			}
		}
		return parts, nil

	case responses.ResponseFunctionToolCall:
		return []*genai.Part{{
			FunctionCall: &genai.FunctionCall{
				ID:   v.CallID,
				Name: v.Name,
				Args: parseJSONArgs(v.Arguments),
			},
		}}, nil

	case responses.ResponseReasoningItem:
		parts := make([]*genai.Part, 0, len(v.Summary))
		for _, summary := range v.Summary {
			if summary.Text == "" {
				continue
			}
			parts = append(parts, &genai.Part{Text: summary.Text, Thought: true})
		}
		return parts, nil
	}

	return nil, nil
}

func extractUsage(resp *responses.Response) *genai.GenerateContentResponseUsageMetadata {
	if resp == nil || !resp.JSON.Usage.Valid() {
		return nil
	}

	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        int32(resp.Usage.InputTokens),
		CandidatesTokenCount:    int32(resp.Usage.OutputTokens),
		TotalTokenCount:         int32(resp.Usage.TotalTokens),
		CachedContentTokenCount: int32(resp.Usage.InputTokensDetails.CachedTokens),
		ThoughtsTokenCount:      int32(resp.Usage.OutputTokensDetails.ReasoningTokens),
	}
}

func buildFinishReason(resp *responses.Response) genai.FinishReason {
	if resp == nil {
		return genai.FinishReasonUnspecified
	}

	if resp.Status == responses.ResponseStatusIncomplete {
		switch resp.IncompleteDetails.Reason {
		case "max_output_tokens":
			return genai.FinishReasonMaxTokens
		case "content_filter":
			return genai.FinishReasonSafety
		default:
			return genai.FinishReasonUnspecified
		}
	}

	return genai.FinishReasonStop
}

func parseJSONArgs(argsJSON string) map[string]any {
	if argsJSON == "" {
		return map[string]any{}
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return map[string]any{}
	}
	return args
}
