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
}

func (s *Service) Run(ctx context.Context, stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]) error {
	return NewSession(ctx, stream,
		WithGeminiAPIKey(s.GeminiAPIKey),
		WithAnthropicAPIKey(s.AnthropicAPIKey),
		WithOpenAIAPIKey(s.OpenAIAPIKey),
		WithTavilyAPIKey(s.TavilyAPIKey),
	)
}
