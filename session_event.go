package agentd

import (
	"context"
	"sync"
	"time"
)

// SessionEventType identifies the kind of lifecycle event emitted by a session.
type SessionEventType string

const (
	EventSessionStarted  SessionEventType = "session_started"
	EventSessionEnded    SessionEventType = "session_ended"
	EventUserMessage     SessionEventType = "user_message"
	EventAgentStarted    SessionEventType = "agent_started"
	EventAgentEnded      SessionEventType = "agent_ended"
	EventModelRequest    SessionEventType = "model_request"
	EventModelResponse   SessionEventType = "model_response"
	EventModelError      SessionEventType = "model_error"
	EventToolDispatched  SessionEventType = "tool_dispatched"
	EventToolResponse    SessionEventType = "tool_response"
	EventToolStarted     SessionEventType = "tool_started"
	EventToolCompleted   SessionEventType = "tool_completed"
	EventToolError       SessionEventType = "tool_error"
	EventStateChange     SessionEventType = "state_change"
	EventOutputChunk     SessionEventType = "output_chunk"
	EventCancelRequested SessionEventType = "cancel_requested"
	EventError           SessionEventType = "error"
)

// SessionEvent is a structured record of something that happened during a
// session's lifecycle. The Data map carries type-specific payload (e.g.
// tool_name, model, token counts).
type SessionEvent struct {
	Type      SessionEventType
	Timestamp time.Time
	SessionID string
	Data      map[string]any
}

// SessionListener receives session events. Implementations must be safe for
// concurrent use and should return quickly — heavy work should be offloaded
// internally.
type SessionListener interface {
	OnSessionEvent(ctx context.Context, event SessionEvent)
}

// SessionListenerFunc adapts an ordinary function into a SessionListener.
type SessionListenerFunc func(ctx context.Context, event SessionEvent)

func (f SessionListenerFunc) OnSessionEvent(ctx context.Context, event SessionEvent) {
	f(ctx, event)
}

// SessionEventEmitter fans out session events to registered listeners.
// It is safe for concurrent use.
type SessionEventEmitter struct {
	mu        sync.RWMutex
	listeners []SessionListener
}

// NewSessionEventEmitter creates an emitter with no listeners.
func NewSessionEventEmitter() *SessionEventEmitter {
	return &SessionEventEmitter{}
}

// Subscribe adds a listener that will receive all future events.
func (e *SessionEventEmitter) Subscribe(l SessionListener) {
	e.mu.Lock()
	e.listeners = append(e.listeners, l)
	e.mu.Unlock()
}

// Emit sends an event to every registered listener.
func (e *SessionEventEmitter) Emit(ctx context.Context, event SessionEvent) {
	e.mu.RLock()
	listeners := e.listeners
	e.mu.RUnlock()

	for _, l := range listeners {
		l.OnSessionEvent(ctx, event)
	}
}
