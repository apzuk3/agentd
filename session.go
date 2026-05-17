package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"connectrpc.com/connect"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/runner"
	adksession "google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

type Session struct {
	mu       sync.Mutex
	stream   *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]
	toolcall map[string]chan *agentdv1.RunRequest_ToolcallResult

	onToolCreated      func(name string)
	onToolCreatedError func(name string, error error)
	onToolCall         func(name string, args map[string]any)
	onToolCallResult   func(name string, result map[string]any)
	onToolCallError    func(name string, args map[string]any, error error)
}

type SessionOptions func(*Session) error

func NewSession(stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]) *Session {
	return &Session{
		stream:   stream,
		toolcall: make(map[string]chan *agentdv1.RunRequest_ToolcallResult),
	}
}

func (s *Session) HandleSession(stream *connect.BidiStream[agentdv1.RunRequest, agentdv1.RunResponse]) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			req, err := stream.Receive()
			if err != nil {
				return err
			}

			switch x := req.GetRequest().(type) {
			case *agentdv1.RunRequest_Run_:
				run := x.Run

				tools := make(map[string]tool.Tool)
				for _, t := range run.Tools {
					tool, err := s.CreateTool(ctx, t)
					if err != nil {
						return err
					}
					tools[t.Name] = tool
				}

				rootagent, err := s.createAgent(ctx, run.Agent, tools)
				if err != nil {
					return err
				}

				sessionService := adksession.InMemoryService()

				runnerCfg := runner.Config{
					AppName:        "agentd",
					Agent:          rootagent,
					SessionService: sessionService,
				}

				r, err := runner.New(runnerCfg)
				if err != nil {
					return err
				}

				agentcfg := agent.RunConfig{
					StreamingMode: agent.StreamingModeSSE,
				}

				content := &genai.Content{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						{
							Text: run.UserPrompt,
						},
					},
				}

				iter := r.Run(ctx, "user_prompt", "session_id", content, agentcfg)
				for event := range iter {

					isFinal := !event.Partial && event.IsFinalResponse()

					for _, part := range event.Content.Parts {
						stream.Send(&agentdv1.RunResponse{
							Response: &agentdv1.RunResponse_OutputChunk_{
								OutputChunk: &agentdv1.RunResponse_OutputChunk{
									Content:   part.Text,
									Last:      isFinal,
									IsThought: part.Thought,
								},
							},
						})
					}
				}

			case *agentdv1.RunRequest_Heartbeat_:
				stream.Send(&agentdv1.RunResponse{
					Response: &agentdv1.RunResponse_Heartbeat_{
						Heartbeat: &agentdv1.RunResponse_Heartbeat{},
					},
				})

			case *agentdv1.RunRequest_ToolcallResult_:
				toolcallResult := x.ToolcallResult

				s.mu.Lock()
				ch, ok := s.toolcall[toolcallResult.ToolCallId]
				if ok {
					delete(s.toolcall, toolcallResult.ToolCallId)
				}
				s.mu.Unlock()

				if !ok {
					continue
				}
				select {
				case ch <- toolcallResult:
				default:
				}
				close(ch)
			case *agentdv1.RunRequest_Cancel_:
				cancel()

				s.mu.Lock()
				pending := s.toolcall
				s.toolcall = make(map[string]chan *agentdv1.RunRequest_ToolcallResult)
				s.mu.Unlock()

				for _, ch := range pending {
					select {
					case ch <- &agentdv1.RunRequest_ToolcallResult{
						Error: new("tool call cancelled"),
					}:
					default:
					}
					close(ch)
				}

				if err := stream.Send(&agentdv1.RunResponse{
					Response: &agentdv1.RunResponse_End_{
						End: &agentdv1.RunResponse_End{
							Reason:    new("Client cancelled session"),
							Completed: false,
						},
					},
				}); err != nil {
					return err
				}
				return nil
			}
		}
	}
}

func (s *Session) createAgent(ctx context.Context, agentdAgent *agentdv1.Agent, toolsMap map[string]tool.Tool) (agent.Agent, error) {
	switch x := agentdAgent.Type.(type) {
	case *agentdv1.Agent_Llm:
		return s.createLlmAgent(ctx, x.Llm, toolsMap)
	case *agentdv1.Agent_Sequential:
		return s.createSequentialAgent(ctx, x.Sequential)
	case *agentdv1.Agent_Parallel:
		return s.createParallelAgent(ctx, x.Parallel)
	case *agentdv1.Agent_Loop:
		return s.createLoopAgent(ctx, x.Loop)
	default:
		return nil, fmt.Errorf("unknown agent type: %T", x)
	}
}

