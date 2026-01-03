package executor

import (
	"context"

	"connectrpc.com/connect"
	executorv1 "github.com/syss-io/executor/gen/proto/go/executor/v1"
)

func (s *ServiceImpl) PromptSession(ctx context.Context, stream *connect.BidiStream[executorv1.PromptSessionRequest, executorv1.PromptSessionResponse]) error {
	return nil
}
