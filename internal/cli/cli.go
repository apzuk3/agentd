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

// Config configures a CLI session. Turn is required; everything else is
// presentational.
type Config struct {
	// AgentName and AgentDesc identify the root agent in the transcript and
	// sidebar.
	AgentName string
	AgentDesc string
	// Tools lists the registered tool names shown under CAPABILITIES.
	Tools []string
	// SubAgents is the flattened agent tree shown under SUB-AGENTS.
	SubAgents []AgentInfo
	// SessionID is shown under SESSION.
	SessionID string
	// Welcome overrides the initial system greeting.
	Welcome string
	// Turn runs a single user turn, streaming events back to the UI.
	Turn TurnFunc
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

	m := Model{
		cfg:       cfg,
		textInput: ti,
		spinner:   sp,
		status:    statusIdle,
		// Detect the terminal background now, before the program starts reading
		// input, so glamour never issues an OSC query mid-session (which would
		// leak the terminal's reply into the text field).
		mdStyle: detectMarkdownStyle(),
		messages: []message{
			{sender: "system", text: welcome},
		},
	}

	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
