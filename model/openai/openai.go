package openai

import (
	"context"
	"fmt"
	"iter"

	openaiapi "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"google.golang.org/adk/model"
)

type Config struct {
	APIKey        string
	ClientOptions []option.RequestOption
}

type openaiModel struct {
	client openaiapi.Client
	name   string
}

func NewModel(_ context.Context, modelName string, cfg *Config) (model.LLM, error) {
	if modelName == "" {
		return nil, fmt.Errorf("model name must be provided")
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api key must be provided to use openai provider")
	}

	opts := append([]option.RequestOption{}, cfg.ClientOptions...)
	opts = append(opts, option.WithAPIKey(cfg.APIKey))

	return &openaiModel{
		name:   modelName,
		client: openaiapi.NewClient(opts...),
	}, nil
}

func (m *openaiModel) Name() string {
	return m.name
}

func (m *openaiModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if stream {
		return m.generateStream(ctx, req)
	}
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.generate(ctx, req)
		yield(resp, err)
	}
}

func (m *openaiModel) generate(ctx context.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("llm request must not be nil")
	}

	builder := RequestBuilder{modelName: m.name}
	params, err := builder.FromLLMRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := m.client.Responses.New(ctx, *params)
	if err != nil {
		return nil, fmt.Errorf("failed to send llm request to openai: %w", err)
	}

	responseBuilder := ResponseBuilder{}
	return responseBuilder.FromResponse(resp)
}

func (m *openaiModel) generateStream(ctx context.Context, req *model.LLMRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		builder := RequestBuilder{modelName: m.name}
		params, err := builder.FromLLMRequest(req)
		if err != nil {
			yield(nil, err)
			return
		}

		stream := m.client.Responses.NewStreaming(ctx, *params)
		for resp, err := range readStreamEvents(stream) {
			if !yield(resp, err) {
				return
			}
		}
	}
}
