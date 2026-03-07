package agentd

import (
	"context"
	"sync"
)

// SessionPlugin controls and observes the session lifecycle.
// Methods prefixed with "Before" or returning error act as gates: returning a
// non-nil error rejects the operation. Fire-and-forget methods are purely
// observational.
//
// Implementations must be safe for concurrent use. Embed BasePlugin to get
// no-op defaults for every method and override only the ones you need.
type SessionPlugin interface {
	// OnSessionStart is called when a new session is initialised.
	// Return an error to reject the session.
	OnSessionStart(ctx context.Context, info SessionStartInfo) error

	// OnSessionEnd is called when the session finishes (normally or on error).
	OnSessionEnd(ctx context.Context, info SessionEndInfo)

	// BeforeAgent is called before an agent begins execution.
	// Return an error to prevent the agent from running.
	BeforeAgent(ctx context.Context, info AgentInfo) error

	// AfterAgent is called after an agent finishes execution.
	AfterAgent(ctx context.Context, info AgentInfo)

	// BeforeModelCall is called before an LLM request is sent.
	// Return an error to block the request.
	BeforeModelCall(ctx context.Context, info ModelCallInfo) error

	// AfterModelCall is called after an LLM response is received.
	// Return an error to halt the session (e.g. token budget exceeded).
	AfterModelCall(ctx context.Context, info ModelCallResult) error

	// BeforeToolCall is called before a tool is executed.
	// Return an error to reject the tool call.
	BeforeToolCall(ctx context.Context, info ToolCallInfo) error

	// AfterToolCall is called after a tool completes.
	AfterToolCall(ctx context.Context, info ToolCallResult)

	// OnToolDispatched is called when a tool call is sent to the client.
	OnToolDispatched(ctx context.Context, info ToolDispatchInfo)

	// OnToolResponse is called when a tool call response arrives from the client.
	OnToolResponse(ctx context.Context, info ToolResponseInfo)

	// OnOutputChunk is called for each content chunk streamed to the client.
	OnOutputChunk(ctx context.Context, info OutputChunkInfo)

	// OnUserMessage is called when the user message is received.
	OnUserMessage(ctx context.Context, info UserMessageInfo)

	// OnError is called when an error occurs during the session.
	OnError(ctx context.Context, info ErrorInfo)
}

// ---------------------------------------------------------------------------
// Info structs — typed payloads for each plugin method
// ---------------------------------------------------------------------------

type SessionStartInfo struct {
	SessionID  string
	RootAgent  string
	ToolCount  int
	UserPrompt string
}

type SessionEndInfo struct {
	SessionID string
	Usage     UsageInfo
	Err       error
}

type AgentInfo struct {
	SessionID string
	AgentName string
}

type ModelCallInfo struct {
	SessionID       string
	AgentName       string
	Model           string
	CumulativeUsage UsageInfo
}

type ModelCallResult struct {
	SessionID        string
	AgentName        string
	PromptTokens     int32
	CompletionTokens int32
	TotalTokens      int32
	CumulativeUsage  UsageInfo
	Err              error
}

type ToolCallInfo struct {
	SessionID string
	ToolName  string
	AgentName string
	AgentPath []string
	Args      map[string]any
}

type ToolCallResult struct {
	SessionID string
	ToolName  string
	AgentName string
	Result    map[string]any
	Err       error
}

type ToolDispatchInfo struct {
	SessionID  string
	ToolCallID string
	ToolName   string
	AgentPath  []string
	InputLen   int
}

type ToolResponseInfo struct {
	SessionID  string
	ToolCallID string
	ToolName   string
}

type OutputChunkInfo struct {
	SessionID  string
	AgentName  string
	AgentPath  []string
	IsThought  bool
	IsFinal    bool
	ContentLen int
}

type UserMessageInfo struct {
	SessionID  string
	AgentName  string
	ContentLen int
}

type ErrorInfo struct {
	SessionID string
	Message   string
	Err       error
}

type UsageInfo struct {
	PromptTokens     int32
	CompletionTokens int32
	CachedTokens     int32
	ThoughtsTokens   int32
	TotalTokens      int32
	LLMCalls         int32
}

// ---------------------------------------------------------------------------
// BasePlugin — no-op implementation of every SessionPlugin method
// ---------------------------------------------------------------------------

// BasePlugin provides no-op defaults for every SessionPlugin method.
// Embed it in your plugin struct and override only the methods you need.
type BasePlugin struct{}

