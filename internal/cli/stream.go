package cli

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// EventKind classifies a [Event]. It is the TUI's own, transport-agnostic
// vocabulary: the package deliberately knows nothing about agentd, ADK, or
// genai. The caller (agentd.LaunchCLI) adapts whatever stream it has into these
// events.
type EventKind int

const (
	// EventText is an incremental chunk of model reply text. While a reply is
	// still being streamed, Partial is true.
	EventText EventKind = iota
	// EventThought is an incremental chunk of the model's "thinking" text.
	EventThought
	// EventToolCall signals the model invoked a tool; ToolName is set.
	EventToolCall
	// EventToolResult signals a tool returned; ToolName is set.
	EventToolResult
	// EventFinal carries the full, aggregated reply text for a turn.
	EventFinal
	// EventUsage reports token usage for one model response; PromptTokens and
	// OutputTokens are set and accumulate into the tab's running totals.
	EventUsage
	// EventError carries a non-fatal error encountered during the turn; Err is
	// set.
	EventError
)

// Event is a single thing that happened during a turn, delivered from a
// [TurnFunc] to the TUI.
type Event struct {
	Kind EventKind
	Text string
	// ToolName / ToolID identify the tool for EventToolCall and EventToolResult.
	// ToolID (when the model populates it) is used to match a result back to its
	// originating call.
	ToolName string
	ToolID   string
	// ToolArgs carries the decoded call arguments (EventToolCall).
	ToolArgs map[string]any
	// ToolResult carries the decoded tool response (EventToolResult).
	ToolResult map[string]any
	// PromptTokens / OutputTokens carry token usage for EventUsage.
	PromptTokens int
	OutputTokens int
	Partial      bool
	Author       string
	Err          error
}

// TurnFunc runs a single user turn. It sends [Event]s on out as they happen and
// returns when the turn is complete or ctx is cancelled. Implementations must
// not close out: the TUI owns the channel's lifecycle. A returned error is
// surfaced to the user as an [EventError].
type TurnFunc func(ctx context.Context, input string, out chan<- Event) error

// eventMsg wraps a single streamed [Event] for the bubbletea update loop. tab
// identifies which tab's turn produced it, so a broadcast to N agents can route
// each event back to the right transcript.
type eventMsg struct {
	tab int
	ev  Event
}

// doneMsg signals that the tab's turn event channel has been closed.
type doneMsg struct{ tab int }

// runTurn starts the turn in a goroutine, funnelling its events into out and
// closing out when the turn finishes (or errors). Returning a nil message keeps
// it out of the update loop; progress is observed via listen.
func runTurn(ctx context.Context, turn TurnFunc, input string, out chan Event) tea.Cmd {
	return func() tea.Msg {
		go func() {
			defer close(out)
			if err := turn(ctx, input, out); err != nil {
				out <- Event{Kind: EventError, Err: err}
			}
		}()
		return nil
	}
}

// listen blocks for the next event on the given tab's channel and tags it with
// the tab index. When out is closed it yields a doneMsg for that tab, ending
// that tab's read loop. Each tab reschedules only its own listen, so the N
// broadcast loops stay independent.
func listen(tab int, out chan Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-out
		if !ok {
			return doneMsg{tab: tab}
		}
		return eventMsg{tab: tab, ev: ev}
	}
}
