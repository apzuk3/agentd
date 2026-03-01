package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"iter"
	"net"
	"net/http"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/google/jsonschema-go/jsonschema"
	"golang.org/x/net/http2"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
	"github.com/apzuk3/agentd/gen/proto/go/agentd/v1/agentdv1connect"
)

type registeredTool struct {
	proto   *agentdv1.Tool
	handler func(ctx context.Context, input string) (string, error)
}

// Client manages tool registrations and communicates with an agentd server.
type Client struct {
	baseURL           string
	httpClient        connect.HTTPClient
	connectOpts       []connect.ClientOption
	heartbeatInterval time.Duration
	tools             map[string]*registeredTool
}

// Option configures a Client.
type Option func(*Client)

func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.httpClient = c }
}

func WithHeartbeatInterval(d time.Duration) Option {
	return func(cl *Client) { cl.heartbeatInterval = d }
}

func WithConnectOptions(opts ...connect.ClientOption) Option {
	return func(cl *Client) { cl.connectOpts = append(cl.connectOpts, opts...) }
}

// New creates a Client that will connect to the agentd server at baseURL.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:           baseURL,
		heartbeatInterval: 30 * time.Second,
		tools:             make(map[string]*registeredTool),
	}
	for _, o := range opts {
		o(c)
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			},
		}
	}
	return c
}

// AddTool registers a tool whose input schema is automatically generated from T.
// T must be a concrete struct (not any/interface{}); use json tags for field
// names and the "jsonschema" struct tag for property descriptions.
// The handler fn receives the parsed input and returns a result that is
// JSON-marshaled back to the server.
func AddTool[T any](c *Client, name, description string, fn func(context.Context, T) (any, error)) error {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		return fmt.Errorf("failed to infer input schema for tool %q: %w (T must be a concrete struct, not any/interface{})", name, err)
	}

	b, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("failed to marshal input schema for tool %q: %w", name, err)
	}
	schemaStr := string(b)

	toolProto := &agentdv1.Tool{
		Name:        name,
		Description: description,
		InputSchema: &schemaStr,
	}

	if err := protovalidate.Validate(toolProto); err != nil {
		return fmt.Errorf("invalid tool definition: %w", err)
	}

	c.tools[name] = &registeredTool{
		proto: toolProto,
		handler: func(ctx context.Context, input string) (string, error) {
			var args T
			if input != "" {
				if err := json.Unmarshal([]byte(input), &args); err != nil {
					return "", fmt.Errorf("unmarshaling tool input: %w", err)
				}
			}
			result, err := fn(ctx, args)
			if err != nil {
				return "", err
			}
			switch v := result.(type) {
			case string:
				return v, nil
			default:
				b, err := json.Marshal(v)
				if err != nil {
					return "", fmt.Errorf("marshaling tool result: %w", err)
				}
				return string(b), nil
			}
		},
	}
	return nil
}

// Tool returns the proto Tool definition for a registered tool, for use when
// constructing agent trees.
func (c *Client) Tool(name string) *agentdv1.Tool {
	rt, ok := c.tools[name]
	if !ok {
		return nil
	}
	return rt.proto
}

// RunOption configures a single Run invocation.
type RunOption func(*runConfig)

type runConfig struct {
	sessionID string
}

// WithSessionID resumes an existing session instead of creating a new one.
func WithSessionID(id string) RunOption {
	return func(rc *runConfig) { rc.sessionID = id }
}

// Event is yielded by Run for each server message the caller should see.
// Exactly one field is non-nil.
type Event struct {
	OutputChunk *OutputChunk
	Error       *Error
	End         *End
}

type OutputChunk struct {
	AgentPath []string
	Content   string
	Last      bool
	IsThought bool
}

type End struct {
	Completed    bool
	UsageSummary *agentdv1.UsageSummary
}

type Error struct {
	Code      agentdv1.ErrorCode
	Message   string
	Retryable bool
}

