package agentd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

type message struct {
	sender    string // "user", "agent", "system"
	text      string
	isThought bool
}

type eventMsg struct {
	event *session.Event
	err   error
}

type doneMsg struct{}

type streamChan chan eventMsg

func runAgent(r *runner.Runner, sessionID string, userID string, input string, ch streamChan) tea.Cmd {
	return func() tea.Msg {
		go func() {
			defer close(ch)
			ctx := context.Background()
			msg := &genai.Content{
				Parts: []*genai.Part{
					{
						Text: input,
					},
				},
			}
			runCfg := agent.RunConfig{}
			events := r.Run(ctx, userID, sessionID, msg, runCfg)
			for ev, err := range events {
				ch <- eventMsg{event: ev, err: err}
				if err != nil {
					break
				}
			}
		}()
		return nil
	}
}

func readStream(ch streamChan) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		ev, ok := <-ch
		if !ok {
			return doneMsg{}
		}
		return ev
	}
}

type mainModel struct {
	agent       agent.Agent
	runner      *runner.Runner
	sessionID   string
	userID      string
	activeTools []string

	// UI State
	viewport      viewport.Model
	textInput     textinput.Model
	spinner       spinner.Model
	messages      []message
	status        string // "Idle", "Thinking...", "Running [tool]..."
	width         int
	height        int
	mainWidth     int // computed transcript/input width (excludes sidebar + borders)
	streamCh      streamChan
	mdRenderer    *glamour.TermRenderer
	rendererWidth int // word-wrap width the current mdRenderer was built for
}

func (m mainModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

// busy reports whether the agent is currently processing a turn.
func (m mainModel) busy() bool {
	return m.status != "Idle"
}

// ensureRenderer (re)builds the markdown renderer when the wrap width changes.
func (m *mainModel) ensureRenderer(width int) {
	if width < 10 {
		width = 10
	}
	if m.mdRenderer != nil && m.rendererWidth == width {
		return
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		m.mdRenderer = nil
		return
	}
	m.mdRenderer = r
	m.rendererWidth = width
}

func (m mainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyPgUp, tea.KeyPgDown:
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case tea.KeyEnter:
			if m.status != "Idle" {
				return m, nil
			}
			input := strings.TrimSpace(m.textInput.Value())
			if input == "" {
				return m, nil
			}

			// Add user message to history
			m.messages = append(m.messages, message{sender: "user", text: input})
			m.textInput.SetValue("")
			m.status = "Thinking..."

			ch := make(streamChan, 16)
			m.streamCh = ch

			// Scroll viewport to bottom
			m.viewport.SetContent(m.renderMessages(m.mainWidth))
			m.viewport.GotoBottom()

			return m, tea.Batch(
				runAgent(m.runner, m.sessionID, m.userID, input, ch),
				readStream(ch),
				m.spinner.Tick,
			)
		}

	case spinner.TickMsg:
		// Only keep the loader animating while the agent is working.
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

		// Initialize viewport
		m.viewport.Width = m.mainWidth
		m.viewport.Height = m.height - 5
		m.viewport.SetContent(m.renderMessages(m.mainWidth))

	case eventMsg:
		if msg.err != nil {
			m.messages = append(m.messages, message{sender: "system", text: fmt.Sprintf("Error: %v", msg.err)})
			m.status = "Idle"
			m.streamCh = nil
			m.viewport.SetContent(m.renderMessages(m.mainWidth))
			m.viewport.GotoBottom()
			return m, nil
		}

		ev := msg.event
		if ev == nil {
			return m, readStream(m.streamCh)
		}

		if ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" {
					if ev.Partial {
						// Stream partial update
						if len(m.messages) > 0 && m.messages[len(m.messages)-1].sender == "agent" && m.messages[len(m.messages)-1].isThought == p.Thought {
							m.messages[len(m.messages)-1].text += p.Text
						} else {
							m.messages = append(m.messages, message{sender: "agent", text: p.Text, isThought: p.Thought})
						}
					} else {
						// Final turn response
						if len(m.messages) > 0 && m.messages[len(m.messages)-1].sender == "agent" && m.messages[len(m.messages)-1].isThought == p.Thought {
							// If the final event has non-empty text, make sure we are fully in sync
							if p.Text != "" {
								m.messages[len(m.messages)-1].text = p.Text
							}
						} else {
							m.messages = append(m.messages, message{sender: "agent", text: p.Text, isThought: p.Thought})
						}
					}
				}

				if p.FunctionCall != nil {
					m.status = "Running " + p.FunctionCall.Name
					m.messages = append(m.messages, message{
						sender: "system",
						text:   fmt.Sprintf("🔧 Running tool %s...", p.FunctionCall.Name),
					})
				}

				if p.FunctionResponse != nil {
					m.status = "Thinking..."
					m.messages = append(m.messages, message{
						sender: "system",
						text:   fmt.Sprintf("✓ Tool %s completed successfully", p.FunctionResponse.Name),
					})
				}
			}
		}

		m.viewport.SetContent(m.renderMessages(m.mainWidth))
		m.viewport.GotoBottom()
		return m, readStream(m.streamCh)

	case doneMsg:
		m.status = "Idle"
		m.streamCh = nil
		m.viewport.SetContent(m.renderMessages(m.mainWidth))
		m.viewport.GotoBottom()
		return m, nil
	}

	m.textInput, cmd = m.textInput.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// computeMainWidth derives the transcript/input column width from the total
