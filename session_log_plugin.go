package agentd

import (
	"context"
	"log/slog"
)

// LogPlugin is a SessionPlugin that logs every lifecycle event via slog.
// Register it with a PluginChain to get structured logging for the full
// session lifecycle.
type LogPlugin struct {
	BasePlugin
	log *slog.Logger
}

// NewLogPlugin creates a plugin that logs to the given logger.
// If logger is nil, slog.Default() is used.
func NewLogPlugin(logger *slog.Logger) *LogPlugin {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogPlugin{log: logger}
}

func (l *LogPlugin) OnSessionStart(ctx context.Context, info SessionStartInfo) error {
	l.log.InfoContext(ctx, "session started",
		"session_id", info.SessionID,
		"root_agent", info.RootAgent,
		"tool_count", info.ToolCount,
	)
	return nil
}

func (l *LogPlugin) OnSessionEnd(ctx context.Context, info SessionEndInfo) {
	l.log.InfoContext(ctx, "session ended",
		"session_id", info.SessionID,
		"prompt_tokens", info.Usage.PromptTokens,
		"completion_tokens", info.Usage.CompletionTokens,
		"cached_tokens", info.Usage.CachedTokens,
		"thoughts_tokens", info.Usage.ThoughtsTokens,
		"total_tokens", info.Usage.TotalTokens,
		"llm_calls", info.Usage.LLMCalls,
		"error", info.Err,
	)
}

func (l *LogPlugin) BeforeAgent(ctx context.Context, info AgentInfo) error {
	l.log.DebugContext(ctx, "agent started",
		"session_id", info.SessionID,
		"agent", info.AgentName,
	)
	return nil
}

func (l *LogPlugin) AfterAgent(ctx context.Context, info AgentInfo) {
	l.log.DebugContext(ctx, "agent ended",
		"session_id", info.SessionID,
		"agent", info.AgentName,
	)
}

func (l *LogPlugin) BeforeModelCall(ctx context.Context, info ModelCallInfo) error {
	l.log.DebugContext(ctx, "model request",
		"session_id", info.SessionID,
		"agent", info.AgentName,
		"model", info.Model,
	)
	return nil
}

func (l *LogPlugin) AfterModelCall(ctx context.Context, info ModelCallResult) error {
	l.log.DebugContext(ctx, "model response",
		"session_id", info.SessionID,
		"agent", info.AgentName,
		"prompt_tokens", info.PromptTokens,
		"completion_tokens", info.CompletionTokens,
		"total_tokens", info.TotalTokens,
	)
	return nil
}

func (l *LogPlugin) BeforeToolCall(ctx context.Context, info ToolCallInfo) error {
	l.log.DebugContext(ctx, "tool started",
		"session_id", info.SessionID,
		"agent", info.AgentName,
		"tool_name", info.ToolName,
	)
	return nil
}

func (l *LogPlugin) AfterToolCall(ctx context.Context, info ToolCallResult) {
	if info.Err != nil {
		l.log.ErrorContext(ctx, "tool error",
			"session_id", info.SessionID,
			"agent", info.AgentName,
			"tool_name", info.ToolName,
			"error", info.Err,
		)
		return
	}
	l.log.DebugContext(ctx, "tool completed",
		"session_id", info.SessionID,
		"agent", info.AgentName,
		"tool_name", info.ToolName,
	)
}

func (l *LogPlugin) OnToolDispatched(ctx context.Context, info ToolDispatchInfo) {
	l.log.InfoContext(ctx, "tool dispatched",
		"session_id", info.SessionID,
		"tool_call_id", info.ToolCallID,
		"tool_name", info.ToolName,
		"agent_path", info.AgentPath,
		"input_len", info.InputLen,
	)
}

func (l *LogPlugin) OnToolResponse(ctx context.Context, info ToolResponseInfo) {
	l.log.InfoContext(ctx, "tool response received",
		"session_id", info.SessionID,
		"tool_call_id", info.ToolCallID,
		"tool_name", info.ToolName,
	)
}

func (l *LogPlugin) OnOutputChunk(ctx context.Context, info OutputChunkInfo) {
	l.log.DebugContext(ctx, "output chunk",
		"session_id", info.SessionID,
		"agent", info.AgentName,
		"is_thought", info.IsThought,
		"is_final", info.IsFinal,
		"content_len", info.ContentLen,
	)
}

func (l *LogPlugin) OnUserMessage(ctx context.Context, info UserMessageInfo) {
	l.log.DebugContext(ctx, "user message",
		"session_id", info.SessionID,
		"agent", info.AgentName,
		"content_len", info.ContentLen,
	)
}

func (l *LogPlugin) OnError(ctx context.Context, info ErrorInfo) {
	l.log.ErrorContext(ctx, "session error",
		"session_id", info.SessionID,
		"message", info.Message,
		"error", info.Err,
	)
}

var _ SessionPlugin = (*LogPlugin)(nil)
