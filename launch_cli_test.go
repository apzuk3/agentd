package agentd

import (
	"testing"

	"github.com/apzuk3/agentd/internal/cli"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
)

func TestToCLIEvent(t *testing.T) {
	tests := []struct {
		name string
		in   *StreamEvent
		want cli.EventKind
	}{
		{"text", &StreamEvent{Kind: StreamEventText}, cli.EventText},
		{"thought", &StreamEvent{Kind: StreamEventThought}, cli.EventThought},
		{"toolcall", &StreamEvent{Kind: StreamEventToolCall}, cli.EventToolCall},
		{"toolresult", &StreamEvent{Kind: StreamEventToolResult}, cli.EventToolResult},
		{"final", &StreamEvent{Kind: StreamEventFinal}, cli.EventFinal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toCLIEvent(tt.in); got.Kind != tt.want {
				t.Errorf("toCLIEvent(%v).Kind = %v, want %v", tt.in.Kind, got.Kind, tt.want)
			}
		})
	}
}

func TestToCLIEventCopiesFields(t *testing.T) {
	in := &StreamEvent{
		Kind:     StreamEventToolCall,
		Text:     "hi",
		ToolName: "app.deploy",
		Partial:  true,
		Author:   "executor",
	}
	got := toCLIEvent(in)
	if got.Text != "hi" || got.ToolName != "app.deploy" || !got.Partial || got.Author != "executor" {
		t.Errorf("toCLIEvent did not copy fields: %+v", got)
	}
}

// newAgent builds a leaf agent. agent.Agent has an unexported method, so tests
// must construct real agents via agent.New rather than a hand-rolled stub.
func newAgent(t *testing.T, name, desc string, subs ...agent.Agent) agent.Agent {
	t.Helper()
	a, err := agent.New(agent.Config{Name: name, Description: desc, SubAgents: subs})
	if err != nil {
		t.Fatalf("agent.New(%q): %v", name, err)
	}
	return a
}

func TestFlattenSubAgents(t *testing.T) {
	grandchild := newAgent(t, "grandchild", "gc")
	child1 := newAgent(t, "child1", "c1", grandchild)
	child2 := newAgent(t, "child2", "c2")
	root := newAgent(t, "root", "r", child1, child2)

	got := flattenSubAgents(root, 0)

	want := []cli.AgentInfo{
		{Name: "child1", Description: "c1", Depth: 0},
		{Name: "grandchild", Description: "gc", Depth: 1},
		{Name: "child2", Description: "c2", Depth: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("flattenSubAgents len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("flattenSubAgents[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFlattenSubAgentsLeaf(t *testing.T) {
	if got := flattenSubAgents(newAgent(t, "solo", "s"), 0); got != nil {
		t.Errorf("flattenSubAgents(leaf) = %+v, want nil", got)
	}
}

func TestWithSessionServiceSetsService(t *testing.T) {
	c := defaultRunConfig()
	svc := session.InMemoryService()
	if err := withSessionService(svc)(c); err != nil {
		t.Fatalf("withSessionService: %v", err)
	}
	if c.sessionSvc != svc {
		t.Errorf("sessionSvc = %v, want the injected service", c.sessionSvc)
	}
}

func TestRegisteredToolNamesSorted(t *testing.T) {
	got := registeredToolNames()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("registeredToolNames not sorted at %d: %v", i, got)
		}
	}
}