func (s *Session) createLlmAgent(ctx context.Context, agentdLlm *agentdv1.LlmAgent, toolsMap map[string]tool.Tool) (agent.Agent, error) {
	subagents := make([]agent.Agent, len(agentdLlm.SubAgents))
	for i, agentdAgent := range agentdLlm.SubAgents {
		subagent, err := s.createAgent(ctx, agentdAgent, toolsMap)
		if err != nil {
			return nil, fmt.Errorf("creating sub-agent %d: %w", i, err)
		}
		subagents[i] = subagent
	}

	tools := make([]tool.Tool, len(agentdLlm.ToolNames))
	for toolIndex, toolName := range agentdLlm.ToolNames {
		tool, ok := toolsMap[toolName]
		if !ok {
			return nil, fmt.Errorf("tool %q not found", toolName)
		}
		tools[toolIndex] = tool
	}

	llmAgent, err := llmagent.New(llmagent.Config{
		Name:        agentdLlm.Name,
		Description: agentdLlm.Description,
		Instruction: agentdLlm.Instruction,
		SubAgents:   subagents,
		Tools:       tools,
		BeforeToolCallbacks: []llmagent.BeforeToolCallback{
			func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
				if s.onToolCall != nil {
					go s.onToolCall(tool.Name(), args)
				}
				return args, nil
			},
		},
		AfterToolCallbacks: []llmagent.AfterToolCallback{
			func(ctx tool.Context, tool tool.Tool, args map[string]any, result map[string]any, err error) (map[string]any, error) {
				if err != nil {
					if s.onToolCallError != nil {
						go s.onToolCallError(tool.Name(), args, err)
					}
					return result, err
				}
				if s.onToolCallResult != nil {
					go s.onToolCallResult(tool.Name(), result)
				}
				return result, nil
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create llm agent: %w", err)
	}
	return llmAgent, nil
}

func (s *Session) createSequentialAgent(ctx context.Context, agentdSequential *agentdv1.SequentialAgent) (agent.Agent, error) {
	subagents := make([]agent.Agent, len(agentdSequential.Agents))
	for i, agentdAgent := range agentdSequential.Agents {
		subagent, err := s.createAgent(ctx, agentdAgent, nil)
		if err != nil {
			return nil, fmt.Errorf("creating sub-agent %d: %w", i, err)
		}
		subagents[i] = subagent
	}

	sequentialAgent, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        "sequential_agent",
			Description: "A sequential agent that runs sub-agents",
			SubAgents:   subagents,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	return sequentialAgent, nil
}

func (s *Session) createParallelAgent(ctx context.Context, agentdParallel *agentdv1.ParallelAgent) (agent.Agent, error) {
	subagents := make([]agent.Agent, len(agentdParallel.Agents))
	for i, agentdAgent := range agentdParallel.Agents {
		subagent, err := s.createAgent(ctx, agentdAgent, nil)
		if err != nil {
			return nil, fmt.Errorf("creating sub-agent %d: %w", i, err)
		}
		subagents[i] = subagent
	}

	parallelAgent, err := parallelagent.New(parallelagent.Config{
		AgentConfig: agent.Config{
			Name:        "parallel_agent",
			Description: "A parallel agent that runs sub-agents",
			SubAgents:   subagents,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}
	return parallelAgent, nil
}

func (s *Session) createLoopAgent(ctx context.Context, agentdLoop *agentdv1.LoopAgent) (agent.Agent, error) {
	subagents := make([]agent.Agent, len(agentdLoop.Agents))
	for i, agentdAgent := range agentdLoop.Agents {
		subagent, err := s.createAgent(ctx, agentdAgent, nil)
		if err != nil {
			return nil, fmt.Errorf("creating sub-agent %d: %w", i, err)
		}
		subagents[i] = subagent
	}

	loopAgent, err := loopagent.New(loopagent.Config{
		AgentConfig: agent.Config{
			Name:        "loop_agent",
			Description: "A loop agent that runs sub-agents",
			SubAgents:   subagents,
		},
		MaxIterations: uint(agentdLoop.MaxIterations),
	})
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}
	return loopAgent, nil
}

func (s *Session) CreateTool(ctx context.Context, agentdTool *agentdv1.Tool) (tool.Tool, error) {
	tool, err := s.createTool(ctx, agentdTool)
	if err != nil {
		err = fmt.Errorf("creating tool: %w", err)

		if s.onToolCreatedError != nil {
			go s.onToolCreatedError(agentdTool.Name, err)
		}

		return nil, err
	}
	if s.onToolCreated != nil {
		go s.onToolCreated(tool.Name())
	}
	return tool, nil
}

func (s *Session) createTool(ctx context.Context, agentdTool *agentdv1.Tool) (tool.Tool, error) {
	cfg := functiontool.Config{
		Name:        agentdTool.Name,
		Description: agentdTool.Description,
	}

	if agentdTool.InputSchema != nil {
		var schema jsonschema.Schema
		if err := json.Unmarshal([]byte(*agentdTool.InputSchema), &schema); err != nil {
			return nil, fmt.Errorf("parsing input schema for tool %q: %w", cfg.Name, err)
		}
		cfg.InputSchema = &schema
	}

	if agentdTool.OutputSchema != nil {
		var schema jsonschema.Schema
		if err := json.Unmarshal([]byte(*agentdTool.OutputSchema), &schema); err != nil {
			return nil, fmt.Errorf("parsing output schema for tool %q: %w", cfg.Name, err)
		}
		cfg.OutputSchema = &schema
	}

	return functiontool.New(cfg, func(_ tool.Context, args map[string]any) (map[string]any, error) {
		argsJSON, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("marshalling tool args: %w", err)
		}

		callID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating tool call ID: %w", err)
		}

		err = s.stream.Send(&agentdv1.RunResponse{
			Response: &agentdv1.RunResponse_ToolcallRequest_{
				ToolcallRequest: &agentdv1.RunResponse_ToolcallRequest{
					ToolName:  agentdTool.Name,
					ToolInput: string(argsJSON),
				},
			},
		})

		if err != nil {
			return nil, fmt.Errorf("sending tool call request: %w", err)
		}

		ch := make(chan *agentdv1.RunRequest_ToolcallResult, 1)
		s.mu.Lock()
		s.toolcall[callID.String()] = ch
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			delete(s.toolcall, callID.String())
			s.mu.Unlock()
		}()

		select {
		case resp, ok := <-ch:
			if !ok || resp == nil {
				return nil, fmt.Errorf("tool call cancelled")
			}
			if resp.Error != nil {
				return nil, fmt.Errorf("tool call error: %s", *resp.Error)
			}
			if resp.Result == nil {
				return map[string]any{}, nil
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(*resp.Result), &out); err != nil {
				return nil, fmt.Errorf("unmarshalling tool result: %w", err)
			}
			return out, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
}
