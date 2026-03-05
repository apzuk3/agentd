package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"connectrpc.com/connect"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	adksession "google.golang.org/adk/session"
	"google.golang.org/genai"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

type Session struct {
	id              string
	geminiAPIKey    string
	anthropicAPIKey string
	openaiAPIKey    string
	tavilyAPIKey    string
	stream          *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]
	log             *slog.Logger

	mu           sync.Mutex
	pendingTools map[string]chan *agentdv1.RunRequest_ToolCallResponse

	cancel     context.CancelFunc
	agentPaths map[string][]string
	usage      usageSummary
}

type usageSummary struct {
	promptTokens     atomic.Int32
	completionTokens atomic.Int32
	cachedTokens     atomic.Int32
	thoughtsTokens   atomic.Int32
	totalTokens      atomic.Int32
	llmCalls         atomic.Int32
}

// NewSession handles a single Run bidi stream. It expects the first message to
// be an ExecuteRequest, then builds the ADK agent tree and runs the agent loop
// concurrently with the client message read loop.
func NewSession(ctx context.Context, stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse], opts ...SessionOption) error {
	req, err := stream.Receive()
	if err != nil {
		slog.ErrorContext(ctx, "failed to receive initial message", "error", err)
		return err
	}

	exec := req.GetExecute()
	if exec == nil {
		slog.WarnContext(ctx, "first message was not ExecuteRequest")
		if sendErr := sendError(stream, "", agentdv1.ErrorCode_ERROR_CODE_INTERNAL, "first message must be ExecuteRequest"); sendErr != nil {
			return sendErr
		}
		return errors.New("first message was not ExecuteRequest")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s := &Session{
		stream:       stream,
		log:          slog.Default(),
		pendingTools: make(map[string]chan *agentdv1.RunRequest_ToolCallResponse),
		cancel:       cancel,
		agentPaths:   make(map[string][]string),
	}

	for _, opt := range opts {
		opt(s)
	}

	sessionService := adksession.InMemoryService()

	adkSession, err := sessionService.Create(runCtx, &adksession.CreateRequest{
		AppName:   "agentd",
		UserID:    "user",
		SessionID: exec.GetSessionId(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create ADK session", "error", err)
		return fmt.Errorf("creating ADK session: %w", err)
	}

	s.id = adkSession.Session.ID()
	s.log = s.log.With("session_id", s.id)

	s.log.InfoContext(ctx, "session created")

	if err := stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_Execute{
			Execute: &agentdv1.RunResponse_ExecuteResponse{
				SessionId: s.id,
			},
		},
	}); err != nil {
		s.log.ErrorContext(ctx, "failed to send execute response", "error", err)
		return err
	}

	if exec.GetAgent() == nil {
		s.log.ErrorContext(ctx, "agent definition is missing")
		if sendErr := sendError(stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INVALID_AGENT_TREE, "agent definition is required"); sendErr != nil {
			return sendErr
		}
		return errors.New("agent definition is required")
	}

	toolCatalog := make(map[string]*agentdv1.Tool, len(exec.GetTools()))
	for _, t := range exec.GetTools() {
		toolCatalog[t.GetName()] = t
	}

	s.log.InfoContext(ctx, "building agent tree", "root_agent", exec.GetAgent().GetName(), "tool_count", len(toolCatalog))

	builtinCfg := &BuiltinToolConfig{
		TavilyAPIKey: s.tavilyAPIKey,
	}

	rootAgent, err := createAgent(runCtx, exec.GetAgent(), s, s.geminiAPIKey, s.anthropicAPIKey, s.openaiAPIKey, nil, s.agentPaths, toolCatalog, builtinCfg)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to build agent tree", "error", err)
		if sendErr := sendError(stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INVALID_AGENT_TREE, err.Error()); sendErr != nil {
			return sendErr
		}
		return fmt.Errorf("building agent tree: %w", err)
	}

	s.log.InfoContext(ctx, "agent tree built", "agent_count", len(s.agentPaths))

	if err := s.sendStateSnapshot(adkSession.Session.State()); err != nil {
		s.log.ErrorContext(ctx, "failed to send initial state", "error", err)
		return fmt.Errorf("sending initial state: %w", err)
	}

	r, err := runner.New(runner.Config{
		AppName:        "agentd",
		Agent:          rootAgent,
		SessionService: sessionService,
	})
	if err != nil {
		s.log.ErrorContext(ctx, "failed to create runner", "error", err)
		return fmt.Errorf("creating runner: %w", err)
	}

	s.log.InfoContext(ctx, "starting agent run", "user_prompt_len", len(exec.GetUserPrompt()))

	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- s.runAgent(runCtx, r, s.id, exec.GetUserPrompt())
	}()

	loopErr := s.loop(runCtx)

	cancel()
	runnerErr := <-runnerDone

	s.log.InfoContext(ctx, "session ended",
		"prompt_tokens", s.usage.promptTokens.Load(),
		"completion_tokens", s.usage.completionTokens.Load(),
		"cached_tokens", s.usage.cachedTokens.Load(),
		"thoughts_tokens", s.usage.thoughtsTokens.Load(),
		"total_tokens", s.usage.totalTokens.Load(),
		"llm_calls", s.usage.llmCalls.Load(),
		"loop_error", loopErr,
		"runner_error", runnerErr,
	)

	if loopErr != nil {
		return loopErr
	}
	return runnerErr
}

