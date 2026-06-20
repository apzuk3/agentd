package cli

import (
	"strings"
	"testing"
)

func TestComputeMainWidth(t *testing.T) {
	// Wide terminal: sidebar fits, main column is total minus the sidebar reserve.
	if got := computeMainWidth(120); got != 120-sidebarReserve {
		t.Errorf("computeMainWidth(120) = %d, want %d", got, 120-sidebarReserve)
	}
	// Narrow terminal: sidebar dropped, main column is total minus chrome.
	if got := computeMainWidth(20); got != 20-chromeReserve {
		t.Errorf("computeMainWidth(20) = %d, want %d", got, 20-chromeReserve)
	}
}

func TestRenderSidebarSections(t *testing.T) {
	m := Model{cfg: Config{
		AgentName: "executor",
		AgentDesc: "does things",
		Tools:     []string{"app.deploy"},
		SubAgents: []AgentInfo{{Name: "child", Depth: 0}},
		SessionID: "sess-1",
	}}
	out := m.renderSidebar(sidebarWidth)

	for _, want := range []string{"AGENT INFO", "executor", "STATUS", "CAPABILITIES", "app.deploy", "SUB-AGENTS", "child", "SESSION", "sess-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q:\n%s", want, out)
		}
	}
}

func TestRenderSidebarNoTools(t *testing.T) {
	m := Model{cfg: Config{AgentName: "a", SessionID: "s"}}
	out := m.renderSidebar(sidebarWidth)
	if !strings.Contains(out, "No tools registered.") {
		t.Errorf("sidebar should note empty tools:\n%s", out)
	}
	if strings.Contains(out, "SUB-AGENTS") {
		t.Errorf("sidebar should omit SUB-AGENTS when there are none:\n%s", out)
	}
}

func TestRenderMessagesIncludesSenders(t *testing.T) {
	m := Model{
		cfg: Config{AgentName: "executor"},
		messages: []message{
			{sender: "user", text: "hi"},
			{sender: "agent", text: "hello"},
			{sender: "system", text: "note"},
		},
	}
	out := m.renderMessages(80)
	for _, want := range []string{"You:", "executor:", "hi", "hello", "note"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q:\n%s", want, out)
		}
	}
}
