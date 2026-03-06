package agentd

import (
	"context"

	"connectrpc.com/connect"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
	"github.com/apzuk3/agentd/gen/proto/go/agentd/v1/agentdv1connect"
)

var _ agentdv1connect.AgentdHandler = (*Service)(nil)

type Service struct {
	agentdv1connect.UnimplementedAgentdHandler

	GeminiAPIKey    string
	AnthropicAPIKey string
	OpenAIAPIKey    string
	TavilyAPIKey    string

	EventEmitter *SessionEventEmitter
}

func (s *Service) Run(ctx context.Context, stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]) error {
	keys := resolveProviderKeys(
		ProviderKeys{
			GeminiAPIKey:    s.GeminiAPIKey,
			AnthropicAPIKey: s.AnthropicAPIKey,
			OpenAIAPIKey:    s.OpenAIAPIKey,
			TavilyAPIKey:    s.TavilyAPIKey,
		},
		stream.RequestHeader(),
	)

	opts := []SessionOption{
		WithGeminiAPIKey(keys.GeminiAPIKey),
		WithAnthropicAPIKey(keys.AnthropicAPIKey),
		WithOpenAIAPIKey(keys.OpenAIAPIKey),
		WithTavilyAPIKey(keys.TavilyAPIKey),
	}

	if s.EventEmitter != nil {
		opts = append(opts, WithEventEmitter(s.EventEmitter))
	}

	return NewSession(ctx, stream, opts...)
}
