// Package cli implements the bubbletea terminal UI behind agentd.LaunchCLI.
//
// It is deliberately transport-agnostic: it knows nothing about agentd, ADK, or
// genai. The caller provides agent metadata and a [TurnFunc] via [Config], and
// the package renders the chat, streams events, and handles cancellation. This
// keeps the UI decoupled and independently testable, and keeps every ADK import
// inside the agentd package.
package cli

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// AgentInfo describes one agent in the sidebar's agent tree. Depth is the
// indentation level (0 for direct children of the root).
type AgentInfo struct {
	Name        string
	Description string
	Depth       int
}

// TabConfig configures a single agent tab. Each tab has its own transcript and
// runs its own turn; the presentational fields drive the tab label and sidebar
// when the tab is active. Turn is required.
type TabConfig struct {
	// AgentName and AgentDesc identify the agent in the tab label, transcript,
	// and sidebar.
	AgentName string
	AgentDesc string
	// ModelName is the underlying model (e.g. "gemini-3.5-flash"), shown in the
	// tab label and sidebar. Empty when it could not be determined.
	ModelName string
	// Tools lists the tool names attached to the agent (and its sub-agents)
	// that are shown under CAPABILITIES in the sidebar.
	Tools []string
	// SubAgents is the flattened agent tree shown under SUB-AGENTS.
	SubAgents []AgentInfo
	// SessionID is shown under SESSION.
	SessionID string
	// Turn runs a single user turn for this tab, streaming events back to the UI.
	Turn TurnFunc
}

// Config configures a CLI session. At least one tab is required; a submitted
// message is broadcast to every tab.
type Config struct {
	// Tabs are the agent tabs, rendered left-to-right in order.
	Tabs []TabConfig
	// Welcome overrides the initial system greeting (shown on the first tab).
	Welcome string
}

const defaultWelcome = "👋 Welcome to the Agentd CLI! Type your message and press Enter."

// Run builds and runs the bubbletea program for the given configuration. It
// blocks until the user quits and returns any error from the TUI runtime.
func Run(cfg Config) error {
	ti := textinput.New()
	ti.Placeholder = "Type a message to the agent..."
	ti.Focus()

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = spinnerStyle

	welcome := cfg.Welcome
	if welcome == "" {
		welcome = defaultWelcome
	}

	tabs := make([]tab, len(cfg.Tabs))
	for i, tc := range cfg.Tabs {
		tabs[i] = tab{cfg: tc, status: statusIdle}
	}
	// Seed the greeting on the first tab only, so it isn't repeated per agent.
	if len(tabs) > 0 {
		tabs[0].messages = []message{{sender: "system", text: welcome}}
	}

	m := Model{
		cfg:       cfg,
		tabs:      tabs,
		active:    0,
		textInput: ti,
		spinner:   sp,
		// Detect the terminal background now, before the program starts reading
		// input, so glamour never issues an OSC query mid-session (which would
		// leak the terminal's reply into the text field).
		mdStyle: detectMarkdownStyle(),
	}

	// Mouse capture is deliberately left off so the terminal's native
	// drag-to-select / copy keeps working. Tabs are switched with tab / shift+tab.
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