// terminal width, accounting for the sidebar and borders. Falls back to a
// near-full-width layout (sidebar hidden) on narrow terminals.
func computeMainWidth(total int) int {
	const sidebarWidth = 30
	mw := total - sidebarWidth - 3
	if mw < 30 {
		mw = total - 2
	}
	return mw
}

func (m mainModel) renderMessages(width int) string {
	if width < 10 {
		width = 10
	}
	var sb strings.Builder
	for _, msg := range m.messages {
		switch msg.sender {
		case "user":
			userStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("86")). // Cyan
				Bold(true)
			sb.WriteString(userStyle.Render(" You: ") + "\n")
			textStyle := lipgloss.NewStyle().
				PaddingLeft(2).
				Width(width - 4)
			sb.WriteString(textStyle.Render(msg.text) + "\n\n")
		case "agent":
			agentStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("211")). // Pink/Salmon
				Bold(true)
			sb.WriteString(agentStyle.Render(" "+m.agent.Name()+": ") + "\n")

			if msg.isThought {
				sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  💭 Thinking Process:") + "\n")
				textStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("244")). // Dark gray for thoughts
					Italic(true).
					PaddingLeft(4).
					Width(width - 6)
				sb.WriteString(textStyle.Render(msg.text) + "\n\n")
			} else if m.mdRenderer != nil {
				// Render agent replies as markdown (code blocks, lists, emphasis).
				out, err := m.mdRenderer.Render(msg.text)
				if err != nil {
					out = msg.text
				}
				body := strings.TrimRight(out, "\n")
				sb.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(body) + "\n\n")
			} else {
				textStyle := lipgloss.NewStyle().
					PaddingLeft(2).
					Width(width - 4)
				sb.WriteString(textStyle.Render(msg.text) + "\n\n")
			}
		case "system":
			sysStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("220")). // Gold/Yellow
				Italic(true).
				PaddingLeft(2).
				Width(width - 4)
			sb.WriteString(sysStyle.Render(msg.text) + "\n\n")
		}
	}
	return sb.String()
}

