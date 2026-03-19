package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	openaiapi "github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

type RequestBuilder struct {
	modelName string
}

func (builder *RequestBuilder) FromLLMRequest(req *model.LLMRequest) (*responses.ResponseNewParams, error) {
	if req == nil {
		return nil, fmt.Errorf("llm request must not be nil")
	}

	items, err := builder.buildInputItems(req.Contents)
	if err != nil {
		return nil, err
	}

	params := &responses.ResponseNewParams{
		Model: shared.ResponsesModel(builder.modelName),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam(items),
		},
	}

	if err := builder.appendConfigOptions(params, req.Config); err != nil {
		return nil, err
	}

	return params, nil
}

func (builder *RequestBuilder) buildInputItems(contents []*genai.Content) ([]responses.ResponseInputItemUnionParam, error) {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(contents))

	for _, content := range contents {
		if content == nil {
			continue
		}

		role := toMessageRole(content.Role)
		parts := make(responses.ResponseInputMessageContentListParam, 0, len(content.Parts))

		flushMessage := func() {
			if len(parts) == 0 {
				return
			}
			items = append(items, responses.ResponseInputItemParamOfMessage(parts, role))
			parts = make(responses.ResponseInputMessageContentListParam, 0)
		}

		for _, part := range content.Parts {
			if part == nil {
				continue
			}

			switch {
			case part.Text != "":
				parts = append(parts, responses.ResponseInputContentParamOfInputText(part.Text))

			case part.FunctionCall != nil:
				flushMessage()
				argsJSON, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal function call args: %w", err)
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(string(argsJSON), part.FunctionCall.ID, part.FunctionCall.Name))

			case part.FunctionResponse != nil:
				flushMessage()
				items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(part.FunctionResponse.ID, stringifyFunctionResponse(part.FunctionResponse.Response)))

			case isImagePart(part):
				dataURL := fmt.Sprintf("data:%s;base64,%s", part.InlineData.MIMEType, base64.StdEncoding.EncodeToString(part.InlineData.Data))
				parts = append(parts, responses.ResponseInputContentUnionParam{
					OfInputImage: &responses.ResponseInputImageParam{
						Detail:   responses.ResponseInputImageDetailAuto,
						ImageURL: openaiapi.String(dataURL),
					},
				})

			case part.ExecutableCode != nil:
				code := fmt.Sprintf("Code:```%s\n%s\n```", part.ExecutableCode.Language, part.ExecutableCode.Code)
				parts = append(parts, responses.ResponseInputContentParamOfInputText(code))

			case part.CodeExecutionResult != nil:
				output := fmt.Sprintf("Execution Result:```code_output\n%s\n```", part.CodeExecutionResult.Output)
				parts = append(parts, responses.ResponseInputContentParamOfInputText(output))

			default:
				return nil, fmt.Errorf("unsupported part type %+v", part)
			}
		}

		flushMessage()
	}

	return items, nil
}

func (builder *RequestBuilder) appendConfigOptions(params *responses.ResponseNewParams, cfg *genai.GenerateContentConfig) error {
	if cfg == nil {
		return nil
	}

	if cfg.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openaiapi.Int(int64(cfg.MaxOutputTokens))
	}
	if cfg.Temperature != nil {
		params.Temperature = openaiapi.Float(float64(*cfg.Temperature))
	}
	if cfg.TopP != nil {
		params.TopP = openaiapi.Float(float64(*cfg.TopP))
	}

	if cfg.ThinkingConfig != nil {
		effort := shared.ReasoningEffortMedium
		switch cfg.ThinkingConfig.ThinkingLevel {
		case genai.ThinkingLevelLow:
			effort = shared.ReasoningEffortLow
		case genai.ThinkingLevelHigh:
			effort = shared.ReasoningEffortHigh
		}
		params.Reasoning = shared.ReasoningParam{Effort: effort}
	}

	if cfg.SystemInstruction != nil {
		if instructions := extractTextFromContent(cfg.SystemInstruction); instructions != "" {
			params.Instructions = openaiapi.String(instructions)
		}
	}

	if len(cfg.Tools) > 0 {
		tools, err := convertTools(cfg.Tools)
		if err != nil {
			return err
		}
		if len(tools) > 0 {
			params.Tools = tools
		}
	}

	if cfg.ResponseSchema != nil {
		schemaMap, err := schemaToMap(cfg.ResponseSchema)
		if err != nil {
			return fmt.Errorf("failed to convert response schema: %w", err)
		}
		format := responses.ResponseFormatTextConfigParamOfJSONSchema("response", schemaMap)
		if format.OfJSONSchema != nil {
			format.OfJSONSchema.Strict = openaiapi.Bool(true)
		}
		params.Text = responses.ResponseTextConfigParam{Format: format}
	} else if cfg.ResponseMIMEType == "application/json" {
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{OfJSONObject: &responses.ResponseFormatJSONObjectParam{}},
		}
	}

	return nil
}