// Run opens a bidirectional stream to the server, sends the agent tree and
// user prompt, and returns an iterator that yields events. Tool calls and
// heartbeats are handled internally. Breaking out of the iterator cancels
// the session.
func (c *Client) Run(ctx context.Context, agent *agentdv1.Agent, userPrompt string, opts ...RunOption) iter.Seq2[*Event, error] {
	var rc runConfig
	for _, o := range opts {
		o(&rc)
	}

	return func(yield func(*Event, error) bool) {
		rpcClient := agentdv1connect.NewAgentdClient(c.httpClient, c.baseURL, c.connectOpts...)

		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		stream, err := rpcClient.Run(streamCtx)
		if err != nil {
			yield(nil, fmt.Errorf("opening stream: %w", err))
			return
		}
		defer stream.CloseResponse()
		defer stream.CloseRequest()

		execReq := &agentdv1.RunRequest{
			Request: &agentdv1.RunRequest_Execute{
				Execute: &agentdv1.RunRequest_ExecuteRequest{
					Agent:      agent,
					UserPrompt: userPrompt,
				},
			},
		}
		if rc.sessionID != "" {
			execReq.GetExecute().SessionId = &rc.sessionID
		}

		if err := stream.Send(execReq); err != nil {
			yield(nil, fmt.Errorf("sending execute request: %w", err))
			return
		}

		resp, err := stream.Receive()
		if err != nil {
			yield(nil, fmt.Errorf("receiving execute response: %w", err))
			return
		}

		execResp := resp.GetExecute()
		if execResp == nil {
			yield(nil, fmt.Errorf("expected ExecuteResponse, got %T", resp.GetResponse()))
			return
		}
		sessionID := execResp.GetSessionId()

		var sendMu sync.Mutex

		// Heartbeat goroutine.
		heartbeatDone := make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			ticker := time.NewTicker(c.heartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-streamCtx.Done():
					return
				case <-ticker.C:
					sendMu.Lock()
					_ = stream.Send(&agentdv1.RunRequest{
						Request: &agentdv1.RunRequest_Heartbeat{
							Heartbeat: &agentdv1.RunRequest_HeartbeatRequest{
								SessionId: sessionID,
							},
						},
					})
					sendMu.Unlock()
				}
			}
		}()

		defer func() {
			cancel()
			<-heartbeatDone
		}()

		for {
			resp, err := stream.Receive()
			if err != nil {
				yield(nil, err)
				return
			}

			switch r := resp.GetResponse().(type) {
			case *agentdv1.RunResponse_ToolCall:
				tc := r.ToolCall
				rt, ok := c.tools[tc.GetToolName()]
				if !ok {
					sendMu.Lock()
					errMsg := fmt.Sprintf("unknown tool: %s", tc.GetToolName())
					_ = stream.Send(&agentdv1.RunRequest{
						Request: &agentdv1.RunRequest_ToolCallResponse_{
							ToolCallResponse: &agentdv1.RunRequest_ToolCallResponse{
								SessionId:  sessionID,
								ToolCallId: tc.GetToolCallId(),
								ToolName:   tc.GetToolName(),
								Result:     &agentdv1.RunRequest_ToolCallResponse_Error{Error: errMsg},
							},
						},
					})
					sendMu.Unlock()
					continue
				}

				output, execErr := rt.handler(streamCtx, tc.GetToolInput())
				tcResp := &agentdv1.RunRequest_ToolCallResponse{
					SessionId:  sessionID,
					ToolCallId: tc.GetToolCallId(),
					ToolName:   tc.GetToolName(),
				}
				if execErr != nil {
					tcResp.Result = &agentdv1.RunRequest_ToolCallResponse_Error{Error: execErr.Error()}
				} else {
					tcResp.Result = &agentdv1.RunRequest_ToolCallResponse_Output{Output: output}
				}

				sendMu.Lock()
				_ = stream.Send(&agentdv1.RunRequest{
					Request: &agentdv1.RunRequest_ToolCallResponse_{
						ToolCallResponse: tcResp,
					},
				})
				sendMu.Unlock()

			case *agentdv1.RunResponse_OutputChunk_:
				chunk := r.OutputChunk
				if !yield(&Event{
					OutputChunk: &OutputChunk{
						AgentPath: chunk.GetAgentPath(),
						Content:   chunk.GetContent(),
						Last:      chunk.GetLast(),
						IsThought: chunk.GetIsThought(),
					},
				}, nil) {
					return
				}

			case *agentdv1.RunResponse_Error:
				e := r.Error
				if !yield(&Event{
					Error: &Error{
						Code:      e.GetCode(),
						Message:   e.GetMessage(),
						Retryable: e.GetRetryable(),
					},
				}, nil) {
					return
				}

			case *agentdv1.RunResponse_End:
				end := r.End
				yield(&Event{
					End: &End{
						Completed:    end.GetCompleted(),
						UsageSummary: end.GetUsageSummary(),
					},
				}, nil)
				return

			case *agentdv1.RunResponse_Heartbeat:
				// Internal bookkeeping, not yielded.

			case *agentdv1.RunResponse_Execute:
				// Unexpected duplicate; ignore.
			}
		}
	}
}