func (m mainModel) renderSidebar(width int) string {
	var sb strings.Builder

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("211")).
		Bold(true).
		Underline(true)

	// Header
	sb.WriteString(titleStyle.Render("AGENT INFO") + "\n\n")
	sb.WriteString("Name: " + m.agent.Name() + "\n")
	desc := m.agent.Description()
	if len(desc) > 50 {
		desc = desc[:47] + "..."
	}
	sb.WriteString("Desc: " + desc + "\n\n")

	// Status
	sb.WriteString(titleStyle.Render("STATUS") + "\n\n")
	statusColor := "82" // Green
	indicator := "●"
	if m.busy() {
		statusColor = "213" // Magenta
		indicator = m.spinner.View()
	}
	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(statusColor)).
		Bold(true)
	sb.WriteString(statusStyle.Render(indicator+" "+m.status) + "\n\n")

	// Capabilities (Active Tools)
	sb.WriteString(titleStyle.Render("CAPABILITIES") + "\n\n")
	if len(m.activeTools) == 0 {
		sb.WriteString("No tools registered.\n")
	} else {
		for _, t := range m.activeTools {
			sb.WriteString("- " + t + "\n")
		}
	}
	sb.WriteString("\n")

	// Session Info
	sb.WriteString(titleStyle.Render("SESSION") + "\n\n")
	sb.WriteString("ID: " + m.sessionID + "\n")

	return sb.String()
}

func (m mainModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing CLI UI..."
	}

	sidebarWidth := 30
	mainWidth := computeMainWidth(m.width)
	if mainWidth >= m.width-2 {
		sidebarWidth = 0
	}
	m.mainWidth = mainWidth
	m.ensureRenderer(mainWidth - 4)

	// Rounded styles
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62"))

	sidebarStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Width(sidebarWidth).
		Height(m.height - 2)

	// Adjust viewport dimensions
	viewportHeight := m.height - 5
	if viewportHeight < 3 {
		viewportHeight = 3
	}
	m.viewport.Height = viewportHeight
	m.viewport.Width = mainWidth

	content := m.renderMessages(mainWidth)
	if m.busy() {
		// Inline animated loader row beneath the transcript.
		loader := lipgloss.NewStyle().
			Foreground(lipgloss.Color("213")).
			PaddingLeft(2).
			Render(m.spinner.View() + " " + m.status)
		content += loader + "\n"
	}
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()

	m.textInput.Width = mainWidth - 4
	inputView := m.textInput.View()

	mainBody := lipgloss.JoinVertical(
		lipgloss.Left,
		m.viewport.View(),
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", mainWidth)),
		inputView,
	)

	mainContainer := borderStyle.Render(mainBody)

	if sidebarWidth > 0 {
		sidebarContent := m.renderSidebar(sidebarWidth)
		sidebarContainer := sidebarStyle.Render(sidebarContent)
		return lipgloss.JoinHorizontal(lipgloss.Top, mainContainer, sidebarContainer)
	}

	return mainContainer
}

// CLI launches the interactive terminal GUI chat interface for the given agents.
func CLI(rootAgent agent.Agent, subAgents ...agent.Agent) {
	sessionSvc := session.InMemoryService()
	cfg := runner.Config{
		AppName:           "agentd",
		Agent:             rootAgent,
		SessionService:    sessionSvc,
		AutoCreateSession: true,
	}

	r, err := runner.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to initialize agent runner: %v\n", err)
		os.Exit(1)
	}

	ti := textinput.New()
	ti.Placeholder = "Type a message to the agent..."
	ti.Focus()

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))

	// List active tools from system default registry
	var tools []string
	for name := range defaultToolRegistry.tools {
		tools = append(tools, name)
	}
	sort.Strings(tools)

	initialModel := mainModel{
		agent:       rootAgent,
		runner:      r,
		sessionID:   "session-1",
		userID:      "user-1",
		activeTools: tools,
		textInput:   ti,
		spinner:     sp,
		status:      "Idle",
		messages: []message{
			{
				sender: "system",
				text:   "👋 Welcome to the Agentd CLI! Type your message and press Enter.",
			},
		},
	}

	p := tea.NewProgram(initialModel, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: TUI failure: %v\n", err)
		os.Exit(1)
	}
}