func convertTools(genaiTools []*genai.Tool) ([]responses.ToolUnionParam, error) {
	tools := make([]responses.ToolUnionParam, 0)

	for _, genaiTool := range genaiTools {
		if genaiTool == nil {
			continue
		}

		if genaiTool.GoogleSearch != nil ||
			genaiTool.CodeExecution != nil ||
			genaiTool.FileSearch != nil ||
			genaiTool.Retrieval != nil ||
			genaiTool.ComputerUse != nil {
			return nil, fmt.Errorf("only function declarations are supported for openai custom tools")
		}

		for _, fn := range genaiTool.FunctionDeclarations {
			if fn == nil {
				continue
			}
			if fn.Name == "" {
				return nil, fmt.Errorf("function declaration missing name")
			}

			parameters, err := functionParametersToMap(fn)
			if err != nil {
				return nil, fmt.Errorf("invalid function declaration %q: %w", fn.Name, err)
			}

			tool := responses.ToolParamOfFunction(fn.Name, parameters, true)
			if fn.Description != "" && tool.OfFunction != nil {
				tool.OfFunction.Description = openaiapi.String(fn.Description)
			}
			tools = append(tools, tool)
		}
	}

	return tools, nil
}

func functionParametersToMap(fn *genai.FunctionDeclaration) (map[string]any, error) {
	if fn == nil {
		return nil, fmt.Errorf("function declaration must not be nil")
	}

	if fn.ParametersJsonSchema != nil {
		return schemaToMap(fn.ParametersJsonSchema)
	}

	if fn.Parameters != nil {
		return schemaToMap(fn.Parameters)
	}

	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}, nil
}

func schemaToMap(schema any) (map[string]any, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	normalizeTypeStrings(result)
	return result, nil
}

func normalizeTypeStrings(value any) {
	switch v := value.(type) {
	case map[string]any:
		if typeVal, ok := v["type"].(string); ok && typeVal != "" {
			v["type"] = strings.ToLower(typeVal)
		}
		if items, ok := v["items"]; ok {
			normalizeTypeStrings(items)
		}
		if props, ok := v["properties"].(map[string]any); ok {
			for _, prop := range props {
				normalizeTypeStrings(prop)
			}
		}
		if anyOf, ok := v["anyOf"].([]any); ok {
			for _, item := range anyOf {
				normalizeTypeStrings(item)
			}
		}
	case []any:
		for _, item := range v {
			normalizeTypeStrings(item)
		}
	}
}

func toMessageRole(role string) responses.EasyInputMessageRole {
	switch role {
	case "assistant", "model":
		return responses.EasyInputMessageRoleAssistant
	case "system":
		return responses.EasyInputMessageRoleSystem
	case "developer":
		return responses.EasyInputMessageRoleDeveloper
	default:
		return responses.EasyInputMessageRoleUser
	}
}

func extractTextFromContent(content *genai.Content) string {
	if content == nil {
		return ""
	}
	texts := make([]string, 0, len(content.Parts))
	for _, part := range content.Parts {
		if part != nil && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func stringifyFunctionResponse(resp map[string]any) string {
	if resp == nil {
		return ""
	}
	if result, ok := resp["result"]; ok && result != nil {
		return fmt.Sprint(result)
	}
	if output, ok := resp["output"]; ok && output != nil {
		return fmt.Sprint(output)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Sprint(resp)
	}
	return string(data)
}

func isImagePart(part *genai.Part) bool {
	if part == nil || part.InlineData == nil {
		return false
	}
	mime := strings.ToLower(part.InlineData.MIMEType)
	return strings.HasPrefix(mime, "image")
}
