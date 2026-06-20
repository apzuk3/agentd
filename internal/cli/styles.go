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
	// minViewportHeight keeps the transcript usable on very short terminals.
	minViewportHeight = 3
	// descMaxLen truncates the agent description shown in the sidebar.
	descMaxLen = 50
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
)
