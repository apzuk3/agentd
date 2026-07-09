package cli

import (
	"fmt"
	"strconv"
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

// viewportHeight is the transcript height for the current terminal size,
// accounting for the tab bar above and the divider/input below.
func (m Model) viewportHeight() int {
	h := m.height - inputReserve - tabBarReserve
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

// renderMessages renders the active tab's transcript to a single string at the
// given width.
func (m Model) renderMessages(width int) string {
	if width < 10 {
		width = 10
	}
	if len(m.tabs) == 0 {
		return ""
	}
	active := m.tabs[m.active]
	var sb strings.Builder
	for _, msg := range active.messages {
		switch msg.sender {
		case "user":
			sb.WriteString(userLabelStyle.Render(" You: ") + "\n")
			sb.WriteString(lipgloss.NewStyle().PaddingLeft(2).Width(width-4).Render(msg.text) + "\n\n")
		case "agent":
			sb.WriteString(agentLabelStyle.Render(" "+active.cfg.AgentName+": ") + "\n")
			sb.WriteString(m.renderAgentBody(msg, width))
		case "tool":
			sb.WriteString(m.renderToolBlock(msg, width))
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

// renderToolBlock renders a burst of tool calls as a single bordered panel:
// a header summarising how many calls ran, then one compact line per call
// (status icon · name · args → result). Running calls show the live spinner.
func (m Model) renderToolBlock(msg message, width int) string {
	done, failed := 0, 0
	for _, c := range msg.calls {
		switch c.state {
		case toolDone:
			done++
		case toolFailed:
			failed++
		}
	}

	header := "🔧 tool call"
	if len(msg.calls) != 1 {
		header = fmt.Sprintf("🔧 %d tool calls", len(msg.calls))
	}
	summary := fmt.Sprintf("%d/%d done", done+failed, len(msg.calls))
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed", failed)
	}
	header += "  " + toolArgsStyle.Render("· "+summary)

	lines := []string{toolHeaderStyle.Render(header)}
	for _, c := range msg.calls {
		lines = append(lines, m.renderToolCall(c))
	}

	inner := strings.Join(lines, "\n")
	boxWidth := width - 6
	if boxWidth < 20 {
		boxWidth = 20
	}
	box := toolBoxStyle.Width(boxWidth).Render(inner)
	return lipgloss.NewStyle().PaddingLeft(2).Render(box) + "\n\n"
}

// renderToolCall renders a single call line: icon + name + (args) → result.
func (m Model) renderToolCall(c toolCall) string {
	var icon string
	switch c.state {
	case toolDone:
		icon = toolDoneStyle.Render("✓")
	case toolFailed:
		icon = toolErrorStyle.Render("✗")
	default:
		icon = m.spinner.View()
	}

	line := icon + " " + toolNameStyle.Render(c.name)
	if c.args != "" {
		line += toolArgsStyle.Render("(" + c.args + ")")
	}
	if c.result != "" {
		style := toolResultStyle
		if c.state == toolFailed {
			style = toolErrorStyle
		}
		line += style.Render("  → " + c.result)
	}
	return line
}

// renderSidebar renders the right-hand info panel for the active tab.
func (m Model) renderSidebar(width int) string {
	var sb strings.Builder
	if len(m.tabs) == 0 {
		return ""
	}
	active := m.tabs[m.active]
	cfg := active.cfg

	sb.WriteString(sidebarTitleStyle.Render("AGENT INFO") + "\n\n")
	sb.WriteString("Name: " + cfg.AgentName + "\n")
	desc := cfg.AgentDesc
	if len(desc) > descMaxLen {
		desc = desc[:descMaxLen-3] + "..."
	}
	sb.WriteString("Desc: " + desc + "\n")
	if cfg.ModelName != "" {
		sb.WriteString("Model: " + cfg.ModelName + "\n")
	}
	sb.WriteString("\n")

	sb.WriteString(sidebarTitleStyle.Render("STATUS") + "\n\n")
	indicator := "●"
	statusStyle := statusIdleStyle
	if active.busy() {
		indicator = m.spinner.View()
		statusStyle = statusBusyStyle
	}
	sb.WriteString(statusStyle.Render(indicator+" "+active.statusLabel()) + "\n\n")

	sb.WriteString(sidebarTitleStyle.Render("TOKENS") + "\n\n")
	sb.WriteString("In:    " + formatInt(active.promptTokens) + "\n")
	sb.WriteString("Out:   " + formatInt(active.outputTokens) + "\n")
	sb.WriteString("Total: " + formatInt(active.promptTokens+active.outputTokens) + "\n\n")

	sb.WriteString(sidebarTitleStyle.Render("CAPABILITIES") + "\n\n")
	if len(cfg.Tools) == 0 {
		sb.WriteString("No tools registered.\n")
	} else {
		for _, t := range cfg.Tools {
			sb.WriteString("- " + t + "\n")
		}
	}
	sb.WriteString("\n")

	if len(cfg.SubAgents) > 0 {
		sb.WriteString(sidebarTitleStyle.Render("SUB-AGENTS") + "\n\n")
		for _, sa := range cfg.SubAgents {
			sb.WriteString(strings.Repeat("  ", sa.Depth) + "- " + sa.Name + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString(sidebarTitleStyle.Render("SESSION") + "\n\n")
	sb.WriteString("ID: " + cfg.SessionID + "\n")

	return sb.String()
}

// formatInt renders n with thousands separators, e.g. 12345 -> "12,345".
func formatInt(n int) string {
	s := strconv.Itoa(n)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	if len(s) <= 3 {
		return neg + s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
		if len(s) > lead {
			b.WriteByte(',')
		}
	}
	for i := lead; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return neg + b.String()
}

// renderTabBar renders the tab labels left-to-right. The active tab is
// highlighted; other busy tabs are tinted so background work is visible even
// when not viewed. Tabs are switched with tab / shift+tab.
func (m Model) renderTabBar(width int) string {
	var sb strings.Builder
	for i := range m.tabs {
		name := m.tabs[i].cfg.AgentName
		if name == "" {
			name = fmt.Sprintf("agent %d", i+1)
		}
		if mdl := m.tabs[i].cfg.ModelName; mdl != "" {
			name += " · " + mdl
		}
		style := tabInactiveStyle
		switch {
		case i == m.active:
			style = tabActiveStyle
		case m.tabs[i].busy():
			style = tabBusyStyle
		}
		sb.WriteString(style.Render(name))
	}
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
	if active := m.tabs[m.active]; active.busy() {
		content += loaderStyle.Render(m.spinner.View()+" "+active.statusLabel()) + "\n"
	}
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()

	m.textInput.Width = mainWidth - 4

	tabBar := m.renderTabBar(mainWidth)
	mainBody := lipgloss.JoinVertical(
		lipgloss.Left,
		tabBar,
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
