package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/google/jsonschema-go/jsonschema"
	"golang.org/x/net/http2"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
	"github.com/apzuk3/agentd/gen/proto/go/agentd/v1/agentdv1connect"
)

type contextKey struct{}

var stateKey contextKey

// GetState retrieves the current ADK session state from the context.
// This is populated automatically during tool execution.
// The returned map is a copy; modifying it does not affect the session state.
func GetState(ctx context.Context) map[string]string {
	v := ctx.Value(stateKey)
	if v == nil {
		return nil
	}
	return v.(map[string]string)
}

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

var (
	contextType = reflect.TypeFor[context.Context]()
	errorType   = reflect.TypeFor[error]()
)

// Model name constants for use with LlmAgent.Model. These are the supported
// model identifiers for each provider; use the string values directly when
// constructing agent definitions. Model availability may change; consult each
// provider's documentation for current offerings.
const (
	// ——— Gemini (Google) ———
	// Gemini 2.5: most advanced, best price-performance.
	ModelGemini25Pro        = "gemini-2.5-pro"
	ModelGemini25Flash      = "gemini-2.5-flash"
	ModelGemini25FlashLite  = "gemini-2.5-flash-lite"
	// Gemini 3: preview, next-generation.
	ModelGemini31ProPreview     = "gemini-3.1-pro-preview"
	ModelGemini3FlashPreview    = "gemini-3-flash-preview"
	ModelGemini31FlashLitePreview = "gemini-3.1-flash-lite-preview"
	// Gemini 2.0: legacy (retiring June 2026).
	ModelGemini20Flash      = "gemini-2.0-flash"
	ModelGemini20FlashLite  = "gemini-2.0-flash-lite"
	// Gemini 1.5: stable older generation.
	ModelGemini15Pro        = "gemini-1.5-pro"
	ModelGemini15Flash      = "gemini-1.5-flash"
	ModelGemini15FlashLite  = "gemini-1.5-flash-lite"

	// ——— Claude (Anthropic) ———
	// Claude 4: latest frontier models.
	ModelClaudeOpus46   = "claude-opus-4-6"
	ModelClaudeSonnet46 = "claude-sonnet-4-6"
	ModelClaudeHaiku45  = "claude-haiku-4-5"
	// Claude 3.5: previous generation.
	ModelClaude35Sonnet = "claude-3-5-sonnet-20241022"
	ModelClaude35Haiku  = "claude-3-5-haiku-20241022"
	// Claude 3: legacy.
	ModelClaude3Opus   = "claude-3-opus-20240229"
	ModelClaude3Sonnet = "claude-3-sonnet-20240229"
	ModelClaude3Haiku  = "claude-3-haiku-20240307"

	// ——— OpenAI ———
	// GPT-5: flagship and reasoning.
	ModelGPT54       = "gpt-5.4"
	ModelGPT54Pro    = "gpt-5.4-pro"
	ModelGPT5Mini    = "gpt-5-mini"
	ModelGPT5Nano    = "gpt-5-nano"
	ModelGPT5        = "gpt-5"
	ModelGPT5Pro     = "gpt-5-pro"
	ModelGPT52       = "gpt-5.2"
	ModelGPT51       = "gpt-5.1"
	// GPT-4: smart non-reasoning.
	ModelGPT41       = "gpt-4.1"
	ModelGPT41Mini   = "gpt-4.1-mini"
	ModelGPT41Nano   = "gpt-4.1-nano"
	ModelGPT4o       = "gpt-4o"
	ModelGPT4oMini   = "gpt-4o-mini"
	ModelGPT4Turbo   = "gpt-4-turbo"
	ModelGPT4        = "gpt-4"
	// Reasoning (o-series).
	ModelO3         = "o3"
	ModelO3Pro      = "o3-pro"
	ModelO3Mini     = "o3-mini"
	ModelO4Mini     = "o4-mini"
	ModelO1         = "o1"
	ModelO1Pro      = "o1-pro"
	ModelO1Mini     = "o1-mini"
	// Legacy.
	ModelGPT35Turbo = "gpt-3.5-turbo"
)

// AddTool registers a tool whose input schema is inferred from fn's signature.
// fn must be a function with the signature func(context.Context, T) (R, error)
// where T is a concrete struct; use json tags for field names and the
// "jsonschema" struct tag for property descriptions. The return value R is
// JSON-marshaled back to the server (strings are sent as-is).
func (c *Client) AddTool(name, description string, fn any) error {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	if fnType.Kind() != reflect.Func {
		return fmt.Errorf("AddTool %q: fn must be a function, got %T", name, fn)
	}
	if fnType.NumIn() != 2 {
		return fmt.Errorf("AddTool %q: fn must accept exactly 2 parameters (context.Context, T), got %d", name, fnType.NumIn())
	}
	if fnType.NumOut() != 2 {
		return fmt.Errorf("AddTool %q: fn must return exactly 2 values (result, error), got %d", name, fnType.NumOut())
	}
	if !fnType.In(0).Implements(contextType) {
		return fmt.Errorf("AddTool %q: first parameter must be context.Context, got %s", name, fnType.In(0))
	}
	if !fnType.Out(1).Implements(errorType) {
		return fmt.Errorf("AddTool %q: second return value must implement error, got %s", name, fnType.Out(1))
	}

	inputType := fnType.In(1)

	schema, err := jsonschema.ForType(inputType, nil)
	if err != nil {
		return fmt.Errorf("failed to infer input schema for tool %q: %w (input must be a concrete struct)", name, err)
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
			argPtr := reflect.New(inputType)
			if input != "" {
				if err := json.Unmarshal([]byte(input), argPtr.Interface()); err != nil {
					return "", fmt.Errorf("unmarshaling tool input: %w", err)
				}
			}
			results := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), argPtr.Elem()})
			if !results[1].IsNil() {
				return "", results[1].Interface().(error)
			}
			result := results[0].Interface()
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

