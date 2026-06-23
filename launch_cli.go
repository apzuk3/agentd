package agentd

import (
	"context"
	"reflect"
	"sort"

	"github.com/apzuk3/agentd/internal/cli"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// withSessionService pins a specific session service for the run. It is the
// unexported counterpart to the public [RunOption] set: it lets [LaunchCLI]
// reuse one in-memory session store (and one session ID) across every turn so a
// conversation keeps its history, while keeping the public option set free of
// ADK service types (see AGENT.md).
func withSessionService(svc session.Service) RunOption {
	return func(c *runConfig) error {
		c.sessionSvc = svc
		return nil
	}
}

// LaunchCLI starts an interactive terminal chat for the agent, built entirely on
// top of [LaunchStreaming]. It is the streaming-based launcher counterpart to
// [LaunchSync] and the HTTP launchers, and reuses the same neutral [RunOption]
// set ([WithUserID], [WithSessionID], [WithAppName]).
//
// Unlike a one-shot launcher, the CLI is multi-turn: it resolves the identity
// and session ID once and reuses a single in-memory session store for the whole
// session, so the agent remembers earlier turns. Every turn still flows through
// [LaunchStreaming] — there is no second path into the runner. The terminal UI
// (a bubbletea TUI) lives in an internal package and consumes only agentd's
// ADK-free [StreamEvent]s.
//
// It blocks until the user quits (Esc, or Ctrl+C while idle). Ctrl+C during a
// turn cancels that turn instead of quitting. Any error from the TUI runtime is
// returned.
//
// Example:
//
//	model, err := agentd.NewModel(context.Background(), agentd.ModelGeminiFlash35)
//	if err != nil {
//		log.Fatal(err)
//	}
//	root, err := agentd.LLMAgent("executor", model,
//		agentd.WithLLMAgentInstruction("Help the user deploy apps."),
//	)
//	if err != nil {
//		log.Fatal(err)
//	}
//	if err := agentd.LaunchCLI(root); err != nil {
//		log.Fatal(err)
//	}
func LaunchCLI(root agent.Agent, opts ...RunOption) error {
	// Resolve identity/session once so every turn shares the same values.
	cfg := defaultRunConfig()
	for _, o := range opts {
		if err := o(cfg); err != nil {
			return err
		}
	}

	// A single session store for the whole CLI lifetime gives multi-turn
	// memory: the runner does get-or-create per turn against the same store.
	svc := session.InMemoryService()
	turnOpts := []RunOption{
		WithUserID(cfg.userID),
		WithSessionID(cfg.sessionID),
		WithAppName(cfg.appName),
		withSessionService(svc),
	}

	turn := func(ctx context.Context, input string, out chan<- cli.Event) error {
		for ev, err := range LaunchStreaming(ctx, root, input, turnOpts...) {
			if err != nil {
				return err
			}
			out <- toCLIEvent(ev)
		}
		return nil
	}

	return cli.Run(cli.Config{
		AgentName: root.Name(),
		AgentDesc: root.Description(),
		Tools:     agentToolNames(root),
		SubAgents: flattenSubAgents(root, 0),
		SessionID: cfg.sessionID,
		Turn:      turn,
	})
}

// toCLIEvent maps an agentd StreamEvent onto the TUI's neutral event type.
func toCLIEvent(ev *StreamEvent) cli.Event {
	out := cli.Event{
		Text:     ev.Text,
		ToolName: ev.ToolName,
		Partial:  ev.Partial,
		Author:   ev.Author,
	}
	switch ev.Kind {
	case StreamEventText:
		out.Kind = cli.EventText
	case StreamEventThought:
		out.Kind = cli.EventThought
	case StreamEventToolCall:
		out.Kind = cli.EventToolCall
	case StreamEventToolResult:
		out.Kind = cli.EventToolResult
	case StreamEventFinal:
		out.Kind = cli.EventFinal
	}
	return out
}

// flattenSubAgents walks the agent tree under root depth-first into a flat list
// for the sidebar. root itself is not included; depth is the indentation level
// of root's direct children.
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

// agentToolNames walks the full agent tree (root and all sub-agents) and
// returns the sorted unique names of tools statically attached to any LLMAgent
// inside it. This is used for the CAPABILITIES list in the CLI sidebar so that
// only tools actually given to the agent (via WithLLMAgentTools etc.) are shown,
// not every tool present in the global registry.
//
// It uses reflection to reach the (exported) tool list fields on the concrete
// ADK agent implementations without depending on unexported ADK internal packages.
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

// collectToolNames scans a reflect.Value (following pointers/interfaces and
// descending into exported struct fields) looking for slices that contain
// tool.Tool values and records their .Name()s.
func collectToolNames(v reflect.Value, seen map[string]struct{}) {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
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
				if !item.CanInterface() {
					continue
				}
				if item.IsNil() {
					// could be typed nil for interface slice
					// still try Interface
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
		// Recurse into struct fields (covers the exported "State" field holding Tools,
		// embedded bases, and future wrappers).
		ft := sf.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			collectToolNames(fv, seen)
		}
	}
}
