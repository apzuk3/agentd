package agentd

import (
	"context"
	"iter"

	"github.com/google/uuid"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// StreamEventKind classifies a StreamEvent.
//
// It is agentd's stable, ADK-free vocabulary for the things that happen during
// an agent turn. Callers switch on the kind instead of inspecting the
// underlying ADK session.Event / genai.Content types.
type StreamEventKind int

const (
	// StreamEventText is an incremental chunk of model text. In
	// agent.StreamingModeSSE these arrive with Partial set to true as the
	// reply is generated.
	StreamEventText StreamEventKind = iota
	// StreamEventThought is an incremental chunk of the model's "thinking"
	// text (genai Part.Thought). Treat it as diagnostic, not as the answer.
	StreamEventThought
	// StreamEventToolCall signals the model invoked a tool. ToolName and
	// ToolArgs are populated.
	StreamEventToolCall
	// StreamEventToolResult signals a tool returned a value. ToolName and
	// ToolData are populated.
	StreamEventToolResult
	// StreamEventFinal carries the full, aggregated reply text for a turn.
	// Exactly one is emitted per finished turn (per participating agent),
	// after any partial StreamEventText chunks.
	StreamEventFinal
)

// StreamEvent is agentd's ADK-free view of a single thing that happened during
// an agent turn. Callers range over [LaunchStreaming] and switch on Kind.
//
// Which fields are populated depends on Kind:
//   - StreamEventText, StreamEventThought, StreamEventFinal -> Text
//   - StreamEventToolCall                                    -> ToolName, ToolArgs
//   - StreamEventToolResult                                  -> ToolName, ToolData
//
// Author is always set to the name of the agent that emitted the event, which
// is useful when several agents participate in one invocation.
type StreamEvent struct {
	Kind     StreamEventKind
	Text     string         // Text / Thought / Final
	ToolName string         // ToolCall / ToolResult
	ToolArgs map[string]any // ToolCall  (genai FunctionCall.Args)
	ToolData any            // ToolResult (genai FunctionResponse.Response)
	Partial  bool           // true while a streamed message is still being built
	Author   string         // which agent emitted the event
}

// runConfig holds the resolved session/identity settings for a launcher call.
type runConfig struct {
	userID     string
	sessionID  string
	appName    string
	sessionSvc session.Service
}

// RunOption configures the session and identity used by a launcher
// ([LaunchStreaming], [LaunchSync], and [CLI]). The same options apply to
// every launcher because none of them are specific to streaming.
type RunOption func(*runConfig) error

const (
	// defaultUserID is the non-secret default identity attributed to a run
	// when the caller does not supply one via WithUserID. It is a display
	// label, not a credential.
	defaultUserID = "agentd-user"
	// defaultAppName is the default application name recorded on the session.
	defaultAppName = "agentd"
)

func defaultRunConfig() *runConfig {
	return &runConfig{
		userID:     defaultUserID,
		sessionID:  uuid.NewString(),
		appName:    defaultAppName,
		sessionSvc: session.InMemoryService(),
	}
}

// WithUserID sets the user identity used for the run. Defaults to
// "agentd-user". Use a stable value to attribute multiple turns to the same
// user within a session service.
func WithUserID(id string) RunOption {
	return func(c *runConfig) error {
		c.userID = id
		return nil
	}
}

// WithSessionID pins the session ID. Defaults to a fresh UUID per call. Pass
// the same ID (with the same session service) across calls to continue a
// multi-turn conversation.
func WithSessionID(id string) RunOption {
	return func(c *runConfig) error {
		c.sessionID = id
		return nil
	}
}

// WithAppName sets the application name recorded on the session. Defaults to
// "agentd".
func WithAppName(name string) RunOption {
	return func(c *runConfig) error {
		c.appName = name
		return nil
	}
}

// stream is the shared engine behind every launcher. It resolves the run
// options, builds the ADK runner and session, and yields mapped StreamEvents
// using the given streaming mode. The mode is an internal detail: callers pick
// a launcher (LaunchStreaming vs LaunchSync) rather than a mode.
func stream(ctx context.Context, a agent.Agent, input string, mode agent.StreamingMode, opts ...RunOption) iter.Seq2[*StreamEvent, error] {
	return func(yield func(*StreamEvent, error) bool) {
		cfg := defaultRunConfig()
		for _, o := range opts {
			if err := o(cfg); err != nil {
				yield(nil, err)
				return
			}
		}

		r, err := runner.New(runner.Config{
			AppName:           cfg.appName,
			Agent:             a,
			SessionService:    cfg.sessionSvc,
			AutoCreateSession: true,
		})
		if err != nil {
			yield(nil, err)
			return
		}

		msg := genai.NewContentFromText(input, genai.RoleUser)
		runCfg := agent.RunConfig{StreamingMode: mode}

		for ev, err := range r.Run(ctx, cfg.userID, cfg.sessionID, msg, runCfg) {
			if err != nil {
				yield(nil, err)
				return
			}
			for _, se := range mapEvent(ev) {
				if !yield(se, nil) {
					return
				}
			}
		}
	}
}

// LaunchStreaming runs the agent against a single user input and yields the
// resulting [StreamEvent]s as a Go 1.23+ iterator. It is the core runtime
// primitive of agentd: [LaunchSync] and [CLI] are built on top of it, and
// callers never touch the underlying ADK runner, session, or genai types.
//
// It streams incrementally: callers receive a sequence of StreamEventText
// chunks with Partial set to true as the reply is generated, followed by a
// single StreamEventFinal carrying the full reply text for the turn. Tool
// activity arrives as StreamEventToolCall / StreamEventToolResult, and model
// "thinking" as StreamEventThought. To aggregate a complete answer, sum the
// Text of StreamEventFinal events.
//
// Iteration stops when the agent finishes, the context is cancelled, or the
// caller breaks out of the range loop. Any error from the underlying run is
// yielded as the second value with a nil event; callers should check it and
// stop.
//
// Example:
//
//	for ev, err := range agentd.LaunchStreaming(ctx, root, "deploy main to prod") {
//		if err != nil {
//			return err
//		}
//		switch ev.Kind {
//		case agentd.StreamEventText:
//			fmt.Print(ev.Text)
//		case agentd.StreamEventToolCall:
//			fmt.Printf("\n[tool %s]\n", ev.ToolName)
//		}
//	}
func LaunchStreaming(ctx context.Context, a agent.Agent, input string, opts ...RunOption) iter.Seq2[*StreamEvent, error] {
	return stream(ctx, a, input, agent.StreamingModeSSE, opts...)
}

// mapEvent converts a single ADK session.Event into zero or more agentd
// StreamEvents. It generalizes the inline event handling previously found only
// in the CLI, so every launcher shares one mapping.
func mapEvent(ev *session.Event) []*StreamEvent {
	if ev == nil || ev.Content == nil {
		return nil
	}

	var out []*StreamEvent
	var turnText string

	for _, p := range ev.Content.Parts {
		switch {
		case p.Text != "" && p.Thought:
			out = append(out, &StreamEvent{
				Kind:    StreamEventThought,
				Text:    p.Text,
				Partial: ev.Partial,
				Author:  ev.Author,
			})
		case p.Text != "":
			turnText += p.Text
			out = append(out, &StreamEvent{
				Kind:    StreamEventText,
				Text:    p.Text,
				Partial: ev.Partial,
				Author:  ev.Author,
			})
		case p.FunctionCall != nil:
			out = append(out, &StreamEvent{
				Kind:     StreamEventToolCall,
				ToolName: p.FunctionCall.Name,
				ToolArgs: p.FunctionCall.Args,
				Author:   ev.Author,
			})
		case p.FunctionResponse != nil:
			out = append(out, &StreamEvent{
				Kind:     StreamEventToolResult,
				ToolName: p.FunctionResponse.Name,
				ToolData: p.FunctionResponse.Response,
				Author:   ev.Author,
			})
		}
	}

	// Emit a single aggregated final event once the turn is complete and it
	// actually produced an answer (tool-call/response-only finals carry no
	// user-facing text).
	if ev.IsFinalResponse() && turnText != "" {
		out = append(out, &StreamEvent{
			Kind:   StreamEventFinal,
			Text:   turnText,
			Author: ev.Author,
		})
	}

	return out
}