// MustTool returns an Option that registers a tool during client construction.
// It panics if the tool definition is invalid. See [Client.AddTool] for fn
// signature requirements.
func MustTool(name, description string, fn any) Option {
	return func(c *Client) {
		if err := c.AddTool(name, description, fn); err != nil {
			panic(fmt.Sprintf("MustTool: %v", err))
		}
	}
}

// resolveTools walks the agent tree, collects all tool_names from LlmAgents,
// resolves each against the registered tools, and returns a deduplicated catalog.
func (c *Client) resolveTools(agent *agentdv1.Agent) ([]*agentdv1.Tool, error) {
	seen := make(map[string]bool)
	var catalog []*agentdv1.Tool

	var walk func(a *agentdv1.Agent) error
	walk = func(a *agentdv1.Agent) error {
		if a == nil {
			return nil
		}
		switch {
		case a.GetLlm() != nil:
			llm := a.GetLlm()
			for _, name := range llm.GetToolNames() {
				if seen[name] {
					continue
				}
				rt, ok := c.tools[name]
				if !ok {
					return fmt.Errorf("tool %q referenced by agent %q is not registered", name, a.GetName())
				}
				seen[name] = true
				catalog = append(catalog, rt.proto)
			}
			for _, sub := range llm.GetSubAgents() {
				if err := walk(sub); err != nil {
					return err
				}
			}
		case a.GetSequential() != nil:
			for _, sub := range a.GetSequential().GetAgents() {
				if err := walk(sub); err != nil {
					return err
				}
			}
		case a.GetParallel() != nil:
			for _, sub := range a.GetParallel().GetAgents() {
				if err := walk(sub); err != nil {
					return err
				}
			}
		case a.GetLoop() != nil:
			for _, sub := range a.GetLoop().GetAgents() {
				if err := walk(sub); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := walk(agent); err != nil {
		return nil, err
	}
	return catalog, nil
}

// RunOption configures a single Run invocation.
type RunOption func(*runConfig)

type runConfig struct {
	sessionID string
	headers   http.Header
}

// WithSessionID resumes an existing session instead of creating a new one.
func WithSessionID(id string) RunOption {
	return func(rc *runConfig) { rc.sessionID = id }
}

// BYOK header names mirroring the server-side constants. Clients use the
// convenience RunOption helpers below instead of setting these directly.
const (
	headerGeminiAPIKey    = "X-Agentd-Gemini-Api-Key"
	headerAnthropicAPIKey = "X-Agentd-Anthropic-Api-Key"
	headerOpenAIAPIKey    = "X-Agentd-Openai-Api-Key"
	headerTavilyAPIKey    = "X-Agentd-Tavily-Api-Key"
)

// WithGeminiAPIKey sets the Gemini API key for this Run only, overriding
// any server-side default.
func WithGeminiAPIKey(key string) RunOption {
	return withHeader(headerGeminiAPIKey, key)
}

// WithAnthropicAPIKey sets the Anthropic API key for this Run only.
func WithAnthropicAPIKey(key string) RunOption {
	return withHeader(headerAnthropicAPIKey, key)
}

// WithOpenAIAPIKey sets the OpenAI API key for this Run only.
func WithOpenAIAPIKey(key string) RunOption {
	return withHeader(headerOpenAIAPIKey, key)
}

// WithTavilyAPIKey sets the Tavily API key for this Run only.
func WithTavilyAPIKey(key string) RunOption {
	return withHeader(headerTavilyAPIKey, key)
}

func withHeader(name, value string) RunOption {
	return func(rc *runConfig) {
		if rc.headers == nil {
			rc.headers = make(http.Header)
		}
		rc.headers.Set(name, value)
	}
}

// Event is yielded by Run for each server message the caller should see.
// Exactly one field is non-nil.
type Event struct {
	OutputChunk *OutputChunk
	StateUpdate *StateUpdate
	Error       *Error
	End         *End
}

type OutputChunk struct {
	AgentPath []string
	Content   string
	Last      bool
	IsThought bool
}

// StateUpdate carries a snapshot or incremental delta of the ADK session state.
// Values are JSON-encoded strings; use [json.Unmarshal] to decode them into
// your application types.
type StateUpdate struct {
	State map[string]string
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

// Result is returned by the synchronous Run method and contains the
// aggregated output from a completed agent session.
type Result struct {
	Output       string
	State        map[string]string
	UsageSummary *agentdv1.UsageSummary
}

func errorEvent(format string, args ...any) *Event {
	return &Event{
		Error: &Error{
			Code:    agentdv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: fmt.Sprintf(format, args...),
		},
	}
}

// RunAsync opens a bidirectional stream to the server, sends the agent tree
// and user prompt, and returns an iterator that yields events. Tool calls and
// heartbeats are handled internally. Breaking out of the iterator cancels
// the session.
func (c *Client) RunAsync(ctx context.Context, agent *agentdv1.Agent, userPrompt string, opts ...RunOption) iter.Seq2[*Event, error] {
	var rc runConfig
	for _, o := range opts {
		o(&rc)
	}

	return func(yield func(*Event, error) bool) {
		toolCatalog, err := c.resolveTools(agent)
		if err != nil {
			yield(errorEvent("%v", err), nil)
			return
		}

		opts := append([]connect.ClientOption{}, c.connectOpts...)
		if len(rc.headers) > 0 {
			opts = append(opts, connect.WithInterceptors(&headerInterceptor{headers: rc.headers}))
		}
		rpcClient := agentdv1connect.NewAgentdClient(c.httpClient, c.baseURL, opts...)

		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		stream, err := rpcClient.Run(streamCtx)
		if err != nil {
			yield(errorEvent("opening stream: %v", err), nil)
			return
		}
		defer stream.CloseResponse()
		defer stream.CloseRequest()

		execReq := &agentdv1.RunRequest{
			Request: &agentdv1.RunRequest_Execute{
				Execute: &agentdv1.RunRequest_ExecuteRequest{
					Agent:      agent,
					UserPrompt: userPrompt,
					Tools:      toolCatalog,
				},
			},
		}
		if rc.sessionID != "" {
			execReq.GetExecute().SessionId = &rc.sessionID
		}

		if err := stream.Send(execReq); err != nil {
			yield(errorEvent("sending execute request: %v", err), nil)
			return
		}

		resp, err := stream.Receive()
		if err != nil {
			yield(errorEvent("receiving execute response: %v", err), nil)
			return
		}

		execResp := resp.GetExecute()
		if execResp == nil {
			yield(errorEvent("expected ExecuteResponse, got %T", resp.GetResponse()), nil)
			return
		}
		sessionID := execResp.GetSessionId()
		currentState := make(map[string]string)

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
				yield(errorEvent("%v", err), nil)
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

				stateCopy := make(map[string]string, len(currentState))
				maps.Copy(stateCopy, currentState)
				toolCtx := context.WithValue(streamCtx, stateKey, stateCopy)

				output, execErr := rt.handler(toolCtx, tc.GetToolInput())
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

			case *agentdv1.RunResponse_StateUpdate_:
				su := r.StateUpdate
				maps.Copy(currentState, su.GetState())
				if !yield(&Event{
					StateUpdate: &StateUpdate{
						State: su.GetState(),
					},
				}, nil) {
					return
				}

			case *agentdv1.RunResponse_Heartbeat:
				// Internal bookkeeping, not yielded.

			case *agentdv1.RunResponse_Execute:
				// Unexpected duplicate; ignore.
			}
		}
	}
}

// Run executes the agent synchronously, blocking until the session completes,
// and returns the aggregated result. It uses RunAsync internally.
func (c *Client) Run(ctx context.Context, agent *agentdv1.Agent, userPrompt string, opts ...RunOption) (*Result, error) {
	var buf strings.Builder
	state := make(map[string]string)
	var usage *agentdv1.UsageSummary

	for ev, _ := range c.RunAsync(ctx, agent, userPrompt, opts...) {
		switch {
		case ev.Error != nil:
			return nil, fmt.Errorf("agent error (code %v): %s", ev.Error.Code, ev.Error.Message)
		case ev.OutputChunk != nil:
			if !ev.OutputChunk.IsThought {
				buf.WriteString(ev.OutputChunk.Content)
			}
		case ev.StateUpdate != nil:
			maps.Copy(state, ev.StateUpdate.State)
		case ev.End != nil:
			usage = ev.End.UsageSummary
		}
	}

	return &Result{
		Output:       buf.String(),
		State:        state,
		UsageSummary: usage,
	}, nil
}

// headerInterceptor injects per-run HTTP headers into the outgoing request.
type headerInterceptor struct {
	headers http.Header
}

func (i *headerInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		for k, vals := range i.headers {
			for _, v := range vals {
				req.Header().Set(k, v)
			}
		}
		return next(ctx, req)
	}
}

func (i *headerInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		for k, vals := range i.headers {
			for _, v := range vals {
				conn.RequestHeader().Set(k, v)
			}
		}
		return conn
	}
}

func (i *headerInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
