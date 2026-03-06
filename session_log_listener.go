package agentd

import (
	"context"
	"log/slog"
)

// SessionLogListener is a SessionListener that logs every session event via
// slog. Subscribe it to a SessionEventEmitter to get structured logging for
// the full session lifecycle.
type SessionLogListener struct {
	log *slog.Logger
}

// NewSessionLogListener creates a listener that logs to the given logger.
// If logger is nil, slog.Default() is used.
func NewSessionLogListener(logger *slog.Logger) *SessionLogListener {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionLogListener{log: logger}
}

func (l *SessionLogListener) OnSessionEvent(ctx context.Context, event SessionEvent) {
	log := l.log.With("session_id", event.SessionID)

	switch event.Type {
	case EventSessionStarted:
		log.InfoContext(ctx, "session started",
			"root_agent", event.Data["root_agent"],
			"tool_count", event.Data["tool_count"],
		)

	case EventSessionEnded:
		log.InfoContext(ctx, "session ended",
			"prompt_tokens", event.Data["prompt_tokens"],
			"completion_tokens", event.Data["completion_tokens"],
			"cached_tokens", event.Data["cached_tokens"],
			"thoughts_tokens", event.Data["thoughts_tokens"],
			"total_tokens", event.Data["total_tokens"],
			"llm_calls", event.Data["llm_calls"],
			"loop_error", event.Data["loop_error"],
			"runner_error", event.Data["runner_error"],
		)

	case EventUserMessage:
		log.DebugContext(ctx, "user message",
			"agent", event.Data["agent"],
			"content_len", event.Data["content_len"],
		)

	case EventAgentStarted:
		log.DebugContext(ctx, "agent started", "agent", event.Data["agent"])

	case EventAgentEnded:
		log.DebugContext(ctx, "agent ended", "agent", event.Data["agent"])

	case EventModelRequest:
		log.DebugContext(ctx, "model request",
			"agent", event.Data["agent"],
			"model", event.Data["model"],
		)

	case EventModelResponse:
		log.DebugContext(ctx, "model response",
			"agent", event.Data["agent"],
			"prompt_tokens", event.Data["prompt_tokens"],
			"completion_tokens", event.Data["completion_tokens"],
			"total_tokens", event.Data["total_tokens"],
		)

	case EventModelError:
		log.ErrorContext(ctx, "model error",
			"agent", event.Data["agent"],
			"model", event.Data["model"],
			"error", event.Data["error"],
		)

	case EventToolDispatched:
		log.InfoContext(ctx, "tool dispatched",
			"tool_call_id", event.Data["tool_call_id"],
			"tool_name", event.Data["tool_name"],
			"agent_path", event.Data["agent_path"],
			"input_len", event.Data["input_len"],
		)

	case EventToolResponse:
		log.InfoContext(ctx, "tool response received",
			"tool_call_id", event.Data["tool_call_id"],
			"tool_name", event.Data["tool_name"],
		)

	case EventToolStarted:
		log.DebugContext(ctx, "tool started",
			"agent", event.Data["agent"],
			"tool_name", event.Data["tool_name"],
		)

	case EventToolCompleted:
		log.DebugContext(ctx, "tool completed",
			"agent", event.Data["agent"],
			"tool_name", event.Data["tool_name"],
		)

	case EventToolError:
		log.ErrorContext(ctx, "tool error",
			"agent", event.Data["agent"],
			"tool_name", event.Data["tool_name"],
			"error", event.Data["error"],
		)

	case EventStateChange:
		log.DebugContext(ctx, "state change",
			"agent", event.Data["agent"],
			"key_count", event.Data["key_count"],
		)

	case EventOutputChunk:
		log.DebugContext(ctx, "output chunk",
			"agent", event.Data["agent"],
			"is_thought", event.Data["is_thought"],
			"is_final", event.Data["is_final"],
			"content_len", event.Data["content_len"],
		)

	case EventCancelRequested:
		log.InfoContext(ctx, "cancel requested")

	case EventError:
		log.ErrorContext(ctx, "session error",
			"message", event.Data["message"],
			"error", event.Data["error"],
		)

	default:
		log.WarnContext(ctx, "unknown session event", "type", event.Type)
	}
}

var _ SessionListener = (*SessionLogListener)(nil)
