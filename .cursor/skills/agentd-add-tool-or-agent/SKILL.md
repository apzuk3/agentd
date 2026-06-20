---
name: agentd-add-tool-or-agent
description: Add a new tool option or agent constructor to the agentd package following the option-func pattern, global tool registry, and "callers import only agentd" philosophy. Use when adding WithFunctionTool*, WithLLMAgent*, WithLoopAgent*, a new agent kind, or any tool/agent helper.
disable-model-invocation: true
---

# Adding a tool option or agent constructor to agentd

This skill covers the two most common contributions to agentd: a new tool
option (`WithFunctionTool*`) and a new agent constructor or option
(`WithLLMAgent*` and friends). See [AGENT.md](../../../AGENT.md) for the
product philosophy.

## The non-negotiable rules

1. **Callers import only `github.com/apzuk3/agentd`.** Every
   `google.golang.org/adk/...` or `google.golang.org/genai` import lives
   inside agentd. Don't accept ADK types in public signatures unless there
   is no alternative (today only `agent.Agent` leaks; everything else
   should be wrapped).
2. **Option-func pattern.** Public signature:
   `func XxxConstructor(required..., ...XxxOption)`. Option type:
   `type XxxOption func(*xxxConfig) error`. Mirror the names already in the
   repo: `WithFunctionTool…`, `WithLLMAgent…`, `WithLoopAgent…`,
   `WithModel…`.
3. **Tools are referenced by name.** Public agent constructors take
   `[]string` tool names and resolve from `defaultToolRegistry`. Never
   accept `tool.Tool` directly.
4. **Godoc + runnable example for every exported symbol.** The
   `WithFunctionTool*` blocks in [tools.go](../../../tools.go) set the bar:
   explain *why* the option exists, when to reach for it, and show a
   complete `agentd.AddTool(...)` (or `agentd.LLMAgent(...)`) call.
5. **Build it.** `go build ./...` after the change.

## Adding a new tool option (`WithFunctionTool*`)

Add to [tools.go](../../../tools.go) next to the existing options.

Template:

```go
// WithFunctionToolX <one-line summary>.
//
// <Paragraph: what underlying ADK functiontool.Config field this sets,
// why a caller would reach for it, and what default behaviour it overrides.>
//
// Example:
//
//	agentd.AddTool("send_email", "Send an email", sendEmail,
//	    agentd.WithFunctionToolX(...),
//	)
func WithFunctionToolX(x XType) FnToolOption {
    return func(cfg *functiontool.Config) {
        cfg.X = x
    }
}
```

Checklist:

- [ ] Name starts with `WithFunctionTool` (consistent prefix).
- [ ] Sets a single field on `functiontool.Config`.
- [ ] Godoc includes a complete `agentd.AddTool(...)` example.
- [ ] If the option introduces a new ADK concept users need to understand
      (e.g. HITL confirmation), expand the long-form godoc block at the top
      of [tools.go](../../../tools.go) — don't make callers read ADK docs.
- [ ] `go build ./...` is clean.

## Adding a new option on an existing agent (`WithLLMAgent*` etc.)

Add to the matching `agent_*.go` file. Template:

```go
func WithLLMAgentY(y YType) LLMAgentOption {
    return func(cfg *llmagent.Config) error {
        cfg.Y = y
        return nil
    }
}
```

If the option resolves names from the global registry (like
`WithLLMAgentTools` does), follow that pattern: look up in
`defaultToolRegistry.tools[name]` and return
`fmt.Errorf("tool %s not found", name)` on miss.

## Adding a brand-new agent kind

Mirror the structure of [agent_loop.go](../../../agent_loop.go) — the
simplest existing example:

```go
package agentd

import (
    "google.golang.org/adk/agent"
    "google.golang.org/adk/agent/<subpkg>"
)

type FooAgentOption func(*foopkg.Config) error

func WithFooAgentBar(bar int) FooAgentOption {
    return func(cfg *foopkg.Config) error {
        cfg.Bar = bar
        return nil
    }
}

func FooAgent(name string, options ...FooAgentOption) (agent.Agent, error) {
    cfg := foopkg.Config{
        AgentConfig: agent.Config{Name: name},
        // sensible defaults here
    }
    for _, option := range options {
        if err := option(&cfg); err != nil {
            return nil, err
        }
    }
    return foopkg.New(cfg)
}
```

Then update [AGENT.md](../../../AGENT.md) "Agents" section to document the
new kind (rule of thumb: if it isn't in `AGENT.md`, it doesn't exist).

## Anti-patterns

- Accepting `tool.Tool`, `*llmagent.Config`, `*genai.Content`, or any ADK
  type in a public signature.
- Adding options that take a registry argument when the default global
  registry would do. Provide a registry-aware variant later, not now.
- Skipping the godoc example. The example is what makes the option
  discoverable — it shows up in editor hovers and pkg.go.dev.
- Returning silently on a bad input (e.g. unknown tool name). Always return
  a descriptive `error` from the option func.
- Touching `cli.go` to wire in a new agent kind. The CLI consumes any
  `agent.Agent` via `LaunchStreaming`; no special-casing needed.

## Final check

After any change, mentally diff the caller's import block against this:

```go
import "github.com/apzuk3/agentd"
```

If anything new needs to creep in, the option is wrong — fix the wrapper,
not the docs.
