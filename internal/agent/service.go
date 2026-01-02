package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	agentv1 "github.com/syss-io/executor/gen/proto/go/agent/v1"
	"github.com/syss-io/executor/internal/llmprovider"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

type ExecutionLogger interface {
	LogEvent(ctx context.Context, sessionID string, eventType string, eventData any) error
}

type ServiceImpl struct {
	Logger ExecutionLogger
	DB     *gorm.DB
}

type agentSessionState struct {
	mu         sync.Mutex
	toolsQueue map[string]chan *agentv1.StartSessionRequest_ToolCallResponse
	sessionID  string
	model      agentv1.Model
	usageLog   []*agentv1.StartSessionResponse_UsageEntry
	budget     float64 // Session budget in USD (0 = unlimited)
}

func (s *ServiceImpl) NewAgentSession(ctx context.Context, stream *connect.BidiStream[agentv1.StartSessionRequest, agentv1.StartSessionResponse]) error {
	state := &agentSessionState{
		toolsQueue: make(map[string]chan *agentv1.StartSessionRequest_ToolCallResponse),
		model:      agentv1.Model_MODEL_GEMINI_2_5_PRO, // default, will be updated from request
	}

	// 1. Receive initial RunRequest
	message, err := stream.Receive()
	if err != nil {
		slog.Error("RunBrain: failed to receive initial message", "error", err)
		return connect.NewError(connect.CodeInternal, err)
	}

	runReq := message.GetRunRequest()
	if runReq == nil {
		slog.Error("RunBrain: missing run request")
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("missing run request"))
	}

	// Set budget from request
	state.budget = runReq.GetBudget()

	s.Logger.LogEvent(ctx, state.sessionID, "session_start", &session.Event{})

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		slog.Error("RunBrain: GEMINI_API_KEY not set")
		return connect.NewError(connect.CodeInternal, fmt.Errorf("GEMINI_API_KEY not set"))
	}

	// 2. Initialize Gemini Model
	llm, err := s.initModel(ctx, runReq.GetModel())
	if err != nil {
		return err
	}

	// 3. Setup sub-agents recursively
	subAgents := make([]agent.Agent, 0, len(runReq.GetAgents()))
	for _, a := range runReq.GetAgents() {
		subAgent, err := s.setupAgent(ctx, llm, a, stream, state)
		if err != nil {
			slog.Error("RunBrain: failed to setup agent", "error", err, "agent", a.GetName())
			return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to setup agent %s: %w", a.GetName(), err))
		}
		subAgents = append(subAgents, subAgent)
	}

	slog.Info("RunBrain: all sub-agents setup", "count", len(subAgents))

	// 4. Background goroutine to receive messages (ToolCallResponse, SessionEnd)
	go func() {
		for {
			msg, err := stream.Receive()
			if err != nil {
				if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "body closed by handler") {
					// Expected connection termination
					return
				}
				slog.Error("RunBrain: message receiver error", "error", err)

				state.mu.Lock()
				sid := state.sessionID
				state.mu.Unlock()

				if sid != "" && s.Logger != nil {
					// Log as an error event if possible, or just skip if no appropriate event type
					// For now, we just slog it as an error above.
				}
				return
			}

			switch msg.Message.(type) {
			case *agentv1.StartSessionRequest_ToolCallResponse_:
				toolResp := msg.GetToolCallResponse()
				if toolResp == nil {
					continue
				}

				state.mu.Lock()
				ch, ok := state.toolsQueue[toolResp.GetRequestId()]
				state.mu.Unlock()

				if ok {
					slog.Debug("RunBrain: routing tool call response", "request_id", toolResp.GetRequestId())
					ch <- toolResp
				} else {
					slog.Warn("RunBrain: received response for unknown tool call", "request_id", toolResp.GetRequestId())
				}

			case *agentv1.StartSessionRequest_SessionEnd_:
				slog.Info("RunBrain: session end requested by client", "reason", msg.GetSessionEnd().GetReason())
				// We don't necessarily terminate here if we are still processing,
				// but we could signal cancellation if needed.
			}
		}
	}()

	// 5. Create Root Agent
	var rootAgent agent.Agent
	switch runReq.GetWorkflow() {
	case agentv1.StartSessionRequest_RunRequest_WORKFLOW_SEQUENTIAL:
		rootAgent, err = sequentialagent.New(sequentialagent.Config{
			AgentConfig: agent.Config{
				Name:        "root_agent",
				Description: runReq.GetInstruction(),
				SubAgents:   subAgents,
			},
		})
	case agentv1.StartSessionRequest_RunRequest_WORKFLOW_LOOP:
		rootAgent, err = loopagent.New(loopagent.Config{
			AgentConfig: agent.Config{
				Name:        "root_agent",
				Description: runReq.GetInstruction(),
				SubAgents:   subAgents,
			},
			MaxIterations: uint(runReq.GetMaxIterations()),
		})
	case agentv1.StartSessionRequest_RunRequest_WORKFLOW_PARALLEL:
		rootAgent, err = parallelagent.New(parallelagent.Config{
			AgentConfig: agent.Config{
				Name:        "root_agent",
				Description: runReq.GetInstruction(),
				SubAgents:   subAgents,
			},
		})
	default:
		rootAgent, err = llmagent.New(llmagent.Config{
			Model:       llm,
			Name:        "root_agent",
			Instruction: runReq.GetInstruction(),
			SubAgents:   subAgents,
		})
	}

	if err != nil {
		slog.Error("RunBrain: failed to create root agent", "error", err, "workflow", runReq.GetWorkflow())
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create root agent: %w", err))
	}

	// 6. Create Runner
	sessService := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "executor",
		Agent:          rootAgent,
		SessionService: sessService,
	})
	if err != nil {
		slog.Error("RunBrain: failed to create runner", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create runner: %w", err))
	}

	// 7. Initialize session
	sessionID := uuid.New().String()
	state.mu.Lock()
	state.sessionID = sessionID
	state.mu.Unlock()

	_, err = sessService.Create(ctx, &session.CreateRequest{
		AppName:   "executor",
		UserID:    "user",
		SessionID: sessionID,
	})
	if err != nil {
		slog.Error("RunBrain: failed to create session", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create session: %w", err))
	}

	// 8. Run the agent and stream responses
	userMsg := &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			{Text: runReq.GetUserMessage()},
		},
	}

	for event, err := range r.Run(ctx, "user", sessionID, userMsg, agent.RunConfig{}) {
		if err != nil {
			slog.Error("RunBrain: error during run", "error", err)
			return connect.NewError(connect.CodeInternal, fmt.Errorf("error during run: %w", err))
		}

		if s.Logger != nil {
			if err := s.Logger.LogEvent(ctx, sessionID, "event", event); err != nil {
				slog.Error("RunBrain: failed to log event", "error", err, "session_id", sessionID)
			}
		}

		if event.UsageMetadata != nil {
			inTokens := int32(event.UsageMetadata.PromptTokenCount)
			outTokens := int32(event.UsageMetadata.CandidatesTokenCount)

			// Get current model for cost calculation
			state.mu.Lock()
			currentModel := state.model
			state.mu.Unlock()

			// Calculate cost using llmprovider
			eventInputCost, eventOutputCost := llmprovider.CalculateCostBreakdown(currentModel, inTokens, outTokens)

			// Create usage entry for this event
			usageEntry := &agentv1.StartSessionResponse_UsageEntry{
				AgentName:    event.Author,
				Model:        llmprovider.GetModelID(currentModel),
				InputTokens:  inTokens,
				OutputTokens: outTokens,
				InputCost:    eventInputCost,
				OutputCost:   eventOutputCost,
				TimestampMs:  time.Now().UnixMilli(),
			}

			state.mu.Lock()
			state.usageLog = append(state.usageLog, usageEntry)
			// Check budget while holding the lock
			exceeded, totalCost := false, 0.0
			if state.budget > 0 {
				for _, entry := range state.usageLog {
					totalCost += entry.InputCost + entry.OutputCost
				}
				exceeded = totalCost > state.budget
			}
			budgetLimit := state.budget
			state.mu.Unlock()

			// Handle budget exceeded outside the lock
			if exceeded {
				slog.Warn("RunBrain: session budget exceeded",
					"budget", budgetLimit,
					"total_cost", totalCost,
					"session_id", state.sessionID)

				// Send SessionEnd with REASON_BUDGET
				if err := stream.Send(&agentv1.StartSessionResponse{
					Message: &agentv1.StartSessionResponse_SessionEnd_{
						SessionEnd: &agentv1.StartSessionResponse_SessionEnd{
							Reason:  agentv1.StartSessionResponse_SessionEnd_REASON_BUDGET,
							Message: fmt.Sprintf("Budget exceeded: $%.4f > $%.4f", totalCost, budgetLimit),
						},
					},
				}); err != nil {
					slog.Error("RunBrain: failed to send budget exceeded", "error", err)
				}

				return nil // Gracefully terminate the session
			}
		}

		if event.Content != nil {
			var text string
			for _, part := range event.Content.Parts {
				if part.Text != "" {
					text += part.Text
				}
			}

			if text != "" {
				state.mu.Lock()
				// Copy usage log and compute totals
				usageLogCopy := make([]*agentv1.StartSessionResponse_UsageEntry, len(state.usageLog))
				copy(usageLogCopy, state.usageLog)
				state.mu.Unlock()

				// Compute totals from usage log
				var inTokens, outTokens int32
				var inCost, outCost float64
				for _, entry := range usageLogCopy {
					inTokens += entry.InputTokens
					outTokens += entry.OutputTokens
					inCost += entry.InputCost
					outCost += entry.OutputCost
				}

				if err := stream.Send(&agentv1.StartSessionResponse{
					Message: &agentv1.StartSessionResponse_RunResponse_{
						RunResponse: &agentv1.StartSessionResponse_RunResponse{
							Content:          text,
							InputTokenCount:  inTokens,
							OutputTokenCount: outTokens,
							InputCost:        inCost,
							OutputCost:       outCost,
							TotalCost:        inCost + outCost,
						},
					},
				}); err != nil {
					slog.Error("RunBrain: failed to send RunResponse", "error", err)
					return connect.NewError(connect.CodeInternal, err)
				}
			}
		}
	}

	// 9. Send SessionEndAck
	if err := stream.Send(&agentv1.StartSessionResponse{
		Message: &agentv1.StartSessionResponse_SessionEndAck_{
			SessionEndAck: &agentv1.StartSessionResponse_SessionEndAck{
				Acknowledged: true,
			},
		},
	}); err != nil {
		slog.Error("RunBrain: failed to send session end ack", "error", err)
	}

	return nil
}

