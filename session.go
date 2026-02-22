package agentd

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

type Session struct {
	id     string
	stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]

	mu           sync.Mutex
	pendingTools map[string]chan *agentdv1.RunRequest_ToolCallResponse
}

// NewSession handles a single Run bidi stream. It expects the first message to
// be an ExecuteRequest, then enters the main read loop dispatching each request
// variant until the stream closes or the client sends an EndRequest.
func NewSession(ctx context.Context, stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]) error {
	req, err := stream.Receive()
	if err != nil {
		return err
	}

	exec := req.GetExecute()
	if exec == nil {
		if sendErr := sendError(stream, "", agentdv1.ErrorCode_ERROR_CODE_INTERNAL, "first message must be ExecuteRequest"); sendErr != nil {
			return sendErr
		}
		return errors.New("first message was not ExecuteRequest")
	}

	s := &Session{
		id:           uuid.New().String(),
		stream:       stream,
		pendingTools: make(map[string]chan *agentdv1.RunRequest_ToolCallResponse),
	}

	if err := stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_Execute{
			Execute: &agentdv1.RunResponse_ExecuteResponse{
				SessionId: s.id,
			},
		},
	}); err != nil {
		return err
	}

	return s.loop(ctx)
}

func (s *Session) loop(ctx context.Context) error {
	defer s.closeAllPending()

	for {
		req, err := s.stream.Receive()
		if err != nil {
			return nil
		}

		switch r := req.GetRequest().(type) {
		case *agentdv1.RunRequest_Execute:
			if err := sendError(s.stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INTERNAL, "ExecuteRequest only valid as first message"); err != nil {
				return err
			}

		case *agentdv1.RunRequest_Heartbeat:
			if err := s.handleHeartbeat(r.Heartbeat); err != nil {
				return err
			}

		case *agentdv1.RunRequest_ToolCallResponse_:
			s.handleToolCallResponse(r.ToolCallResponse)

		case *agentdv1.RunRequest_Cancel:
			if err := s.handleCancel(r.Cancel); err != nil {
				return err
			}

		case *agentdv1.RunRequest_End:
			return s.handleEnd(r.End)

		default:
			if err := sendError(s.stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INTERNAL, fmt.Sprintf("unknown request type: %T", r)); err != nil {
				return err
			}
		}
	}
}

func (s *Session) handleHeartbeat(_ *agentdv1.RunRequest_HeartbeatRequest) error {
	return s.stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_Heartbeat{
			Heartbeat: &agentdv1.RunResponse_HeartbeatResponse{
				SessionId: s.id,
			},
		},
	})
}

func (s *Session) handleToolCallResponse(resp *agentdv1.RunRequest_ToolCallResponse) {
	s.mu.Lock()
	ch, ok := s.pendingTools[resp.GetToolCallId()]
	if ok {
		delete(s.pendingTools, resp.GetToolCallId())
	}
	s.mu.Unlock()

	if ok {
		ch <- resp
	}
}

func (s *Session) handleCancel(_ *agentdv1.RunRequest_CancelRequest) error {
	return s.stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_End{
			End: &agentdv1.RunResponse_EndResponse{
				SessionId: s.id,
				Completed: false,
			},
		},
	})
}

func (s *Session) handleEnd(_ *agentdv1.RunRequest_EndRequest) error {
	return s.stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_End{
			End: &agentdv1.RunResponse_EndResponse{
				SessionId: s.id,
				Completed: false,
			},
		},
	})
}

// DispatchToolCall sends a ToolCallRequest to the client and blocks until the
// matching ToolCallResponse arrives or the context is cancelled.
func (s *Session) DispatchToolCall(ctx context.Context, toolCallID, toolName, toolInput string, agentPath []string) (*agentdv1.RunRequest_ToolCallResponse, error) {
	ch := make(chan *agentdv1.RunRequest_ToolCallResponse, 1)

	s.mu.Lock()
	s.pendingTools[toolCallID] = ch
	s.mu.Unlock()

	if err := s.stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_ToolCall{
			ToolCall: &agentdv1.RunResponse_ToolCallRequest{
				SessionId:  s.id,
				ToolCallId: toolCallID,
				ToolName:   toolName,
				ToolInput:  toolInput,
				AgentPath:  agentPath,
			},
		},
	}); err != nil {
		s.mu.Lock()
		delete(s.pendingTools, toolCallID)
		s.mu.Unlock()
		return nil, fmt.Errorf("sending tool call request: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, errors.New("session closed while waiting for tool call response")
		}
		return resp, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pendingTools, toolCallID)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Session) closeAllPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.pendingTools {
		close(ch)
		delete(s.pendingTools, id)
	}
}

func sendError(stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse], sessionID string, code agentdv1.ErrorCode, msg string) error {
	return stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_Error{
			Error: &agentdv1.RunResponse_ErrorResponse{
				SessionId: sessionID,
				Code:      code,
				Message:   msg,
				Retryable: false,
			},
		},
	})
}
