package agentd

import (
	"context"

	"connectrpc.com/connect"
	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
	"github.com/apzuk3/agentd/gen/proto/go/agentd/v1/agentdv1connect"
)

type Service struct {
}

var _ agentdv1connect.AgentdServiceHandler = (*Service)(nil)

func NewService() *Service {
	return &Service{}
}

func (s *Service) Run(ctx context.Context, stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]) error {
	return nil
}
