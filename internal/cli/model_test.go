package cli

import "testing"

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
		m := Model{status: tt.status, statusDetail: tt.detail}
		if got := m.statusLabel(); got != tt.want {
			t.Errorf("statusLabel(%v) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestBusy(t *testing.T) {
	if (Model{status: statusIdle}).busy() {
		t.Error("idle model reported busy")
	}
	if !(Model{status: statusThinking}).busy() {
		t.Error("thinking model reported not busy")
	}
}

func TestAppendAgentTextPartialAccumulates(t *testing.T) {
	m := &Model{}
	m.appendAgentText("Hel", false, true)
	m.appendAgentText("lo", false, true)

	if len(m.messages) != 1 {
		t.Fatalf("got %d messages, want 1: %+v", len(m.messages), m.messages)
	}
	if m.messages[0].text != "Hello" {
		t.Errorf("text = %q, want %q", m.messages[0].text, "Hello")
	}
}

func TestAppendAgentTextFinalReplaces(t *testing.T) {
	m := &Model{}
	m.appendAgentText("Hel", false, true)
	m.appendAgentText("lo", false, true)
	// The non-partial final carries the full aggregated text and must replace,
	// not append, so the reply is not duplicated.
	m.appendAgentText("Hello", false, false)

	if len(m.messages) != 1 {
		t.Fatalf("got %d messages, want 1: %+v", len(m.messages), m.messages)
	}
	if m.messages[0].text != "Hello" {
		t.Errorf("text = %q, want %q", m.messages[0].text, "Hello")
	}
}

func TestAppendAgentTextThoughtAndReplySeparate(t *testing.T) {
	m := &Model{}
	m.appendAgentText("thinking", true, true)
	m.appendAgentText("answer", false, true)

	if len(m.messages) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(m.messages), m.messages)
	}
	if !m.messages[0].isThought || m.messages[0].text != "thinking" {
		t.Errorf("first message = %+v, want thought 'thinking'", m.messages[0])
	}
	if m.messages[1].isThought || m.messages[1].text != "answer" {
		t.Errorf("second message = %+v, want reply 'answer'", m.messages[1])
	}
}

func TestCancelTurnResetsState(t *testing.T) {
	called := false
	m := &Model{
		status:       statusThinking,
		statusDetail: "x",
		out:          make(chan Event),
		cancel:       func() { called = true },
	}
	m.cancelTurn()

	if !called {
		t.Error("cancelTurn did not invoke cancel func")
	}
	if m.busy() || m.out != nil || m.cancel != nil || m.statusDetail != "" {
		t.Errorf("cancelTurn left dirty state: %+v", m)
	}
}
