package cli

import (
	"strings"
	"testing"
)

func TestStatusLabel(t *testing.T) {
	tests := []struct {
		status status
		detail string
		want   string
	}{
		{statusIdle, "", "Idle"},
		{statusThinking, "", "Thinking..."},
		{statusToolRunning, "app.deploy", "Running app.deploy"},
	}
	for _, tt := range tests {
		tb := tab{status: tt.status, statusDetail: tt.detail}
		if got := tb.statusLabel(); got != tt.want {
			t.Errorf("statusLabel(%v) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestBusy(t *testing.T) {
	if (tab{status: statusIdle}).busy() {
		t.Error("idle tab reported busy")
	}
	if !(tab{status: statusThinking}).busy() {
		t.Error("thinking tab reported not busy")
	}
	// Model.busy() is true when ANY tab is busy.
	m := Model{tabs: []tab{{status: statusIdle}, {status: statusThinking}}}
	if !m.busy() {
		t.Error("model with one busy tab reported not busy")
	}
	idle := Model{tabs: []tab{{status: statusIdle}, {status: statusIdle}}}
	if idle.busy() {
		t.Error("model with all idle tabs reported busy")
	}
}

func TestAppendAgentTextPartialAccumulates(t *testing.T) {
	tr := &tab{}
	tr.appendAgentText("Hel", false, true)
	tr.appendAgentText("lo", false, true)

	if len(tr.messages) != 1 {
		t.Fatalf("got %d messages, want 1: %+v", len(tr.messages), tr.messages)
	}
	if tr.messages[0].text != "Hello" {
		t.Errorf("text = %q, want %q", tr.messages[0].text, "Hello")
	}
}

func TestAppendAgentTextFinalReplaces(t *testing.T) {
	tr := &tab{}
	tr.appendAgentText("Hel", false, true)
	tr.appendAgentText("lo", false, true)
	// The non-partial final carries the full aggregated text and must replace,
	// not append, so the reply is not duplicated.
	tr.appendAgentText("Hello", false, false)

	if len(tr.messages) != 1 {
		t.Fatalf("got %d messages, want 1: %+v", len(tr.messages), tr.messages)
	}
	if tr.messages[0].text != "Hello" {
		t.Errorf("text = %q, want %q", tr.messages[0].text, "Hello")
	}
}

func TestAppendAgentTextThoughtAndReplySeparate(t *testing.T) {
	tr := &tab{}
	tr.appendAgentText("thinking", true, true)
	tr.appendAgentText("answer", false, true)

	if len(tr.messages) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(tr.messages), tr.messages)
	}
	if !tr.messages[0].isThought || tr.messages[0].text != "thinking" {
		t.Errorf("first message = %+v, want thought 'thinking'", tr.messages[0])
	}
	if tr.messages[1].isThought || tr.messages[1].text != "answer" {
		t.Errorf("second message = %+v, want reply 'answer'", tr.messages[1])
	}
}

func TestToolCallsGroupIntoOneBlock(t *testing.T) {
	tr := &tab{}
	// Two calls with no agent text between them collapse into a single block.
	tr.appendToolCall(Event{ToolName: "check", ToolID: "a", ToolArgs: map[string]any{"domain": "nodist.com"}})
	tr.appendToolCall(Event{ToolName: "check", ToolID: "b", ToolArgs: map[string]any{"domain": "nobuzz.com"}})

	if len(tr.messages) != 1 || tr.messages[0].sender != "tool" {
		t.Fatalf("expected one tool block, got %+v", tr.messages)
	}
	if len(tr.messages[0].calls) != 2 {
		t.Fatalf("expected 2 calls in block, got %d", len(tr.messages[0].calls))
	}
	// A single-key arg map is summarised as just its value.
	if got := tr.messages[0].calls[0].args; got != "nodist.com" {
		t.Errorf("args = %q, want %q", got, "nodist.com")
	}
}

func TestAppendToolCallDedupesByID(t *testing.T) {
	tr := &tab{}
	// The same call re-arriving (streamed partial + final) must not double-render.
	tr.appendToolCall(Event{ToolName: "check", ToolID: "a", ToolArgs: map[string]any{"domain": "x.com"}})
	tr.appendToolCall(Event{ToolName: "check", ToolID: "a", ToolArgs: map[string]any{"domain": "x.com"}})

	if len(tr.messages[0].calls) != 1 {
		t.Fatalf("expected 1 deduped call, got %d", len(tr.messages[0].calls))
	}
	// And its result still resolves the single entry.
	tr.completeToolCall(Event{ToolName: "check", ToolID: "a", ToolResult: map[string]any{"error": "boom"}})
	if c := tr.messages[0].calls[0]; c.state != toolFailed {
		t.Errorf("call state = %v, want failed", c.state)
	}
}

func TestCompleteToolCallMatchesByID(t *testing.T) {
	tr := &tab{}
	tr.appendToolCall(Event{ToolName: "check", ToolID: "a"})
	tr.appendToolCall(Event{ToolName: "check", ToolID: "b"})
	// Result for the second call arrives first; it must match by ID, not order.
	tr.completeToolCall(Event{ToolName: "check", ToolID: "b", ToolResult: map[string]any{"available": true}})

	calls := tr.messages[0].calls
	if calls[0].state != toolRunning {
		t.Errorf("call a should still be running, got %v", calls[0].state)
	}
	if calls[1].state != toolDone || calls[1].result != "true" {
		t.Errorf("call b = %+v, want done with result 'true'", calls[1])
	}
}

func TestCompleteToolCallErrorMarksFailed(t *testing.T) {
	tr := &tab{}
	tr.appendToolCall(Event{ToolName: "check", ToolID: "a"})
	tr.completeToolCall(Event{ToolName: "check", ToolID: "a", ToolResult: map[string]any{"error": "boom"}})

	c := tr.messages[0].calls[0]
	if c.state != toolFailed || c.result != "boom" {
		t.Errorf("call = %+v, want failed with result 'boom'", c)
	}
}

func TestRenderToolBlockContents(t *testing.T) {
	m := Model{tabs: []tab{{
		messages: []message{{
			sender: "tool",
			calls: []toolCall{
				{name: "check", args: "nodist.com", result: "false", state: toolDone},
			},
		}},
	}}, active: 0}
	out := m.renderMessages(80)
	for _, want := range []string{"tool call", "check", "nodist.com", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("tool block missing %q:\n%s", want, out)
		}
	}
}

