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
	// EventError carries a non-fatal error encountered during the turn; Err is
	// set.
	EventError
)

// Event is a single thing that happened during a turn, delivered from a
// [TurnFunc] to the TUI.
type Event struct {
	Kind     EventKind
	Text     string
	ToolName string
	Partial  bool
	Author   string
	Err      error
}

// TurnFunc runs a single user turn. It sends [Event]s on out as they happen and
// returns when the turn is complete or ctx is cancelled. Implementations must
// not close out: the TUI owns the channel's lifecycle. A returned error is
// surfaced to the user as an [EventError].
type TurnFunc func(ctx context.Context, input string, out chan<- Event) error

// eventMsg wraps a single streamed [Event] for the bubbletea update loop.
type eventMsg struct{ ev Event }

// doneMsg signals that the current turn's event channel has been closed.
type doneMsg struct{}

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

// listen blocks for the next event on out and turns it into a message. When out
// is closed it yields a doneMsg, ending the per-turn read loop.
func listen(out chan Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-out
		if !ok {
			return doneMsg{}
		}
		return eventMsg{ev: ev}
	}
}
