package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

// status is the agent's current processing state for a turn. It replaces the
// stringly-typed status the original CLI compared against "Idle"/"Thinking...".
type status int

const (
	statusIdle status = iota
	statusThinking
	statusToolRunning
)

// message is one entry in the transcript.
type message struct {
	sender    string // "user", "agent", or "system"
	text      string
	isThought bool
}

// Model is the bubbletea model for the agentd CLI. It owns transcript state,
// input, and the lifecycle of the in-flight turn, but delegates all rendering to
// view.go and all stream plumbing to stream.go.
type Model struct {
	cfg Config

	viewport  viewport.Model
	textInput textinput.Model
	spinner   spinner.Model
	messages  []message

	status       status
	statusDetail string // tool name while statusToolRunning

	width     int
	height    int
	mainWidth int // transcript/input width (excludes sidebar + borders)

	out    chan Event         // events for the current turn (nil when idle)
	cancel context.CancelFunc // cancels the current turn (nil when idle)

	mdRenderer    *glamour.TermRenderer
	rendererWidth int    // word-wrap width the current mdRenderer was built for
	mdStyle       string // glamour style detected once at startup
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

// busy reports whether a turn is currently being processed.
func (m Model) busy() bool {
	return m.status != statusIdle
}

// statusLabel is the human-readable status shown in the sidebar/loader.
func (m Model) statusLabel() string {
	switch m.status {
	case statusThinking:
		return "Thinking..."
	case statusToolRunning:
		return "Running " + m.statusDetail
	default:
		return "Idle"
	}
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			// While a turn is running, Ctrl+C cancels it; otherwise it quits.
			if m.busy() {
				m.cancelTurn()
				m.messages = append(m.messages, message{sender: "system", text: "⏹ Cancelled."})
				m.refresh()
				return m, nil
			}
			return m, tea.Quit
		case tea.KeyEsc:
			m.cancelTurn()
			return m, tea.Quit
		case tea.KeyPgUp, tea.KeyPgDown:
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case tea.KeyEnter:
			return m.submit()
		}

	case spinner.TickMsg:
		if !m.busy() {
			return m, nil
		}
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.mainWidth = computeMainWidth(m.width)
		m.ensureRenderer(m.mainWidth - 4)
		m.viewport.Width = m.mainWidth
		m.viewport.Height = m.viewportHeight()
		m.refresh()

	case eventMsg:
		return m.handleEvent(msg.ev)

	case doneMsg:
		m.status = statusIdle
		m.statusDetail = ""
		m.out = nil
		m.cancel = nil
		m.refresh()
		return m, nil
	}

	m.textInput, cmd = m.textInput.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// submit starts a new turn from the current input value.
func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.busy() {
		return m, nil
	}
	input := strings.TrimSpace(m.textInput.Value())
	if input == "" {
		return m, nil
	}

	m.messages = append(m.messages, message{sender: "user", text: input})
	m.textInput.SetValue("")
	m.status = statusThinking

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Event, 16)
	m.out = out
	m.cancel = cancel

	m.refresh()

	return m, tea.Batch(
		runTurn(ctx, m.cfg.Turn, input, out),
		listen(out),
		m.spinner.Tick,
	)
}

// handleEvent folds a single streamed event into the transcript and schedules
// the next read.
func (m Model) handleEvent(ev Event) (tea.Model, tea.Cmd) {
	switch ev.Kind {
	case EventError:
		m.messages = append(m.messages, message{sender: "system", text: fmt.Sprintf("Error: %v", ev.Err)})
		m.status = statusIdle
	case EventToolCall:
		m.status = statusToolRunning
		m.statusDetail = ev.ToolName
		m.messages = append(m.messages, message{sender: "system", text: fmt.Sprintf("🔧 Running tool %s...", ev.ToolName)})
	case EventToolResult:
		m.status = statusThinking
		m.statusDetail = ""
		m.messages = append(m.messages, message{sender: "system", text: fmt.Sprintf("✓ Tool %s completed successfully", ev.ToolName)})
	case EventThought:
		m.appendAgentText(ev.Text, true, ev.Partial)
	case EventText:
		m.appendAgentText(ev.Text, false, ev.Partial)
	case EventFinal:
		// The aggregated final text mirrors the last non-partial chunk, so the
		// same merge logic keeps the transcript in sync without duplicating.
		m.appendAgentText(ev.Text, false, false)
	}

	m.refresh()
	return m, listen(m.out)
}

// appendAgentText merges streamed agent text into the transcript. Partial chunks
// accumulate onto the trailing agent message; a non-partial chunk replaces it
// (the final event re-sends the full text), so we never double-render a reply.
func (m *Model) appendAgentText(text string, isThought, partial bool) {
	last := len(m.messages) - 1
	canMerge := last >= 0 && m.messages[last].sender == "agent" && m.messages[last].isThought == isThought

	if partial {
		if canMerge {
			m.messages[last].text += text
		} else {
			m.messages = append(m.messages, message{sender: "agent", text: text, isThought: isThought})
		}
		return
	}

	if canMerge {
		if text != "" {
			m.messages[last].text = text
		}
		return
	}
	if text != "" {
		m.messages = append(m.messages, message{sender: "agent", text: text, isThought: isThought})
	}
}

// cancelTurn cancels the in-flight turn (if any) and resets turn state. The
// pending listen still drains to doneMsg, which is harmless.
func (m *Model) cancelTurn() {
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
	m.out = nil
	m.status = statusIdle
	m.statusDetail = ""
}

// refresh re-renders the transcript into the viewport and scrolls to the bottom.
func (m *Model) refresh() {
	m.viewport.SetContent(m.renderMessages(m.mainWidth))
	m.viewport.GotoBottom()
}