func (s *ServiceImpl) setupAgent(ctx context.Context, llm model.LLM, a *agentv1.StartSessionRequest_Agent, stream *connect.BidiStream[agentv1.StartSessionRequest, agentv1.StartSessionResponse], state *agentSessionState) (agent.Agent, error) {
	agentLLM := llm
	if a.GetModel() != agentv1.Model_MODEL_UNSPECIFIED {
		var err error
		agentLLM, err = s.initModel(ctx, a.GetModel())
		if err != nil {
			return nil, err
		}
	}

	cfg := llmagent.Config{
		Model:       agentLLM,
		Name:        a.GetName(),
		Instruction: a.GetInstruction(),
	}

	for _, t := range a.GetTools() {
		toolInstance, err := s.createTool(t, stream, state)
		if err != nil {
			return nil, err
		}
		cfg.Tools = append(cfg.Tools, toolInstance)
	}

	for _, subA := range a.GetSubAgents() {
		subAgent, err := s.setupAgent(ctx, llm, subA, stream, state)
		if err != nil {
			return nil, err
		}
		cfg.SubAgents = append(cfg.SubAgents, subAgent)
	}

	return llmagent.New(cfg)
}

func (s *ServiceImpl) createTool(t *agentv1.StartSessionRequest_Agent_Tool, stream *connect.BidiStream[agentv1.StartSessionRequest, agentv1.StartSessionResponse], state *agentSessionState) (tool.Tool, error) {
	cfg := functiontool.Config{
		Name:        t.GetName(),
		Description: t.GetDescription(),
	}

	if t.GetInputSchema() != "" {
		var inputSchema jsonschema.Schema
		if err := json.Unmarshal([]byte(t.GetInputSchema()), &inputSchema); err != nil {
			return nil, fmt.Errorf("failed to unmarshal input schema for tool %s: %w", t.GetName(), err)
		}
		// ADK/Gemini rejects bare {"type":"object"} with no properties
		if len(inputSchema.Properties) > 0 {
			cfg.InputSchema = &inputSchema
		} else {
			cfg.InputSchema = &jsonschema.Schema{}
		}
	} else {
		cfg.InputSchema = &jsonschema.Schema{}
	}

	if t.GetOutputSchema() != "" {
		var outputSchema jsonschema.Schema
		if err := json.Unmarshal([]byte(t.GetOutputSchema()), &outputSchema); err != nil {
			return nil, fmt.Errorf("failed to unmarshal output schema for tool %s: %w", t.GetName(), err)
		}
		cfg.OutputSchema = &outputSchema
	}

	return functiontool.New(cfg, func(ctx tool.Context, input map[string]any) (map[string]any, error) {
		inputJSON, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal tool input: %w", err)
		}

		requestID := uuid.New().String()
		respCh := make(chan *agentv1.StartSessionRequest_ToolCallResponse, 1)

		state.mu.Lock()
		state.toolsQueue[requestID] = respCh
		state.mu.Unlock()

		defer func() {
			state.mu.Lock()
			delete(state.toolsQueue, requestID)
			state.mu.Unlock()
		}()

		// Send ToolCallRequest to client
		if err := stream.Send(&agentv1.StartSessionResponse{
			Message: &agentv1.StartSessionResponse_ToolCallRequest_{
				ToolCallRequest: &agentv1.StartSessionResponse_ToolCallRequest{
					RequestId: requestID,
					ToolName:  t.GetName(),
					Input:     string(inputJSON),
				},
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to send tool call request: %w", err)
		}

		select {
		case response := <-respCh:
			if response.GetStatus() != agentv1.StartSessionRequest_ToolCallResponse_STATUS_SUCCESS {
				return nil, fmt.Errorf("tool execution failed on client: %s", response.GetError())
			}

			var output map[string]any
			if err := json.Unmarshal([]byte(response.GetOutput()), &output); err != nil {
				return nil, fmt.Errorf("failed to unmarshal tool call response: %w", err)
			}
			return output, nil

		case <-time.After(3 * time.Minute):
			return nil, fmt.Errorf("tool call response timeout after 3 minutes")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
}

func (s *ServiceImpl) initModel(ctx context.Context, m agentv1.Model) (model.LLM, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		slog.Error("initModel: GEMINI_API_KEY not set")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("GEMINI_API_KEY not set"))
	}

	modelName := getModelName(m)
	llm, err := gemini.NewModel(ctx, modelName, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		slog.Error("initModel: failed to create model", "error", err, "model", modelName)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create model %s: %w", modelName, err))
	}
	return llm, nil
}

func getModelName(m agentv1.Model) string {
	switch m {
	case agentv1.Model_MODEL_GEMINI_2_5_PRO:
		return "gemini-2.5-pro"
	case agentv1.Model_MODEL_GEMINI_2_5_FLASH:
		return "gemini-2.5-flash"
	case agentv1.Model_MODEL_GEMINI_2_5_FLASH_LITE:
		return "gemini-2.5-flash-lite"
	case agentv1.Model_MODEL_GEMINI_3_PRO:
		return "gemini-3.0-pro-preview"
	case agentv1.Model_MODEL_GEMINI_3_FLASH:
		return "gemini-3-flash-preview"
	default:
		return "gemini-2.5-pro"
	}
}
