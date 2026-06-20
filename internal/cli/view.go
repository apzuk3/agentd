package cli

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

// detectMarkdownStyle resolves the glamour style to use for rendering replies.
//
// It must be called once, before the bubbletea program starts reading input.
// glamour's WithAutoStyle queries the terminal's background colour (an OSC 11
// escape) at render time; if that query runs while the program is live, the
// terminal's reply is read as user input and leaks into the text field. By
// detecting (and caching, via lipgloss' sync.Once) the background up front and
// then building renderers with an explicit style, no query happens mid-session.
func detectMarkdownStyle() string {
	if lipgloss.HasDarkBackground() {
		return styles.DarkStyle
	}
	return styles.LightStyle
}

// computeMainWidth derives the transcript/input column width from the total
// terminal width, accounting for the sidebar and borders. On narrow terminals
// it falls back to a near-full-width layout with the sidebar hidden.
func computeMainWidth(total int) int {
	mw := total - sidebarReserve
	if mw < minMainWidth {
		mw = total - chromeReserve
	}
	return mw
}

// viewportHeight is the transcript height for the current terminal size.
func (m Model) viewportHeight() int {
	h := m.height - inputReserve
	if h < minViewportHeight {
		h = minViewportHeight
	}
	return h
}

// ensureRenderer (re)builds the markdown renderer when the wrap width changes.
func (m *Model) ensureRenderer(width int) {
	if width < 10 {
		width = 10
	}
	if m.mdRenderer != nil && m.rendererWidth == width {
		return
	}
	style := m.mdStyle
	if style == "" {
		style = styles.DarkStyle
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		m.mdRenderer = nil
		return
	}
	m.mdRenderer = r
	m.rendererWidth = width
}

// renderMessages renders the whole transcript to a single string at the given
// width.
func (m Model) renderMessages(width int) string {
	if width < 10 {
		width = 10
	}
	var sb strings.Builder
	for _, msg := range m.messages {
		switch msg.sender {
		case "user":
			sb.WriteString(userLabelStyle.Render(" You: ") + "\n")
			sb.WriteString(lipgloss.NewStyle().PaddingLeft(2).Width(width-4).Render(msg.text) + "\n\n")
		case "agent":
			sb.WriteString(agentLabelStyle.Render(" "+m.cfg.AgentName+": ") + "\n")
			sb.WriteString(m.renderAgentBody(msg, width))
		case "system":
			sb.WriteString(systemStyle.PaddingLeft(2).Width(width-4).Render(msg.text) + "\n\n")
		}
	}
	return sb.String()
}

// renderAgentBody renders an agent message, formatting thoughts distinctly and
// running normal replies through the markdown renderer when available.
func (m Model) renderAgentBody(msg message, width int) string {
	var sb strings.Builder
	switch {
	case msg.isThought:
		sb.WriteString(thoughtHeaderStyle.Render("  💭 Thinking Process:") + "\n")
		sb.WriteString(thoughtTextStyle.PaddingLeft(4).Width(width-6).Render(msg.text) + "\n\n")
	case m.mdRenderer != nil:
		out, err := m.mdRenderer.Render(msg.text)
		if err != nil {
			out = msg.text
		}
		body := strings.TrimRight(out, "\n")
		sb.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(body) + "\n\n")
	default:
		sb.WriteString(lipgloss.NewStyle().PaddingLeft(2).Width(width-4).Render(msg.text) + "\n\n")
	}
	return sb.String()
}

// renderSidebar renders the right-hand info panel.
func (m Model) renderSidebar(width int) string {
	var sb strings.Builder

	sb.WriteString(sidebarTitleStyle.Render("AGENT INFO") + "\n\n")
	sb.WriteString("Name: " + m.cfg.AgentName + "\n")
	desc := m.cfg.AgentDesc
	if len(desc) > descMaxLen {
		desc = desc[:descMaxLen-3] + "..."
	}
	sb.WriteString("Desc: " + desc + "\n\n")

	sb.WriteString(sidebarTitleStyle.Render("STATUS") + "\n\n")
	indicator := "●"
	statusStyle := statusIdleStyle
	if m.busy() {
		indicator = m.spinner.View()
		statusStyle = statusBusyStyle
	}
	sb.WriteString(statusStyle.Render(indicator+" "+m.statusLabel()) + "\n\n")

	sb.WriteString(sidebarTitleStyle.Render("CAPABILITIES") + "\n\n")
	if len(m.cfg.Tools) == 0 {
		sb.WriteString("No tools registered.\n")
	} else {
		for _, t := range m.cfg.Tools {
			sb.WriteString("- " + t + "\n")
		}
	}
	sb.WriteString("\n")

	if len(m.cfg.SubAgents) > 0 {
		sb.WriteString(sidebarTitleStyle.Render("SUB-AGENTS") + "\n\n")
		for _, sa := range m.cfg.SubAgents {
			sb.WriteString(strings.Repeat("  ", sa.Depth) + "- " + sa.Name + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString(sidebarTitleStyle.Render("SESSION") + "\n\n")
	sb.WriteString("ID: " + m.cfg.SessionID + "\n")

	return sb.String()
}

// View implements tea.Model.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing CLI UI..."
	}

	mainWidth := computeMainWidth(m.width)
	showSidebar := mainWidth < m.width-chromeReserve
	m.mainWidth = mainWidth
	m.ensureRenderer(mainWidth - 4)

	m.viewport.Height = m.viewportHeight()
	m.viewport.Width = mainWidth

	content := m.renderMessages(mainWidth)
	if m.busy() {
		content += loaderStyle.Render(m.spinner.View()+" "+m.statusLabel()) + "\n"
	}
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()

	m.textInput.Width = mainWidth - 4

	mainBody := lipgloss.JoinVertical(
		lipgloss.Left,
		m.viewport.View(),
		dividerStyle.Render(strings.Repeat("─", mainWidth)),
		m.textInput.View(),
	)
	mainContainer := mainBorderStyle.Render(mainBody)

	if !showSidebar {
		return mainContainer
	}

	sidebar := sidebarBorderStyle.
		Width(sidebarWidth).
		Height(m.height - chromeReserve).
		Render(m.renderSidebar(sidebarWidth))
	return lipgloss.JoinHorizontal(lipgloss.Top, mainContainer, sidebar)
}
