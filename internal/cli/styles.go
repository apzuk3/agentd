package cli

import "github.com/charmbracelet/lipgloss"

// Layout constants. Kept here so the view math has a single source of truth
// instead of magic numbers scattered across rendering code.
const (
	// sidebarWidth is the fixed width of the right-hand info panel.
	sidebarWidth = 30
	// sidebarReserve is the horizontal space consumed by the sidebar plus the
	// borders/gap between it and the main column.
	sidebarReserve = sidebarWidth + 3
	// minMainWidth is the narrowest main column before the sidebar is dropped.
	minMainWidth = 30
	// chromeReserve is the horizontal space taken by the main column's border.
	chromeReserve = 2
	// inputReserve is the number of rows reserved below the transcript for the
	// divider and the input line.
	inputReserve = 5
	// tabBarReserve is the number of rows the tab bar consumes above the
	// transcript.
	tabBarReserve = 1
	// minViewportHeight keeps the transcript usable on very short terminals.
	minViewportHeight = 3
	// descMaxLen truncates the agent description shown in the sidebar.
	descMaxLen = 50
	// argsMaxLen / resultMaxLen bound the inline summaries of a tool call's
	// arguments and response inside a tool block.
	argsMaxLen   = 48
	resultMaxLen = 56
)

// Foreground palette (ANSI 256). Centralised so the colours are named once.
var (
	userLabelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	agentLabelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("211")).Bold(true)
	thoughtHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	thoughtTextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	systemStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Italic(true)

	sidebarTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("211")).Bold(true).Underline(true)
	statusIdleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	statusBusyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)

	spinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	loaderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).PaddingLeft(2)
	dividerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	mainBorderStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62"))
	sidebarBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))

	// Tab bar. The active tab is highlighted; inactive tabs are dimmed. A busy
	// tab's label is tinted so background work is visible even when not viewed.
	tabActiveStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("211")).Bold(true).Underline(true).Padding(0, 1)
	tabInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)
	tabBusyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Padding(0, 1)

	// Tool-block palette. Each tool burst renders inside one bordered panel:
	// a dim title header, then one line per call (icon + name + args → result).
	toolBoxStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	toolHeaderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Bold(true)
	toolNameStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)
	toolArgsStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	toolResultStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	toolDoneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	toolRunningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	toolErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)
