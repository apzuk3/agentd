package anthropic

import (
	"context"
	"fmt"
	"iter"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/anthropics/anthropic-sdk-go/vertex"

	"google.golang.org/adk/model"
)

const defaultMaxTokens = 8192

const (
	envProjectID      = "GOOGLE_CLOUD_PROJECT"
	envLocation       = "GOOGLE_CLOUD_LOCATION"
	defaultOAuthScope = "https://www.googleapis.com/auth/cloud-platform"
)

const (
	ProviderVertexAI   = "vertex_ai"
	ProviderAnthropic  = "anthropic"
	ProviderAWSBedrock = "aws_bedrock"
)

type Config struct {
	Provider      string
	APIKey        string
	MaxTokens     int64
	ClientOptions []option.RequestOption
}

func (c *Config) applyDefaults() {
	if c.ClientOptions == nil {
		c.ClientOptions = []option.RequestOption{}
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = defaultMaxTokens
	}
	if c.Provider == "" {
		c.Provider = ProviderVertexAI
	}
}

type anthropicModel struct {
	client anthropic.Client

	name      string
	maxTokens int64
}

func NewModel(ctx context.Context, modelName string, cfg *Config) (model.LLM, error) {
	if modelName == "" {
		return nil, fmt.Errorf("model name must be provided")
	}

	if cfg == nil {
		cfg = &Config{}
	}
	cfg.applyDefaults()

	opts := append([]option.RequestOption{}, cfg.ClientOptions...)

	switch cfg.Provider {
	case ProviderAnthropic:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("api key must be provided to use anthropic provider")
		}
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	case ProviderAWSBedrock:
		// AWS Bedrock auth and settings should be provided via ClientOptions.
	case ProviderVertexAI:
		projectID := os.Getenv(envProjectID)
		location := os.Getenv(envLocation)
		if projectID == "" || location == "" {
			return nil, fmt.Errorf("GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION must be set to use Anthropic on Vertex")
		}
		opts = append(opts, vertex.WithGoogleAuth(ctx, location, projectID, defaultOAuthScope))
	default:
		return nil, fmt.Errorf("unsupported anthropic provider %q", cfg.Provider)
	}

	return &anthropicModel{
		name:      modelName,
		maxTokens: cfg.MaxTokens,
		client:    anthropic.NewClient(opts...),
	}, nil
}

func (m *anthropicModel) Name() string {
	return m.name
}

func (m *anthropicModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if stream {
		return m.generateStream(ctx, req)
	}
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.generate(ctx, req)
		yield(resp, err)
	}
}

func (m *anthropicModel) generate(ctx context.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("llm request must not be nil")
	}

	builder := RequestBuilder{modelName: m.name, maxTokens: m.maxTokens}
	params, err := builder.FromLLMRequest(req)
	if err != nil {
		return nil, err
	}

	msg, err := m.client.Messages.New(ctx, *params)
	if err != nil {
		return nil, fmt.Errorf("failed to send llm request to anthropic: %w", err)
	}

	responseBuilder := ResponseBuilder{}
	return responseBuilder.FromMessage(msg)
}

func (m *anthropicModel) generateStream(ctx context.Context, req *model.LLMRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		builder := RequestBuilder{modelName: m.name, maxTokens: m.maxTokens}
		params, err := builder.FromLLMRequest(req)
		if err != nil {
			yield(nil, err)
			return
		}

		stream := m.client.Messages.NewStreaming(ctx, *params)
		for resp, err := range readStreamEvents(stream) {
			if !yield(resp, err) {
				return
			}
		}
	}
}

func readStreamEvents(stream *ssestream.Stream[anthropic.MessageStreamEventUnion]) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if stream == nil {
			yield(nil, fmt.Errorf("the stream is empty"))
			return
		}
		defer func() {
			_ = stream.Close()
		}()

		var message anthropic.Message
		for stream.Next() {
			event := stream.Current()
			if err := message.Accumulate(event); err != nil {
				yield(nil, fmt.Errorf("accumulate stream event error: %w", err))
				return
			}

			partialResponse := parsePartialStreamEvent(event)
			if partialResponse != nil {
				if !yield(partialResponse, nil) {
					return
				}
			}

			if _, ok := event.AsAny().(anthropic.MessageStopEvent); ok {
				responseBuilder := ResponseBuilder{}
				finalResponse, err := responseBuilder.FromMessage(&message)
				if err != nil {
					yield(nil, err)
					return
				}
				finalResponse.TurnComplete = true
				if !yield(finalResponse, nil) {
					return
				}
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, fmt.Errorf("got stream error: %w", err))
		}
	}
}
