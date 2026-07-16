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
	m := Model{tabs: []tab{{cfg: TabConfig{
		AgentName: "executor",
		AgentDesc: "does things",
		ModelName: "gemini-3.5-flash",
		Tools:     []string{"app.deploy"},
		SubAgents: []AgentInfo{{Name: "child", Depth: 0}},
		SessionID: "sess-1",
	}}}, active: 0}
	out := m.renderSidebar(sidebarWidth)

	for _, want := range []string{"AGENT INFO", "executor", "Model: gemini-3.5-flash", "STATUS", "CAPABILITIES", "app.deploy", "SUB-AGENTS", "child", "SESSION", "sess-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q:\n%s", want, out)
		}
	}
}

func TestRenderSidebarNoTools(t *testing.T) {
	m := Model{tabs: []tab{{cfg: TabConfig{AgentName: "a", SessionID: "s"}}}, active: 0}
	out := m.renderSidebar(sidebarWidth)
	if !strings.Contains(out, "No tools registered.") {
		t.Errorf("sidebar should note empty tools:\n%s", out)
	}
	if strings.Contains(out, "SUB-AGENTS") {
		t.Errorf("sidebar should omit SUB-AGENTS when there are none:\n%s", out)
	}
	if strings.Contains(out, "Model:") {
		t.Errorf("sidebar should omit Model line when unset:\n%s", out)
	}
}

func TestRenderMessagesIncludesSenders(t *testing.T) {
	m := Model{
		tabs: []tab{{
			cfg: TabConfig{AgentName: "executor"},
			messages: []message{
				{sender: "user", text: "hi"},
				{sender: "agent", text: "hello"},
				{sender: "system", text: "note"},
			},
		}},
		active: 0,
	}
	out := m.renderMessages(80)
	for _, want := range []string{"You:", "executor:", "hi", "hello", "note"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q:\n%s", want, out)
		}
	}
}

func TestFormatInt(t *testing.T) {
	tests := map[int]string{0: "0", 42: "42", 999: "999", 1000: "1,000", 12345: "12,345", 1234567: "1,234,567", -12345: "-12,345"}
	for in, want := range tests {
		if got := formatInt(in); got != want {
			t.Errorf("formatInt(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderSidebarShowsTokens(t *testing.T) {
	m := Model{tabs: []tab{{
		cfg:          TabConfig{AgentName: "a", SessionID: "s"},
		promptTokens: 1200,
		outputTokens: 345,
	}}, active: 0}
	out := m.renderSidebar(sidebarWidth)
	for _, want := range []string{"TOKENS", "1,200", "345", "1,545"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing token info %q:\n%s", want, out)
		}
	}
}

func TestRenderSubAgentBlock(t *testing.T) {
	m := Model{tabs: []tab{{
		cfg: TabConfig{AgentName: "root"},
		messages: []message{{
			sender:    "subagent",
			agentName: "researcher",
			text:      "found three options",
			inTokens:  1200,
			outTokens: 340,
		}},
	}}, active: 0}
	out := m.renderMessages(80)
	for _, want := range []string{"researcher", "found three options", "1,200", "340", "in", "out"} {
		if !strings.Contains(out, want) {
			t.Errorf("sub-agent block missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTabBar(t *testing.T) {
	m := Model{tabs: []tab{
		{cfg: TabConfig{AgentName: "alpha"}},
		{cfg: TabConfig{AgentName: "beta", ModelName: "gpt-5.5"}},
	}, active: 0}

	bar := m.renderTabBar(120)
	// Both agent names and the model name appear in the bar.
	for _, want := range []string{"alpha", "beta", "gpt-5.5"} {
		if !strings.Contains(bar, want) {
			t.Errorf("tab bar missing %q:\n%s", want, bar)
		}
	}
}
