package agentd

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// recordingPlugin records which methods were called.
type recordingPlugin struct {
	BasePlugin
	calls []string
	mu    sync.Mutex
}

func (r *recordingPlugin) record(name string) {
	r.mu.Lock()
	r.calls = append(r.calls, name)
	r.mu.Unlock()
}

func (r *recordingPlugin) getCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *recordingPlugin) OnSessionStart(_ context.Context, _ SessionStartInfo) error {
	r.record("OnSessionStart")
	return nil
}

func (r *recordingPlugin) OnSessionEnd(_ context.Context, _ SessionEndInfo) {
	r.record("OnSessionEnd")
}

func (r *recordingPlugin) BeforeToolCall(_ context.Context, _ ToolCallInfo) error {
	r.record("BeforeToolCall")
	return nil
}

func (r *recordingPlugin) AfterToolCall(_ context.Context, _ ToolCallResult) {
	r.record("AfterToolCall")
}

func (r *recordingPlugin) OnError(_ context.Context, _ ErrorInfo) {
	r.record("OnError")
}

func TestPluginChain_NoPlugins(t *testing.T) {
	c := NewPluginChain()
	if err := c.OnSessionStart(context.Background(), SessionStartInfo{SessionID: "s1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.OnSessionEnd(context.Background(), SessionEndInfo{SessionID: "s1"})
}

func TestPluginChain_SinglePlugin(t *testing.T) {
	c := NewPluginChain()
	p := &recordingPlugin{}
	c.Register(p)

	ctx := context.Background()
	if err := c.OnSessionStart(ctx, SessionStartInfo{SessionID: "s1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.OnSessionEnd(ctx, SessionEndInfo{SessionID: "s1"})

	calls := p.getCalls()
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if calls[0] != "OnSessionStart" {
		t.Errorf("calls[0] = %q, want OnSessionStart", calls[0])
	}
	if calls[1] != "OnSessionEnd" {
		t.Errorf("calls[1] = %q, want OnSessionEnd", calls[1])
	}
}

func TestPluginChain_MultiplePlugins(t *testing.T) {
	c := NewPluginChain()
	p1 := &recordingPlugin{}
	p2 := &recordingPlugin{}
	c.Register(p1)
	c.Register(p2)

	ctx := context.Background()
	if err := c.BeforeToolCall(ctx, ToolCallInfo{ToolName: "read_file"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(p1.getCalls()) != 1 {
		t.Errorf("p1 got %d calls, want 1", len(p1.getCalls()))
	}
	if len(p2.getCalls()) != 1 {
		t.Errorf("p2 got %d calls, want 1", len(p2.getCalls()))
	}
}

func TestPluginChain_ShortCircuit(t *testing.T) {
	c := NewPluginChain()

	errBlocked := errors.New("blocked by policy")

	blocker := &blockingPlugin{err: errBlocked}
	after := &recordingPlugin{}

	c.Register(blocker)
	c.Register(after)

	err := c.BeforeToolCall(context.Background(), ToolCallInfo{ToolName: "dangerous"})
	if !errors.Is(err, errBlocked) {
		t.Fatalf("got %v, want %v", err, errBlocked)
	}

	if len(after.getCalls()) != 0 {
		t.Errorf("second plugin should not have been called, got %d calls", len(after.getCalls()))
	}
}

// blockingPlugin rejects BeforeToolCall with a fixed error.
type blockingPlugin struct {
	BasePlugin
	err error
}

func (b *blockingPlugin) BeforeToolCall(_ context.Context, _ ToolCallInfo) error {
	return b.err
}

func TestPluginChain_AfterModelCall_ShortCircuit(t *testing.T) {
	c := NewPluginChain()

	errBudget := errors.New("token budget exceeded")

	budget := &budgetPlugin{err: errBudget}
	observer := &recordingPlugin{}

	c.Register(budget)
	c.Register(observer)

	err := c.AfterModelCall(context.Background(), ModelCallResult{
		CumulativeUsage: UsageInfo{TotalTokens: 100_000},
	})
	if !errors.Is(err, errBudget) {
		t.Fatalf("got %v, want %v", err, errBudget)
	}
}

type budgetPlugin struct {
	BasePlugin
	err error
}

func (b *budgetPlugin) AfterModelCall(_ context.Context, _ ModelCallResult) error {
	return b.err
}

func TestPluginChain_ConcurrentCalls(t *testing.T) {
	c := NewPluginChain()

	var count atomic.Int32
	p := &countingPlugin{count: &count}
	c.Register(p)

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.OnOutputChunk(context.Background(), OutputChunkInfo{SessionID: "s1"})
		}()
	}
	wg.Wait()

	if count.Load() != 100 {
		t.Errorf("got %d calls, want 100", count.Load())
	}
}

type countingPlugin struct {
	BasePlugin
	count *atomic.Int32
}

func (p *countingPlugin) OnOutputChunk(_ context.Context, _ OutputChunkInfo) {
	p.count.Add(1)
}

func TestBasePlugin_Noop(t *testing.T) {
	var p BasePlugin
	ctx := context.Background()

	if err := p.OnSessionStart(ctx, SessionStartInfo{}); err != nil {
		t.Errorf("OnSessionStart: %v", err)
	}
	p.OnSessionEnd(ctx, SessionEndInfo{})
	if err := p.BeforeAgent(ctx, AgentInfo{}); err != nil {
		t.Errorf("BeforeAgent: %v", err)
	}
	p.AfterAgent(ctx, AgentInfo{})
	if err := p.BeforeModelCall(ctx, ModelCallInfo{}); err != nil {
		t.Errorf("BeforeModelCall: %v", err)
	}
	if err := p.AfterModelCall(ctx, ModelCallResult{}); err != nil {
		t.Errorf("AfterModelCall: %v", err)
	}
	if err := p.BeforeToolCall(ctx, ToolCallInfo{}); err != nil {
		t.Errorf("BeforeToolCall: %v", err)
	}
	p.AfterToolCall(ctx, ToolCallResult{})
	p.OnToolDispatched(ctx, ToolDispatchInfo{})
	p.OnToolResponse(ctx, ToolResponseInfo{})
	p.OnOutputChunk(ctx, OutputChunkInfo{})
	p.OnUserMessage(ctx, UserMessageInfo{})
	p.OnError(ctx, ErrorInfo{})
}

func TestPluginChain_TypedInfo(t *testing.T) {
	c := NewPluginChain()

	var gotInfo ToolCallInfo
	p := &infoCapture{capture: func(info ToolCallInfo) { gotInfo = info }}
	c.Register(p)

	expected := ToolCallInfo{
		SessionID: "sess-42",
		ToolName:  "read_file",
		AgentName: "coder",
		AgentPath: []string{"root", "coder"},
		Args:      map[string]any{"path": "/tmp/foo"},
	}

	if err := c.BeforeToolCall(context.Background(), expected); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotInfo.SessionID != expected.SessionID {
		t.Errorf("SessionID = %q, want %q", gotInfo.SessionID, expected.SessionID)
	}
	if gotInfo.ToolName != expected.ToolName {
		t.Errorf("ToolName = %q, want %q", gotInfo.ToolName, expected.ToolName)
	}
	if gotInfo.AgentName != expected.AgentName {
		t.Errorf("AgentName = %q, want %q", gotInfo.AgentName, expected.AgentName)
	}
	if gotInfo.Args["path"] != "/tmp/foo" {
		t.Errorf("Args[path] = %v, want /tmp/foo", gotInfo.Args["path"])
	}
}

type infoCapture struct {
	BasePlugin
	capture func(ToolCallInfo)
}

func (p *infoCapture) BeforeToolCall(_ context.Context, info ToolCallInfo) error {
	p.capture(info)
	return nil
}
