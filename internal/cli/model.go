package cli

import (
	"context"
	"fmt"
	"sort"
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

// toolState is the lifecycle of a single tool invocation inside a tool block.
type toolState int

const (
	toolRunning toolState = iota
	toolDone
	toolFailed
)

// toolCall is one tool invocation rendered inside a grouped tool block. Calls
// start as toolRunning and are flipped to toolDone/toolFailed in place when
// their result arrives, so the block collapses a call+result pair into one line.
type toolCall struct {
	id     string
	name   string
	args   string // pre-summarised call arguments
	result string // pre-summarised response (or error text)
	state  toolState
}

// message is one entry in the transcript.
type message struct {
	sender    string // "user", "agent", "system", or "tool"
	text      string
	isThought bool
	calls     []toolCall // populated when sender == "tool"
}

// tab is one agent's transcript plus the lifecycle of its in-flight turn. A
// broadcast starts one turn per tab, each streaming into its own out channel.
type tab struct {
	cfg TabConfig

	messages     []message
	status       status
	statusDetail string // tool name while statusToolRunning

	promptTokens int // cumulative input tokens across the session
	outputTokens int // cumulative output tokens across the session

	out    chan Event         // events for the current turn (nil when idle)
	cancel context.CancelFunc // cancels the current turn (nil when idle)
}

// busy reports whether this tab's turn is still running.
func (t tab) busy() bool { return t.status != statusIdle }

// statusLabel is the human-readable status shown in the sidebar/loader.
func (t tab) statusLabel() string {
	switch t.status {
	case statusThinking:
		return "Thinking..."
	case statusToolRunning:
		return "Running " + t.statusDetail
	default:
		return "Idle"
	}
}

// reset returns the tab to idle, dropping any in-flight turn references. The
// pending listen still drains to doneMsg, which is harmless.
func (t *tab) reset() {
	if t.cancel != nil {
		t.cancel()
	}
	t.cancel = nil
	t.out = nil
	t.status = statusIdle
	t.statusDetail = ""
}

// Model is the bubbletea model for the agentd CLI. It owns the set of agent
// tabs, the shared input, and (via each tab) the lifecycle of the in-flight
// turns, but delegates all rendering to view.go and all stream plumbing to
// stream.go. The viewport and spinner are shared: the viewport always shows the
// active tab, and the single spinner ticks while any tab is busy.
type Model struct {
	cfg Config

	tabs   []tab
	active int // index of the tab currently shown

	viewport  viewport.Model
	textInput textinput.Model
	spinner   spinner.Model

	width     int
	height    int
	mainWidth int // transcript/input width (excludes sidebar + borders)

	mdRenderer    *glamour.TermRenderer
	rendererWidth int    // word-wrap width the current mdRenderer was built for
	mdStyle       string // glamour style detected once at startup
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

// busy reports whether any tab's turn is currently being processed.
func (m Model) busy() bool {
	for i := range m.tabs {
		if m.tabs[i].busy() {
			return true
		}
	}
	return false
}

// activeTab returns a pointer to the currently shown tab.
func (m *Model) activeTab() *tab {
	return &m.tabs[m.active]
}

// cancelAll cancels every in-flight turn and returns all tabs to idle.
func (m *Model) cancelAll() {
	for i := range m.tabs {
		m.tabs[i].reset()
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
			// While any turn is running, Ctrl+C cancels them all; otherwise quits.
			if m.busy() {
				m.cancelAll()
				m.activeTab().messages = append(m.activeTab().messages, message{sender: "system", text: "⏹ Cancelled."})
				m.refresh()
				return m, nil
			}
			return m, tea.Quit
		case tea.KeyEsc:
			m.cancelAll()
			return m, tea.Quit
		case tea.KeyTab:
			m.switchTab(m.active + 1)
			return m, nil
		case tea.KeyShiftTab:
			m.switchTab(m.active - 1)
			return m, nil
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
		return m.handleEvent(msg)

	case doneMsg:
		if msg.tab >= 0 && msg.tab < len(m.tabs) {
			m.tabs[msg.tab].reset()
			if msg.tab == m.active {
				m.refresh()
			}
		}
		return m, nil
	}

	m.textInput, cmd = m.textInput.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// switchTab makes idx (taken modulo the tab count) the active tab and scrolls
// its transcript to the bottom. Switching between tabs does not preserve scroll
// position — the active tab always renders at the bottom.
func (m *Model) switchTab(idx int) {
	n := len(m.tabs)
	if n == 0 {
		return
	}
	idx = ((idx % n) + n) % n
	if idx == m.active {
		return
	}
	m.active = idx
	m.refresh()
}

// submit broadcasts the current input to every tab, starting one turn per tab.
// Input is blocked while any tab is still busy.
func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.busy() {
		return m, nil
	}
	input := strings.TrimSpace(m.textInput.Value())
	if input == "" {
		return m, nil
	}

	m.textInput.SetValue("")

	cmds := make([]tea.Cmd, 0, 2*len(m.tabs)+1)
	for i := range m.tabs {
		t := &m.tabs[i]
		t.messages = append(t.messages, message{sender: "user", text: input})
		t.status = statusThinking

		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan Event, 16)
		t.out = out
		t.cancel = cancel

		cmds = append(cmds, runTurn(ctx, t.cfg.Turn, input, out), listen(i, out))
	}
	// One shared spinner drives the busy animation for all tabs.
	cmds = append(cmds, m.spinner.Tick)

	m.refresh()
	return m, tea.Batch(cmds...)
}

// handleEvent folds a single streamed event into its tab's transcript and
// schedules that tab's next read. Only the active tab triggers a re-render.
func (m Model) handleEvent(msg eventMsg) (tea.Model, tea.Cmd) {
	if msg.tab < 0 || msg.tab >= len(m.tabs) {
		return m, nil
	}
	t := &m.tabs[msg.tab]
	ev := msg.ev

	switch ev.Kind {
	case EventError:
		t.messages = append(t.messages, message{sender: "system", text: fmt.Sprintf("Error: %v", ev.Err)})
		t.status = statusIdle
	case EventToolCall:
		t.status = statusToolRunning
		t.statusDetail = ev.ToolName
		t.appendToolCall(ev)
	case EventToolResult:
		t.status = statusThinking
		t.statusDetail = ""
		t.completeToolCall(ev)
	case EventThought:
		t.appendAgentText(ev.Text, true, ev.Partial)
	case EventText:
		t.appendAgentText(ev.Text, false, ev.Partial)
	case EventFinal:
		// The aggregated final text mirrors the last non-partial chunk, so the
		// same merge logic keeps the transcript in sync without duplicating.
		t.appendAgentText(ev.Text, false, false)
	case EventUsage:
		t.promptTokens += ev.PromptTokens
		t.outputTokens += ev.OutputTokens
	}

	if msg.tab == m.active {
		m.refresh()
	}
	return m, listen(msg.tab, t.out)
}

// appendAgentText merges streamed agent text into the transcript. Partial chunks
// accumulate onto the trailing agent message; a non-partial chunk replaces it
// (the final event re-sends the full text), so we never double-render a reply.
func (t *tab) appendAgentText(text string, isThought, partial bool) {
	last := len(t.messages) - 1
	canMerge := last >= 0 && t.messages[last].sender == "agent" && t.messages[last].isThought == isThought

	if partial {
		if canMerge {
			t.messages[last].text += text
		} else {
			t.messages = append(t.messages, message{sender: "agent", text: text, isThought: isThought})
		}
		return
	}

	if canMerge {
		if text != "" {
			t.messages[last].text = text
		}
		return
	}
	if text != "" {
		t.messages = append(t.messages, message{sender: "agent", text: text, isThought: isThought})
	}
}

// appendToolCall records a new tool invocation. Consecutive calls (no agent
// text between them) accumulate into a single trailing "tool" block so a burst
// of calls renders as one compact panel rather than a wall of lines.
func (t *tab) appendToolCall(ev Event) {
	tc := toolCall{
		id:    ev.ToolID,
		name:  ev.ToolName,
		args:  summariseMap(ev.ToolArgs, argsMaxLen),
		state: toolRunning,
	}
	last := len(t.messages) - 1
	if last >= 0 && t.messages[last].sender == "tool" {
		// The same call can arrive more than once (streamed partial + final
		// event). Dedupe by ID so a call isn't rendered twice — refresh its args
		// in case the later copy is more complete.
		if ev.ToolID != "" {
			for i := range t.messages[last].calls {
				if t.messages[last].calls[i].id == ev.ToolID {
					if tc.args != "" {
						t.messages[last].calls[i].args = tc.args
					}
					return
				}
			}
		}
		t.messages[last].calls = append(t.messages[last].calls, tc)
		return
	}
	t.messages = append(t.messages, message{sender: "tool", calls: []toolCall{tc}})
}

// completeToolCall flips the matching running call in the most recent tool block
// to done/failed and attaches a summary of its result. Calls are matched by ID
// when the model supplies one, otherwise by name in FIFO order.
func (t *tab) completeToolCall(ev Event) {
	for i := len(t.messages) - 1; i >= 0; i-- {
		if t.messages[i].sender != "tool" {
			continue
		}
		calls := t.messages[i].calls
		for j := range calls {
			c := &calls[j]
			if c.state != toolRunning {
				continue
			}
			if (ev.ToolID != "" && c.id == ev.ToolID) || (ev.ToolID == "" && c.name == ev.ToolName) {
				c.result, c.state = summariseResult(ev.ToolResult)
				return
			}
		}
		return // only the most recent tool block can hold pending calls
	}
}

// summariseResult renders a tool response into a short line and its state. An
// "error" key (the ADK convention) flips the call to failed.
func summariseResult(resp map[string]any) (string, toolState) {
	if e, ok := resp["error"]; ok {
		if s := strings.TrimSpace(fmt.Sprint(e)); s != "" && s != "<nil>" {
			return truncateInline(s, resultMaxLen), toolFailed
		}
	}
	return summariseMap(resp, resultMaxLen), toolDone
}

// summariseMap flattens a key/value map into a compact inline string. A single
// entry is shown as just its value (e.g. "nodist.com"); multiple entries become
// "k=v, k=v", sorted for stable output. The whole thing is truncated to max.
func summariseMap(kv map[string]any, max int) string {
	if len(kv) == 0 {
		return ""
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	if len(keys) == 1 {
		parts = append(parts, fmt.Sprint(kv[keys[0]]))
	} else {
		for _, k := range keys {
			parts = append(parts, k+"="+fmt.Sprint(kv[k]))
		}
	}
	return truncateInline(strings.Join(parts, ", "), max)
}

// truncateInline collapses newlines and clips s to max runes with an ellipsis.
func truncateInline(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 1 {
		return ""
	}
	return string(r[:max-1]) + "…"
}

// refresh re-renders the active tab's transcript into the viewport and scrolls
// to the bottom.
func (m *Model) refresh() {
	m.viewport.SetContent(m.renderMessages(m.mainWidth))
	m.viewport.GotoBottom()
}
