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

	DefaultProviderKeys ProviderKeys

	Plugins []SessionPlugin
}

func (s *Service) Run(ctx context.Context, stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]) error {
	keys := resolveProviderKeys(
		s.DefaultProviderKeys,
		stream.RequestHeader(),
	)

	opts := []SessionOption{
		WithProviderKeys(keys),
	}

	if len(s.Plugins) > 0 {
		opts = append(opts, WithPlugins(s.Plugins...))
	}

	return NewSession(ctx, stream, opts...)
}
