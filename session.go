package agentd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"connectrpc.com/connect"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	adksession "google.golang.org/adk/session"
	"google.golang.org/genai"
	"google.golang.org/protobuf/types/known/structpb"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

type Session struct {
	id              string
	geminiAPIKey    string
	anthropicAPIKey string
	openaiAPIKey    string
	tavilyAPIKey    string
	stream          *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]
	plugins         *PluginChain

	mu           sync.Mutex
	pendingTools map[string]chan *agentdv1.RunRequest_ToolCallResponse

	cancel     context.CancelFunc
	agentPaths map[string][]string
	usage      usageSummary
}

// currentUsage returns a snapshot of the cumulative token usage.
func (s *Session) currentUsage() UsageInfo {
	return UsageInfo{
		PromptTokens:     s.usage.promptTokens.Load(),
		CompletionTokens: s.usage.completionTokens.Load(),
		CachedTokens:     s.usage.cachedTokens.Load(),
		ThoughtsTokens:   s.usage.thoughtsTokens.Load(),
		TotalTokens:      s.usage.totalTokens.Load(),
		LLMCalls:         s.usage.llmCalls.Load(),
	}
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
		return err
	}

	exec := req.GetExecute()
	if exec == nil {
		if sendErr := sendError(stream, "", agentdv1.ErrorCode_ERROR_CODE_INTERNAL, "first message must be ExecuteRequest"); sendErr != nil {
			return sendErr
		}
		return errors.New("first message was not ExecuteRequest")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s := &Session{
		stream:       stream,
		plugins:      NewPluginChain(),
		pendingTools: make(map[string]chan *agentdv1.RunRequest_ToolCallResponse),
		cancel:       cancel,
		agentPaths:   make(map[string][]string),
	}

	for _, opt := range opts {
		opt(s)
	}

	sessionService := adksession.InMemoryService()

	var initialState map[string]any
	if s := exec.GetInitialState(); s != nil {
		initialState = s.AsMap()
	}

	adkSession, err := sessionService.Create(runCtx, &adksession.CreateRequest{
		AppName:   "agentd",
		UserID:    "user",
		SessionID: exec.GetSessionId(),
		State:     initialState,
	})
	if err != nil {
		return fmt.Errorf("creating ADK session: %w", err)
	}

	s.id = adkSession.Session.ID()

	if err := s.plugins.OnSessionStart(ctx, SessionStartInfo{
		SessionID:  s.id,
		RootAgent:  exec.GetAgent().GetName(),
		ToolCount:  len(exec.GetTools()),
		UserPrompt: exec.GetUserPrompt(),
	}); err != nil {
		return fmt.Errorf("plugin rejected session start: %w", err)
	}

	if err := stream.Send(&agentdv1.RunResponse{
		Response: &agentdv1.RunResponse_Execute{
			Execute: &agentdv1.RunResponse_ExecuteResponse{
				SessionId: s.id,
			},
		},
	}); err != nil {
		s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "failed to send execute response", Err: err})
		return err
	}

	if exec.GetAgent() == nil {
		s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "agent definition is missing"})
		if sendErr := sendError(stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INVALID_AGENT_TREE, "agent definition is required"); sendErr != nil {
			return sendErr
		}
		return errors.New("agent definition is required")
	}

	toolCatalog := make(map[string]*agentdv1.Tool, len(exec.GetTools()))
	for _, t := range exec.GetTools() {
		toolCatalog[t.GetName()] = t
	}

	builtinCfg := &BuiltinToolConfig{
		TavilyAPIKey: s.tavilyAPIKey,
	}

	rootAgent, err := createAgent(runCtx, exec.GetAgent(), s, s.geminiAPIKey, s.anthropicAPIKey, s.openaiAPIKey, nil, s.agentPaths, toolCatalog, builtinCfg)
	if err != nil {
		s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "failed to build agent tree", Err: err})
		if sendErr := sendError(stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INVALID_AGENT_TREE, err.Error()); sendErr != nil {
			return sendErr
		}
		return fmt.Errorf("building agent tree: %w", err)
	}

	runnerCfg := runner.Config{
		AppName:        "agentd",
		Agent:          rootAgent,
		SessionService: sessionService,
	}

	p, pluginErr := newSessionPluginBridge(s.id, s.plugins, s.currentUsage)
	if pluginErr != nil {
		s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "failed to create plugin bridge", Err: pluginErr})
		return fmt.Errorf("creating plugin bridge: %w", pluginErr)
	}
	runnerCfg.PluginConfig = runner.PluginConfig{
		Plugins: []*plugin.Plugin{p},
	}

	r, err := runner.New(runnerCfg)
	if err != nil {
		s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "failed to create runner", Err: err})
		return fmt.Errorf("creating runner: %w", err)
	}

	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- s.runAgent(runCtx, r, s.id, exec.GetUserPrompt())
	}()

	loopErr := s.loop(runCtx)

	cancel()
	runnerErr := <-runnerDone

	var sessionErr error
	if loopErr != nil {
		sessionErr = loopErr
	} else {
		sessionErr = runnerErr
	}

	s.plugins.OnSessionEnd(ctx, SessionEndInfo{
		SessionID: s.id,
		Usage:     s.currentUsage(),
		Err:       sessionErr,
	})

	return sessionErr
}