// runAgent drives the ADK runner, iterating over events and streaming
// OutputChunks back to the client. Sends EndResponse when complete.
func (s *Session) runAgent(ctx context.Context, r *runner.Runner, adkSessionID, userPrompt string) error {
	userContent := genai.NewContentFromText(userPrompt, "user")

	cfg := agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}

	s.log.DebugContext(ctx, "runner loop started", "adk_session_id", adkSessionID)

	var lastErr error
	for event, err := range r.Run(ctx, "user", adkSessionID, userContent, cfg) {
		if err != nil {
			if ctx.Err() != nil {
				s.log.InfoContext(ctx, "runner loop cancelled")
				break
			}
			s.log.ErrorContext(ctx, "runner event error", "error", err)
			lastErr = err
			continue
		}
		if event == nil {
			continue
		}

		if event.UsageMetadata != nil {
			s.usage.promptTokens.Add(int32(event.UsageMetadata.PromptTokenCount))
			s.usage.completionTokens.Add(int32(event.UsageMetadata.CandidatesTokenCount))
			s.usage.cachedTokens.Add(int32(event.UsageMetadata.CachedContentTokenCount))
			s.usage.thoughtsTokens.Add(int32(event.UsageMetadata.ThoughtsTokenCount))
			s.usage.totalTokens.Add(int32(event.UsageMetadata.TotalTokenCount))
			s.usage.llmCalls.Add(1)

			s.log.DebugContext(ctx, "usage recorded",
				"prompt_tokens", event.UsageMetadata.PromptTokenCount,
				"completion_tokens", event.UsageMetadata.CandidatesTokenCount,
				"total_tokens", event.UsageMetadata.TotalTokenCount,
			)
		}

		if len(event.Actions.StateDelta) > 0 {
			if err := s.sendStateDelta(event.Actions.StateDelta); err != nil {
				s.log.ErrorContext(ctx, "failed to send state delta", "error", err)
				return fmt.Errorf("sending state delta: %w", err)
			}
		}

		if event.Content == nil || len(event.Content.Parts) == 0 {
			continue
		}

		agentPath := s.agentPaths[event.Author]
		if agentPath == nil {
			agentPath = []string{event.Author}
		}

		for _, part := range event.Content.Parts {
			if part.Text == "" {
				continue
			}
			if part.FunctionCall != nil || part.FunctionResponse != nil {
				continue
			}

			isFinal := !event.Partial && event.IsFinalResponse()

			s.log.DebugContext(ctx, "sending output chunk",
				"agent", event.Author,
				"is_thought", part.Thought,
				"is_final", isFinal,
				"content_len", len(part.Text),
			)

			if err := s.stream.Send(&agentdv1.RunResponse{
				Response: &agentdv1.RunResponse_OutputChunk_{
					OutputChunk: &agentdv1.RunResponse_OutputChunk{
						SessionId: s.id,
						AgentPath: agentPath,
						Content:   part.Text,
						Last:      isFinal,
						IsThought: part.Thought,
					},
				},
			}); err != nil {
				s.log.ErrorContext(ctx, "failed to send output chunk", "error", err)
				return fmt.Errorf("sending output chunk: %w", err)
			}
		}
	}

	if lastErr != nil {
		s.log.ErrorContext(ctx, "runner completed with error", "error", lastErr)
		_ = sendError(s.stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INTERNAL, lastErr.Error())
	} else {
		s.log.InfoContext(ctx, "runner completed successfully")
	}

	return s.stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_End{
			End: &agentdv1.RunResponse_EndResponse{
				SessionId: s.id,
				Completed: lastErr == nil,
				UsageSummary: &agentdv1.UsageSummary{
					TotalUsage: &agentdv1.TokenUsage{
						PromptTokens:     s.usage.promptTokens.Load(),
						CompletionTokens: s.usage.completionTokens.Load(),
						CachedTokens:     s.usage.cachedTokens.Load(),
						ThoughtsTokens:   s.usage.thoughtsTokens.Load(),
						TotalTokens:      s.usage.totalTokens.Load(),
					},
					LlmCalls: s.usage.llmCalls.Load(),
				},
			},
		},
	})
}

