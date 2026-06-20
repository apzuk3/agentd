package agentd

import (
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
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
	if se.Kind != StreamEventText {
		t.Errorf("Kind = %v, want StreamEventText", se.Kind)
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
	if got[0].Kind != StreamEventThought {
		t.Errorf("Kind = %v, want StreamEventThought", got[0].Kind)
	}
	// A thought is never an answer, so it must not produce a final.
	for _, se := range got {
		if se.Kind == StreamEventFinal {
			t.Errorf("thought-only event produced a final: %+v", se)
		}
	}
}

func TestMapEventFinalText(t *testing.T) {
	// Non-partial text with no tool calls is a final response.
	got := mapEvent(textEvent("writer", "done", false, false))
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (text + final): %+v", len(got), got)
	}
	if got[0].Kind != StreamEventText || got[0].Text != "done" {
		t.Errorf("first event = %+v, want text 'done'", got[0])
	}
	if got[1].Kind != StreamEventFinal || got[1].Text != "done" {
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
	if se.Kind != StreamEventToolCall {
		t.Errorf("Kind = %v, want StreamEventToolCall", se.Kind)
	}
	if se.ToolName != "app.deploy" {
		t.Errorf("ToolName = %q, want %q", se.ToolName, "app.deploy")
	}
	if se.ToolArgs["ref"] != "main" {
		t.Errorf("ToolArgs = %v, want ref=main", se.ToolArgs)
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
	if se.Kind != StreamEventToolResult {
		t.Errorf("Kind = %v, want StreamEventToolResult", se.Kind)
	}
	if se.ToolName != "app.deploy" {
		t.Errorf("ToolName = %q, want %q", se.ToolName, "app.deploy")
	}
	data, ok := se.ToolData.(map[string]any)
	if !ok || data["url"] != "https://example.com" {
		t.Errorf("ToolData = %v, want url=https://example.com", se.ToolData)
	}
}

func TestMapEventToolCallEventHasNoFinal(t *testing.T) {
	// An event that only carries a function call must not be a final response,
	// even though it has no partial text.
	ev := &session.Event{
		Author: "executor",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Parts: []*genai.Part{{
					FunctionCall: &genai.FunctionCall{Name: "t"},
				}},
			},
		},
	}
	for _, se := range mapEvent(ev) {
		if se.Kind == StreamEventFinal {
			t.Errorf("tool-call event produced a final: %+v", se)
		}
	}
}

func TestMapEventMultiPartFinalAggregates(t *testing.T) {
	ev := &session.Event{
		Author: "writer",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Parts: []*genai.Part{
					{Text: "Hello "},
					{Text: "world"},
				},
			},
		},
	}
	got := mapEvent(ev)
	// two text parts + one aggregated final
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(got), got)
	}
	final := got[len(got)-1]
	if final.Kind != StreamEventFinal {
		t.Fatalf("last event = %v, want StreamEventFinal", final.Kind)
	}
	if final.Text != "Hello world" {
		t.Errorf("final Text = %q, want %q", final.Text, "Hello world")
	}
}