// runAgent drives the ADK runner, iterating over events and streaming
// OutputChunks back to the client. Sends EndResponse when complete.
func (s *Session) runAgent(ctx context.Context, r *runner.Runner, adkSessionID, userPrompt string) error {
	userContent := genai.NewContentFromText(userPrompt, "user")

	cfg := agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}

	var lastErr error
	for event, err := range r.Run(ctx, "user", adkSessionID, userContent, cfg) {
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "runner event error", Err: err})
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
		}

		if len(event.Actions.StateDelta) > 0 {
			if delta, err := structpb.NewStruct(event.Actions.StateDelta); err == nil {
				if err := s.stream.Send(&agentdv1.RunResponse{
					Response: &agentdv1.RunResponse_StateUpdate_{
						StateUpdate: &agentdv1.RunResponse_StateUpdate{
							SessionId:  s.id,
							StateDelta: delta,
						},
					},
				}); err != nil {
					s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "failed to send state update", Err: err})
					return fmt.Errorf("sending state update: %w", err)
				}
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

			s.plugins.OnOutputChunk(ctx, OutputChunkInfo{
				SessionID:  s.id,
				AgentName:  event.Author,
				AgentPath:  agentPath,
				IsThought:  part.Thought,
				IsFinal:    isFinal,
				ContentLen: len(part.Text),
			})

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
				s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "failed to send output chunk", Err: err})
				return fmt.Errorf("sending output chunk: %w", err)
			}
		}
	}

	if lastErr != nil {
		_ = sendError(s.stream, s.id, agentdv1.ErrorCode_ERROR_CODE_INTERNAL, lastErr.Error())
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

	for {
		req, err := s.stream.Receive()
		if err != nil {
			return nil
		}

		switch r := req.GetRequest().(type) {
		case *agentdv1.RunRequest_Execute:
			s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: "received ExecuteRequest after session start"})
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
			s.handleCancel(r.Cancel)

		case *agentdv1.RunRequest_End:
			return s.handleEnd(r.End)

		default:
			s.plugins.OnError(ctx, ErrorInfo{SessionID: s.id, Message: fmt.Sprintf("received unknown request type: %T", r)})
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
	ch := make(chan *agentdv1.RunRequest_ToolCallResponse, 1)

	s.mu.Lock()
	s.pendingTools[toolCallID] = ch
	s.mu.Unlock()

	s.plugins.OnToolDispatched(ctx, ToolDispatchInfo{
		SessionID:  s.id,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		AgentPath:  agentPath,
		InputLen:   len(toolInput),
	})

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
		s.plugins.OnError(ctx, ErrorInfo{
			SessionID: s.id,
			Message:   "failed to send tool call request",
			Err:       err,
		})
		return nil, fmt.Errorf("sending tool call request: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			s.plugins.OnError(ctx, ErrorInfo{
				SessionID: s.id,
				Message:   "session closed while waiting for tool call response",
			})
			return nil, errors.New("session closed while waiting for tool call response")
		}
		s.plugins.OnToolResponse(ctx, ToolResponseInfo{
			SessionID:  s.id,
			ToolCallID: toolCallID,
			ToolName:   toolName,
		})
		return resp, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pendingTools, toolCallID)
		s.mu.Unlock()
		s.plugins.OnError(ctx, ErrorInfo{
			SessionID: s.id,
			Message:   "tool call cancelled",
			Err:       ctx.Err(),
		})
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
