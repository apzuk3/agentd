package terminal

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"unsafe"

	"github.com/apzuk3/agentd/internal/cli"
	"github.com/google/uuid"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// modelLLMType is the reflect type of the ADK model.LLM interface, used to
// locate an agent's model field regardless of its (unexported) name.
var modelLLMType = reflect.TypeOf((*model.LLM)(nil)).Elem()

// UI wraps one or more pure ADK agents to expose an interactive terminal chat.
// With multiple agents each gets its own tab; a submitted message is broadcast
// to every agent and each reply streams into its own tab.
type UI struct {
	agents []agent.Agent
}

// NewUI creates a new terminal UI for the given ADK agents. Passing a single
// agent yields a single-tab chat; passing several renders one tab per agent.
func NewUI(agents ...agent.Agent) *UI {
	return &UI{agents: agents}
}

// StartChat launches the interactive terminal chat loop. It blocks until the
// user exits the chat session.
func (ui *UI) StartChat(ctx context.Context) error {
	if len(ui.agents) == 0 {
		return fmt.Errorf("terminal: NewUI requires at least one agent")
	}

	userID := "agentd-user"
	appName := "agentd"
	// One shared in-memory store; each agent gets a distinct session ID so their
	// conversation histories never cross.
	sessionSvc := session.InMemoryService()

	tabs := make([]cli.TabConfig, 0, len(ui.agents))
	for _, a := range ui.agents {
		a := a
		sessionID := uuid.NewString()
		turn := func(ctx context.Context, input string, out chan<- cli.Event) error {
			r, err := runner.New(runner.Config{
				AppName:           appName,
				Agent:             a,
				SessionService:    sessionSvc,
				AutoCreateSession: true,
			})
			if err != nil {
				return err
			}

			msg := genai.NewContentFromText(input, genai.RoleUser)
			runCfg := agent.RunConfig{StreamingMode: agent.StreamingModeSSE}

			for ev, err := range r.Run(ctx, userID, sessionID, msg, runCfg) {
				if err != nil {
					return err
				}
				for _, se := range mapEvent(ev) {
					out <- se
				}
			}
			return nil
		}

		tabs = append(tabs, cli.TabConfig{
			AgentName: a.Name(),
			AgentDesc: a.Description(),
			ModelName: agentModelName(a),
			Tools:     agentToolNames(a),
			SubAgents: flattenSubAgents(a, 0),
			SessionID: sessionID,
			Turn:      turn,
		})
	}

	return cli.Run(cli.Config{Tabs: tabs})
}

// mapEvent converts a single ADK session.Event into zero or more cli.Events.
func mapEvent(ev *session.Event) []cli.Event {
	if ev == nil || ev.Content == nil {
		return nil
	}

	var out []cli.Event
	var turnText string

	for _, p := range ev.Content.Parts {
		switch {
		case p.Text != "" && p.Thought:
			out = append(out, cli.Event{
				Kind:    cli.EventThought,
				Text:    p.Text,
				Partial: ev.Partial,
				Author:  ev.Author,
			})
		case p.Text != "":
			turnText += p.Text
			out = append(out, cli.Event{
				Kind:    cli.EventText,
				Text:    p.Text,
				Partial: ev.Partial,
				Author:  ev.Author,
			})
		case p.FunctionCall != nil:
			out = append(out, cli.Event{
				Kind:     cli.EventToolCall,
				ToolName: p.FunctionCall.Name,
				ToolID:   p.FunctionCall.ID,
				ToolArgs: p.FunctionCall.Args,
				Author:   ev.Author,
			})
		case p.FunctionResponse != nil:
			out = append(out, cli.Event{
				Kind:       cli.EventToolResult,
				ToolName:   p.FunctionResponse.Name,
				ToolID:     p.FunctionResponse.ID,
				ToolResult: p.FunctionResponse.Response,
				Author:     ev.Author,
			})
		}
	}

	if ev.IsFinalResponse() && turnText != "" {
		out = append(out, cli.Event{
			Kind:   cli.EventFinal,
			Text:   turnText,
			Author: ev.Author,
		})
	}

	// Token usage rides on the model response, not a part. Count it once per
	// model call (non-partial events) so a turn's several LLM calls sum
	// correctly without double-counting streamed partials.
	if ev.UsageMetadata != nil && !ev.Partial {
		um := ev.UsageMetadata
		prompt := int(um.PromptTokenCount)
		output := int(um.CandidatesTokenCount + um.ThoughtsTokenCount)
		if prompt > 0 || output > 0 {
			out = append(out, cli.Event{
				Kind:         cli.EventUsage,
				PromptTokens: prompt,
				OutputTokens: output,
				Author:       ev.Author,
			})
		}
	}

	return out
}

// flattenSubAgents walks the agent tree depth-first into a flat list for the sidebar.
func flattenSubAgents(root agent.Agent, depth int) []cli.AgentInfo {
	var out []cli.AgentInfo
	for _, sa := range root.SubAgents() {
		out = append(out, cli.AgentInfo{
			Name:        sa.Name(),
			Description: sa.Description(),
			Depth:       depth,
		})
		out = append(out, flattenSubAgents(sa, depth+1)...)
	}
	return out
}

// agentModelName best-effort extracts the model name from an ADK agent. ADK
// stores the model on an unexported model.LLM field of the concrete agent type,
// so we locate the field by its exact interface type (name-independent) and read
// it through unsafe. It returns "" for agents without a model or if the ADK
// layout ever changes — callers must treat the name as optional.
func agentModelName(a agent.Agent) string {
	v := reflect.ValueOf(a)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct || !v.CanAddr() {
		return ""
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Type != modelLLMType {
			continue
		}
		fv := v.Field(i)
		// Read the (unexported) field via unsafe so we can call Name() on it.
		fv = reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem()
		if m, ok := fv.Interface().(model.LLM); ok && m != nil {
			return m.Name()
		}
	}
	return ""
}

// agentToolNames retrieves the sorted list of tools attached to any agent in the tree.
func agentToolNames(root agent.Agent) []string {
	seen := map[string]struct{}{}
	var walk func(agent.Agent)
	walk = func(a agent.Agent) {
		if a == nil {
			return
		}
		collectToolNames(reflect.ValueOf(a), seen)
		for _, sa := range a.SubAgents() {
			walk(sa)
		}
	}
	walk(root)

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// collectToolNames reflects into the agent struct to locate any attached tool.Tool objects.
func collectToolNames(v reflect.Value, seen map[string]struct{}) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)
		if !sf.IsExported() || !fv.CanInterface() {
			continue
		}
		if fv.Kind() == reflect.Slice {
			for j := 0; j < fv.Len(); j++ {
				item := fv.Index(j)
				if !item.CanInterface() || item.IsNil() {
					continue
				}
				if tt, ok := item.Interface().(tool.Tool); ok && tt != nil {
					if name := tt.Name(); name != "" {
						seen[name] = struct{}{}
					}
				}
				if ts, ok := item.Interface().(tool.Toolset); ok && ts != nil {
					if name := ts.Name(); name != "" {
						seen[name] = struct{}{}
					}
				}
			}
		}
		ft := sf.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			collectToolNames(fv, seen)
		}
	}
}
