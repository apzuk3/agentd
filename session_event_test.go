package agentd

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmitter_NoListeners(t *testing.T) {
	e := NewSessionEventEmitter()
	e.Emit(context.Background(), SessionEvent{
		Type:      EventSessionStarted,
		Timestamp: time.Now(),
		SessionID: "s1",
	})
}

func TestEmitter_SingleListener(t *testing.T) {
	e := NewSessionEventEmitter()

	var received []SessionEvent
	e.Subscribe(SessionListenerFunc(func(_ context.Context, ev SessionEvent) {
		received = append(received, ev)
	}))

	e.Emit(context.Background(), SessionEvent{Type: EventSessionStarted, SessionID: "s1"})
	e.Emit(context.Background(), SessionEvent{Type: EventSessionEnded, SessionID: "s1"})

	if len(received) != 2 {
		t.Fatalf("got %d events, want 2", len(received))
	}
	if received[0].Type != EventSessionStarted {
		t.Errorf("event[0].Type = %q, want %q", received[0].Type, EventSessionStarted)
	}
	if received[1].Type != EventSessionEnded {
		t.Errorf("event[1].Type = %q, want %q", received[1].Type, EventSessionEnded)
	}
}

func TestEmitter_MultipleListeners(t *testing.T) {
	e := NewSessionEventEmitter()

	var count1, count2 atomic.Int32
	e.Subscribe(SessionListenerFunc(func(_ context.Context, _ SessionEvent) {
		count1.Add(1)
	}))
	e.Subscribe(SessionListenerFunc(func(_ context.Context, _ SessionEvent) {
		count2.Add(1)
	}))

	e.Emit(context.Background(), SessionEvent{Type: EventModelRequest, SessionID: "s1"})

	if count1.Load() != 1 {
		t.Errorf("listener 1 received %d events, want 1", count1.Load())
	}
	if count2.Load() != 1 {
		t.Errorf("listener 2 received %d events, want 1", count2.Load())
	}
}

func TestEmitter_ConcurrentEmit(t *testing.T) {
	e := NewSessionEventEmitter()

	var count atomic.Int32
	e.Subscribe(SessionListenerFunc(func(_ context.Context, _ SessionEvent) {
		count.Add(1)
	}))

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			e.Emit(context.Background(), SessionEvent{
				Type:      EventOutputChunk,
				SessionID: "s1",
			})
		}(i)
	}
	wg.Wait()

	if count.Load() != 100 {
		t.Errorf("got %d events, want 100", count.Load())
	}
}

func TestSessionEmitHelper_NilEmitter(t *testing.T) {
	s := &Session{id: "test"}
	// Should not panic when emitter is nil.
	s.emit(context.Background(), EventSessionStarted, nil)
}

func TestSessionEmitHelper_PopulatesFields(t *testing.T) {
	e := NewSessionEventEmitter()
	var got SessionEvent
	e.Subscribe(SessionListenerFunc(func(_ context.Context, ev SessionEvent) {
		got = ev
	}))

	s := &Session{id: "sess-42", emitter: e}

	before := time.Now()
	s.emit(context.Background(), EventToolDispatched, map[string]any{"tool_name": "read_file"})
	after := time.Now()

	if got.Type != EventToolDispatched {
		t.Errorf("Type = %q, want %q", got.Type, EventToolDispatched)
	}
	if got.SessionID != "sess-42" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-42")
	}
	if got.Timestamp.Before(before) || got.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in [%v, %v]", got.Timestamp, before, after)
	}
	if got.Data["tool_name"] != "read_file" {
		t.Errorf("Data[tool_name] = %v, want read_file", got.Data["tool_name"])
	}
}