func TestTabResetClearsState(t *testing.T) {
	called := false
	tr := &tab{
		status:       statusThinking,
		statusDetail: "x",
		out:          make(chan Event),
		cancel:       func() { called = true },
	}
	tr.reset()

	if !called {
		t.Error("reset did not invoke cancel func")
	}
	if tr.busy() || tr.out != nil || tr.cancel != nil || tr.statusDetail != "" {
		t.Errorf("reset left dirty state: %+v", tr)
	}
}

func TestCancelAllResetsEveryTab(t *testing.T) {
	m := &Model{tabs: []tab{
		{status: statusThinking, out: make(chan Event), cancel: func() {}},
		{status: statusToolRunning, out: make(chan Event), cancel: func() {}},
	}}
	m.cancelAll()
	if m.busy() {
		t.Errorf("cancelAll left a busy tab: %+v", m.tabs)
	}
}

func TestHandleEventAccumulatesTokens(t *testing.T) {
	m := Model{tabs: []tab{{out: make(chan Event, 1)}}, active: 0}
	m2, _ := m.handleEvent(eventMsg{tab: 0, ev: Event{Kind: EventUsage, PromptTokens: 100, OutputTokens: 20}})
	m = m2.(Model)
	m3, _ := m.handleEvent(eventMsg{tab: 0, ev: Event{Kind: EventUsage, PromptTokens: 50, OutputTokens: 5}})
	m = m3.(Model)

	if got := m.tabs[0].promptTokens; got != 150 {
		t.Errorf("promptTokens = %d, want 150", got)
	}
	if got := m.tabs[0].outputTokens; got != 25 {
		t.Errorf("outputTokens = %d, want 25", got)
	}
}

func TestHandleEventRoutesToTab(t *testing.T) {
	m := Model{tabs: []tab{
		{out: make(chan Event, 1)},
		{out: make(chan Event, 1)},
	}, active: 0}
	// An event tagged for tab 1 must land only on tab 1.
	next, _ := m.handleEvent(eventMsg{tab: 1, ev: Event{Kind: EventText, Text: "hi", Partial: true}})
	nm := next.(Model)
	if len(nm.tabs[0].messages) != 0 {
		t.Errorf("tab 0 got messages it shouldn't: %+v", nm.tabs[0].messages)
	}
	if len(nm.tabs[1].messages) != 1 || nm.tabs[1].messages[0].text != "hi" {
		t.Errorf("tab 1 messages = %+v, want one 'hi'", nm.tabs[1].messages)
	}
}
