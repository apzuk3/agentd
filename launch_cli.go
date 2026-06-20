package agentd

import (
	"context"
	"sort"

	"github.com/apzuk3/agentd/internal/cli"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
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
		Tools:     registeredToolNames(),
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

// registeredToolNames returns the sorted names of every tool in the default
// registry, for display in the CLI sidebar.
func registeredToolNames() []string {
	tools := make([]string, 0, len(defaultToolRegistry.tools))
	for name := range defaultToolRegistry.tools {
		tools = append(tools, name)
	}
	sort.Strings(tools)
	return tools
}