func (BasePlugin) OnSessionStart(context.Context, SessionStartInfo) error { return nil }
func (BasePlugin) OnSessionEnd(context.Context, SessionEndInfo)           {}
func (BasePlugin) BeforeAgent(context.Context, AgentInfo) error           { return nil }
func (BasePlugin) AfterAgent(context.Context, AgentInfo)                  {}
func (BasePlugin) BeforeModelCall(context.Context, ModelCallInfo) error   { return nil }
func (BasePlugin) AfterModelCall(context.Context, ModelCallResult) error  { return nil }
func (BasePlugin) BeforeToolCall(context.Context, ToolCallInfo) error     { return nil }
func (BasePlugin) AfterToolCall(context.Context, ToolCallResult)          {}
func (BasePlugin) OnToolDispatched(context.Context, ToolDispatchInfo)     {}
func (BasePlugin) OnToolResponse(context.Context, ToolResponseInfo)       {}
func (BasePlugin) OnOutputChunk(context.Context, OutputChunkInfo)         {}
func (BasePlugin) OnUserMessage(context.Context, UserMessageInfo)         {}
func (BasePlugin) OnError(context.Context, ErrorInfo)                     {}

// ---------------------------------------------------------------------------
// PluginChain — iterates registered plugins, short-circuits on gate errors
// ---------------------------------------------------------------------------

// PluginChain fans out lifecycle calls to registered SessionPlugins.
// Gate methods (those returning error) short-circuit on the first error.
// It is safe for concurrent use.
type PluginChain struct {
	mu      sync.RWMutex
	plugins []SessionPlugin
}

// NewPluginChain creates an empty plugin chain.
func NewPluginChain() *PluginChain {
	return &PluginChain{}
}

// Register appends a plugin to the chain.
func (c *PluginChain) Register(p SessionPlugin) {
	c.mu.Lock()
	c.plugins = append(c.plugins, p)
	c.mu.Unlock()
}

// snapshot returns a point-in-time copy of the plugin slice so iteration
// does not hold the lock.
func (c *PluginChain) snapshot() []SessionPlugin {
	c.mu.RLock()
	s := c.plugins
	c.mu.RUnlock()
	return s
}

// --- gate methods (short-circuit on first error) ---

func (c *PluginChain) OnSessionStart(ctx context.Context, info SessionStartInfo) error {
	for _, p := range c.snapshot() {
		if err := p.OnSessionStart(ctx, info); err != nil {
			return err
		}
	}
	return nil
}

func (c *PluginChain) BeforeAgent(ctx context.Context, info AgentInfo) error {
	for _, p := range c.snapshot() {
		if err := p.BeforeAgent(ctx, info); err != nil {
			return err
		}
	}
	return nil
}

func (c *PluginChain) BeforeModelCall(ctx context.Context, info ModelCallInfo) error {
	for _, p := range c.snapshot() {
		if err := p.BeforeModelCall(ctx, info); err != nil {
			return err
		}
	}
	return nil
}

func (c *PluginChain) AfterModelCall(ctx context.Context, info ModelCallResult) error {
	for _, p := range c.snapshot() {
		if err := p.AfterModelCall(ctx, info); err != nil {
			return err
		}
	}
	return nil
}

func (c *PluginChain) BeforeToolCall(ctx context.Context, info ToolCallInfo) error {
	for _, p := range c.snapshot() {
		if err := p.BeforeToolCall(ctx, info); err != nil {
			return err
		}
	}
	return nil
}

// --- fire-and-forget methods ---

func (c *PluginChain) OnSessionEnd(ctx context.Context, info SessionEndInfo) {
	for _, p := range c.snapshot() {
		p.OnSessionEnd(ctx, info)
	}
}

func (c *PluginChain) AfterAgent(ctx context.Context, info AgentInfo) {
	for _, p := range c.snapshot() {
		p.AfterAgent(ctx, info)
	}
}

func (c *PluginChain) AfterToolCall(ctx context.Context, info ToolCallResult) {
	for _, p := range c.snapshot() {
		p.AfterToolCall(ctx, info)
	}
}

func (c *PluginChain) OnToolDispatched(ctx context.Context, info ToolDispatchInfo) {
	for _, p := range c.snapshot() {
		p.OnToolDispatched(ctx, info)
	}
}

func (c *PluginChain) OnToolResponse(ctx context.Context, info ToolResponseInfo) {
	for _, p := range c.snapshot() {
		p.OnToolResponse(ctx, info)
	}
}

func (c *PluginChain) OnOutputChunk(ctx context.Context, info OutputChunkInfo) {
	for _, p := range c.snapshot() {
		p.OnOutputChunk(ctx, info)
	}
}

func (c *PluginChain) OnUserMessage(ctx context.Context, info UserMessageInfo) {
	for _, p := range c.snapshot() {
		p.OnUserMessage(ctx, info)
	}
}

func (c *PluginChain) OnError(ctx context.Context, info ErrorInfo) {
	for _, p := range c.snapshot() {
		p.OnError(ctx, info)
	}
}

var _ SessionPlugin = BasePlugin{}