func (s *Session) loop(ctx context.Context) error {
	defer s.closeAllPending()

	s.log.DebugContext(ctx, "message loop started")

	for {
		req, err := s.stream.Receive()
		if err != nil {
			s.log.DebugContext(ctx, "message loop ended", "error", err)
			return nil
		}

		switch r := req.GetRequest().(type) {
		case *agentdv1.RunRequest_Execute:
			s.log.WarnContext(ctx, "received ExecuteRequest after session start")
			if err := sendError(s.stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INTERNAL, "ExecuteRequest only valid as first message"); err != nil {
				return err
			}

		case *agentdv1.RunRequest_Heartbeat:
			s.log.DebugContext(ctx, "received heartbeat")
			if err := s.handleHeartbeat(r.Heartbeat); err != nil {
				return err
			}

		case *agentdv1.RunRequest_ToolCallResponse_:
			s.log.DebugContext(ctx, "received tool call response", "tool_call_id", r.ToolCallResponse.GetToolCallId())
			s.handleToolCallResponse(r.ToolCallResponse)

		case *agentdv1.RunRequest_Cancel:
			s.log.InfoContext(ctx, "received cancel request")
			s.handleCancel(r.Cancel)

		case *agentdv1.RunRequest_End:
			s.log.InfoContext(ctx, "received end request")
			return s.handleEnd(r.End)

		default:
			s.log.WarnContext(ctx, "received unknown request type", "type", fmt.Sprintf("%T", r))
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

// handleCancel cancels the runner context. The runner goroutine will observe
// the cancellation and wind down.
func (s *Session) handleCancel(_ *agentdv1.RunRequest_CancelRequest) {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Session) handleEnd(_ *agentdv1.RunRequest_EndRequest) error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

// DispatchToolCall sends a ToolCallRequest to the client and blocks until the
// matching ToolCallResponse arrives or the context is cancelled.
func (s *Session) DispatchToolCall(ctx context.Context, toolCallID, toolName, toolInput string, agentPath []string) (*agentdv1.RunRequest_ToolCallResponse, error) {
	s.log.InfoContext(ctx, "dispatching tool call",
		"tool_call_id", toolCallID,
		"tool_name", toolName,
		"agent_path", agentPath,
		"input_len", len(toolInput),
	)

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
		s.log.ErrorContext(ctx, "failed to send tool call request", "tool_call_id", toolCallID, "error", err)
		return nil, fmt.Errorf("sending tool call request: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			s.log.WarnContext(ctx, "session closed while waiting for tool call response", "tool_call_id", toolCallID)
			return nil, errors.New("session closed while waiting for tool call response")
		}
		s.log.InfoContext(ctx, "tool call response received", "tool_call_id", toolCallID, "tool_name", toolName)
		return resp, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pendingTools, toolCallID)
		s.mu.Unlock()
		s.log.WarnContext(ctx, "tool call cancelled", "tool_call_id", toolCallID, "tool_name", toolName)
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

func serializeStateValue(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Session) sendStateSnapshot(state adksession.ReadonlyState) error {
	m := make(map[string]string)
	for k, v := range state.All() {
		encoded, err := serializeStateValue(v)
		if err != nil {
			s.log.Warn("skipping unserializable state key", "key", k, "error", err)
			continue
		}
		m[k] = encoded
	}
	if len(m) == 0 {
		return nil
	}
	return s.stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_StateUpdate_{
			StateUpdate: &agentdv1.RunResponse_StateUpdate{
				SessionId: s.id,
				State:     m,
			},
		},
	})
}

func (s *Session) sendStateDelta(delta map[string]any) error {
	m := make(map[string]string, len(delta))
	for k, v := range delta {
		encoded, err := serializeStateValue(v)
		if err != nil {
			s.log.Warn("skipping unserializable state delta key", "key", k, "error", err)
			continue
		}
		m[k] = encoded
	}
	if len(m) == 0 {
		return nil
	}
	return s.stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_StateUpdate_{
			StateUpdate: &agentdv1.RunResponse_StateUpdate{
				SessionId: s.id,
				State:     m,
			},
		},
	})
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
