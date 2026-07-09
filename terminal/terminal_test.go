package terminal

import (
	"context"
	"iter"
	"testing"

	"github.com/apzuk3/agentd/internal/cli"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

func textEvent(author, text string, thought, partial bool) *session.Event {
	return &session.Event{
		Author: author,
		LLMResponse: model.LLMResponse{
			Partial: partial,
			Content: &genai.Content{
				Parts: []*genai.Part{{Text: text, Thought: thought}},
			},
		},
	}
}

func TestMapEventNilSafe(t *testing.T) {
	if got := mapEvent(nil); got != nil {
		t.Fatalf("mapEvent(nil) = %v, want nil", got)
	}
	if got := mapEvent(&session.Event{}); got != nil {
		t.Fatalf("mapEvent(empty) = %v, want nil", got)
	}
}

func TestMapEventPartialText(t *testing.T) {
	got := mapEvent(textEvent("writer", "hel", false, true))
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	se := got[0]
	if se.Kind != cli.EventText {
		t.Errorf("Kind = %v, want EventText", se.Kind)
	}
	if se.Text != "hel" {
		t.Errorf("Text = %q, want %q", se.Text, "hel")
	}
	if !se.Partial {
		t.Errorf("Partial = false, want true")
	}
	if se.Author != "writer" {
		t.Errorf("Author = %q, want %q", se.Author, "writer")
	}
}

func TestMapEventThought(t *testing.T) {
	got := mapEvent(textEvent("thinker", "pondering", true, true))
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].Kind != cli.EventThought {
		t.Errorf("Kind = %v, want EventThought", got[0].Kind)
	}
	for _, se := range got {
		if se.Kind == cli.EventFinal {
			t.Errorf("thought-only event produced a final: %+v", se)
		}
	}
}

func TestMapEventFinalText(t *testing.T) {
	got := mapEvent(textEvent("writer", "done", false, false))
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (text + final): %+v", len(got), got)
	}
	if got[0].Kind != cli.EventText || got[0].Text != "done" {
		t.Errorf("first event = %+v, want text 'done'", got[0])
	}
	if got[1].Kind != cli.EventFinal || got[1].Text != "done" {
		t.Errorf("second event = %+v, want final 'done'", got[1])
	}
}

func TestMapEventToolCall(t *testing.T) {
	ev := &session.Event{
		Author: "executor",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Parts: []*genai.Part{{
					FunctionCall: &genai.FunctionCall{
						Name: "app.deploy",
						Args: map[string]any{"ref": "main"},
					},
				}},
			},
		},
	}
	got := mapEvent(ev)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	se := got[0]
	if se.Kind != cli.EventToolCall {
		t.Errorf("Kind = %v, want EventToolCall", se.Kind)
	}
	if se.ToolName != "app.deploy" {
		t.Errorf("ToolName = %q, want %q", se.ToolName, "app.deploy")
	}
}

func TestMapEventToolResult(t *testing.T) {
	ev := &session.Event{
		Author: "executor",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						Name:     "app.deploy",
						Response: map[string]any{"url": "https://example.com"},
					},
				}},
			},
		},
	}
	got := mapEvent(ev)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	se := got[0]
	if se.Kind != cli.EventToolResult {
		t.Errorf("Kind = %v, want EventToolResult", se.Kind)
	}
	if se.ToolName != "app.deploy" {
		t.Errorf("ToolName = %q, want %q", se.ToolName, "app.deploy")
	}
}

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

func TestAgentToolNamesEmptyForNonLLM(t *testing.T) {
	child := newAgent(t, "child", "c")
	root := newAgent(t, "root", "r", child)
	if got := agentToolNames(root); len(got) != 0 {
		t.Errorf("agentToolNames(non-LLM tree) = %v, want empty", got)
	}
}

type stubLLM struct{}

func (stubLLM) Name() string { return "stub" }

func (stubLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {}
}

func mustNewToolForTest(t *testing.T, name string) tool.Tool {
	t.Helper()
	tr, err := functiontool.New(functiontool.Config{
		Name:        name,
		Description: "test " + name,
	}, func(ctx tool.Context, args map[string]any) (map[string]any, error) { return nil, nil })
	if err != nil {
		t.Fatalf("functiontool.New(%q): %v", name, err)
	}
	return tr
}

func TestAgentModelName(t *testing.T) {
	root, err := llmagent.New(llmagent.Config{
		Name:  "executor",
		Model: stubLLM{},
	})
	if err != nil {
		t.Fatalf("llmagent.New: %v", err)
	}
	if got := agentModelName(root); got != "stub" {
		t.Errorf("agentModelName(llmagent) = %q, want %q", got, "stub")
	}

	// A non-LLM agent has no model and must yield "".
	if got := agentModelName(newAgent(t, "plain", "p")); got != "" {
		t.Errorf("agentModelName(non-LLM) = %q, want empty", got)
	}
}

func TestAgentToolNamesSeesAttachedTools(t *testing.T) {
	t1 := mustNewToolForTest(t, "app.deploy")
	t2 := mustNewToolForTest(t, "db.query")

	root, err := llmagent.New(llmagent.Config{
		Name:  "executor",
		Model: stubLLM{},
		Tools: []tool.Tool{t1, t2},
	})
	if err != nil {
		t.Fatalf("llmagent.New: %v", err)
	}

	got := agentToolNames(root)
	want := []string{"app.deploy", "db.query"}
	if len(got) != len(want) {
		t.Fatalf("agentToolNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("agentToolNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
