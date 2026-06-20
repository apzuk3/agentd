---
name: agentd-add-launcher
description: Add a new launcher to the agentd package that wraps a google.golang.org/adk cmd/launcher sublauncher while keeping all ADK imports internal. Use when adding LaunchStreaming, LaunchSync, LaunchAPI, LaunchWeb, LaunchCLI, or any other Launch* / *Agent runtime entry point to agentd.
disable-model-invocation: true
---

# Adding a launcher to agentd

Launchers are the runtime layer of agentd: they take an `agent.Agent` and put
it in front of a user, an HTTP API, a web UI, or another program. The product
philosophy (see [AGENT.md](../../../AGENT.md)) is that callers import only
`github.com/apzuk3/agentd` — never `google.golang.org/adk/...`.

## The two launcher shapes

Every launcher in agentd is one of these two shapes. Pick the one that
matches.

### Shape A — wraps `runner.Runner.Run` directly (streaming primitive)

Use when the launcher controls its own output (CLI, Sync, Streaming).

Build on top of `agentd.LaunchStreaming`. Do **not** open a second path into
`runner.Runner.Run`; everything that streams must go through this single
primitive so behaviour stays consistent.

```go
// LaunchSync drains the stream to the final aggregated response.
func LaunchSync(ctx context.Context, a agent.Agent, input string, opts ...RunOption) (string, error) {
    var out strings.Builder
    for ev, err := range LaunchStreaming(ctx, a, input, opts...) {
        if err != nil { return out.String(), err }
        if ev.Kind == StreamEventFinal {
            out.WriteString(ev.Text)
        }
    }
    return out.String(), nil
}
```

### Shape B — thin proxy to an ADK `cmd/launcher` sublauncher

Use when the launcher serves HTTP (`LaunchAPI`, `LaunchWeb`). These share the
`ServerOption` set and the internal `resolveServer` / `runServer` engine in
[launch_server.go](../../../launch_server.go): build a `launcher.Config`, wrap
the root agent in `agent.NewSingleLoader` (or `agent.NewMultiLoader` for
sub-agents), and call `Execute` on the chosen ADK launcher.

Service selection uses named parameterless options that set unexported config
fields (never an `WithXService(svc)` taking an ADK type). Defaults are applied
by prepending default options so caller options override them:

```go
func newServerConfig(opts ...ServerOption) (*serverConfig, error) {
    all := append([]ServerOption{
        WithInMemorySessionService(),
        WithInMemoryArtifactService(),
    }, opts...)
    cfg := &serverConfig{}
    for _, o := range all {
        if err := o(cfg); err != nil { return nil, err }
    }
    return cfg, nil
}

func resolveServer(defaultArgs []string, root agent.Agent, opts ...ServerOption) (*launcher.Config, []string, error) {
    cfg, err := newServerConfig(opts...)
    if err != nil { return nil, nil, err }

    var loader agent.Loader = agent.NewSingleLoader(root)
    if len(cfg.subAgents) > 0 {
        if loader, err = agent.NewMultiLoader(root, cfg.subAgents...); err != nil { return nil, nil, err }
    }
    args := defaultArgs
    if cfg.args != nil { args = cfg.args }
    return &launcher.Config{
        SessionService:  cfg.sessionSvc,
        ArtifactService: cfg.artifactSvc,
        MemoryService:   cfg.memorySvc,
        AgentLoader:     loader,
    }, args, nil
}

// LaunchAPI/LaunchWeb just pick the ADK launcher + default args:
func LaunchAPI(root agent.Agent, opts ...ServerOption) error {
    return runServer(prod.NewLauncher(), []string{"api"}, root, opts...)
}
```

## Rules (do not break these)

1. **One import for callers.** Every `google.golang.org/adk/...` and
   `google.golang.org/genai` import lives inside the agentd package. If you
   need to expose an ADK type (e.g. `*session.Event`), wrap it in an
   agentd-owned struct first.
2. **Option-func pattern.** Public API is
   `func LaunchX(required..., ...XOption) error` (or returns the streaming
   iterator). `XOption = func(*xConfig) error`. Reuse the neutral, shared
   `RunOption` set (`WithUserID`, `WithSessionID`, `WithAppName`,
   `WithSessionService`) for session/identity rather than inventing a
   launcher-specific copy; add a dedicated `WithAPI…` / `WithWeb…` option type
   only for settings genuinely unique to that launcher. Never name an option
   after a transport detail (e.g. avoid a `WithStreamingMode`-style knob on a
   synchronous launcher).
3. **Sensible defaults.** Always provide an in-memory session service and a
   stable default app name (`"agentd"`) so the simple call works:
   `agentd.LaunchAPI(root)`.
4. **Streaming launchers go through `LaunchStreaming`.** No second
   `runner.Runner.Run` site.
5. **Godoc + runnable example for every exported symbol.** Match the bar
   already set by `WithFunctionTool*` in [tools.go](../../../tools.go).
6. **Build it.** Run `go build ./...` after the change.

## Reference: ADK launcher map

These are the ADK building blocks. Import them only from inside agentd.

| ADK package                       | What it gives you                             | Use for          |
|-----------------------------------|-----------------------------------------------|------------------|
| `cmd/launcher`                    | `Launcher`, `SubLauncher`, `Config`           | All shapes       |
| `cmd/launcher/prod`               | `NewLauncher()` = web + api + a2a             | `LaunchAPI`      |
| `cmd/launcher/full`               | `NewLauncher()` = console + web + webui + a2a | `LaunchWeb`      |
| `cmd/launcher/web/api`            | REST API sublauncher                          | composing custom |
| `cmd/launcher/web/webui`          | embedded Web UI sublauncher                   | composing custom |
| `agent.NewSingleLoader(a)`        | single-root `agent.Loader`                    | both shapes      |
| `agent.NewMultiLoader(root, ...)` | multi-agent `agent.Loader`                    | both shapes      |
| `runner.New(Config)` / `Run(...)` | core run loop, yields `*session.Event`        | shape A only     |
| `session.InMemoryService()`       | default session service                       | both shapes      |
| `agent.RunConfig{StreamingMode}`  | `StreamingModeNone` or `StreamingModeSSE`     | shape A only     |

## Workflow

1. Decide shape A or B based on the table above.
2. Add `launch_<name>.go` next to the existing agent files.
3. Define `<name>Config`, `<name>Option`, and `With<Name><Thing>` options.
4. Implement the launcher; for shape A consume `LaunchStreaming`, for shape
   B build `launcher.Config` and call `Execute`.
5. Add godoc with a complete runnable example (see `WithFunctionTool*`
   blocks in [tools.go](../../../tools.go)).
6. Update [AGENT.md](../../../AGENT.md) — move the launcher from "Known
   gaps" to its proper section and update the mermaid diagram if the
   topology changed.
7. `go build ./...`.

## Anti-patterns

- Returning raw `*session.Event` or `*genai.Content` from a public
  `agentd.Launch*` function — wrap it in `StreamEvent` instead.
- Accepting `tool.Tool` arguments — tools are always referenced by name from
  the registry.
- Constructing a second `runner.Runner` in a launcher when `LaunchStreaming`
  already does it.
- Requiring callers to pass an `agent.Loader` — accept `agent.Agent` (+
  optional sub-agents via an option) and build the loader internally.
