package agentd

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

type ToolContext tool.Context

type ToolHandler[TArgs, TResults any] func(ToolContext, TArgs) (TResults, error)

type FnToolOption func(*functiontool.Config)

// WithInputSchema overrides the JSON schema describing a tool's arguments.
//
// By default the ADK infers this schema from the generic TArgs type via
// reflection, and the result is sent to the model as the tool's parameter
// declaration (genai FunctionDeclaration.ParametersJsonSchema). That inferred
// schema is also used at call time to decode and validate the model's raw
// arguments into TArgs.
//
// Supply an explicit schema when reflection alone is not expressive enough,
// for example to:
//   - add field descriptions, defaults, or examples that steer how the model
//     fills in arguments;
//   - encode constraints reflection cannot express (enums, min/max, patterns,
//     string formats, which fields are required);
//   - give a meaningful shape to loosely typed handlers (e.g. map[string]any),
//     where reflection yields little useful structure.
//
// A tighter input schema both improves the model's chance of calling the tool
// correctly and rejects malformed arguments before the handler runs.
//
// Example: a "send_email" tool whose priority must be one of a fixed set and
// whose recipient must look like an email address. Reflection over a plain
// string field could not express either constraint:
//
//	schema := &jsonschema.Schema{
//		Type: "object",
//		Properties: map[string]*jsonschema.Schema{
//			"to": {
//				Type:        "string",
//				Format:      "email",
//				Description: "Recipient address, e.g. alice@example.com",
//			},
//			"priority": {
//				Type:        "string",
//				Enum:        []any{"low", "normal", "high"},
//				Description: "Delivery priority; defaults to normal",
//			},
//		},
//		Required: []string{"to"},
//	}
//	agentd.NewTool("send_email", "Send an email", sendEmail, agentd.WithInputSchema(schema))
func WithFunctionToolInputSchema(schema *jsonschema.Schema) FnToolOption {
	return func(cfg *functiontool.Config) {
		cfg.InputSchema = schema
	}
}

// WithOutputSchema overrides the JSON schema describing a tool's result.
//
// By default the ADK infers this schema from the generic TResults type via
// reflection and advertises it to the model as the tool's response declaration
// (genai FunctionDeclaration.ResponseJsonSchema), so the model knows what shape
// to expect back. It is also used to validate the handler's output and decide
// how the result is marshalled (non-map results are wrapped as {"result": ...}).
//
// Supply an explicit schema when you want to:
//   - document the response fields so the model can reason about them on the
//     next turn;
//   - constrain or narrow the advertised result (enums, required fields) beyond
//     what the Go type conveys;
//   - describe loosely typed results that reflection cannot meaningfully infer.
//
// Example: a "create_ticket" tool that returns a status the model should react
// to. Documenting the fields and constraining "status" to known values helps
// the model decide what to say next (e.g. report the URL, or retry on error):
//
//	schema := &jsonschema.Schema{
//		Type: "object",
//		Properties: map[string]*jsonschema.Schema{
//			"ticket_id": {Type: "string", Description: "ID of the created ticket"},
//			"url":       {Type: "string", Description: "Link the user can open"},
//			"status": {
//				Type: "string",
//				Enum: []any{"created", "duplicate", "error"},
//			},
//		},
//		Required: []string{"ticket_id", "status"},
//	}
//	agentd.NewTool("create_ticket", "Open a support ticket", createTicket, agentd.WithOutputSchema(schema))
func WithFunctionToolOutputSchema(schema *jsonschema.Schema) FnToolOption {
	return func(cfg *functiontool.Config) {
		cfg.OutputSchema = schema
	}
}

// WithIsLongRunning marks a tool as a long-running operation.
//
// A normal tool call is synchronous: the model calls it, the handler returns,
// and the result is fed straight back into the conversation. Marking a tool
// long-running signals that its work outlives a single call and may finish
// later (e.g. a job that is kicked off and then polled), so the handler is
// expected to return an intermediate or pending status rather than a final
// result.
//
// The ADK uses this flag in two ways:
//   - IsLongRunning() exposes it so the runner/agent can treat the call as
//     asynchronous instead of blocking on a final answer;
//   - the tool's advertised declaration gets an extra instruction telling the
//     model not to re-invoke the tool while a pending status is outstanding,
//     which prevents it from spamming duplicate calls.
//
// Use this for tools that start background work, await human input, or
// otherwise complete out of band.
//
// Example: a "deploy_service" tool kicks off a deployment that takes minutes.
// Instead of blocking, it returns a job handle with a "pending" status; the
// agent polls a separate "deploy_status" tool until it is done. The flag stops
// the model from firing off a second deploy while the first is still pending:
//
//	agentd.NewTool("deploy_service", "Deploy a service to production", deploy, agentd.WithIsLongRunning(true))
//
// Other good fits: generating a large report, transcoding a video, or a tool
// that pauses for human approval before continuing.
func WithFunctionToolIsLongRunning(isLongRunning bool) FnToolOption {
	return func(cfg *functiontool.Config) {
		cfg.IsLongRunning = isLongRunning
	}
}

// ---------------------------------------------------------------------------
// Human-in-the-Loop (HITL) confirmation flow
// ---------------------------------------------------------------------------
//
// Both WithRequireConfirmation and WithRequireConfirmationProvider hook into the
// same ADK confirmation machinery. The key idea is that a gated tool call does
// NOT run to completion in one shot. It is split across two agent turns: the
// first turn pauses and asks for approval, the second turn (after the user/app
// answers) resumes and actually runs the handler.
//
// The user's decision does not arrive as a Go return value or a channel. It
// comes back into the conversation as a normal genai.FunctionResponse, exactly
// like any other tool result. The application (your frontend, CLI, or backend)
// is responsible for surfacing the request to a human and sending that response
// back through the runner. The ADK correlates request and response by the
// FunctionCall ID.
//
// Turn 1 - the agent wants to run a gated tool:
//
//	user prompt
//	    │
//	    ▼
//	┌─────────┐  calls gated tool   ┌──────────────────────────-┐
//	│  model  │ ──────────────────▶ │ functiontool.Run          │
//	└─────────┘                     │  requireConfirmation? yes │
//	                                │  ctx.RequestConfirmation()│
//	                                └────────────┬───────────-──┘
//	                                             │ records a pending
//	                                             │ ToolConfirmation in
//	                                             │ EventActions and sets
//	                                             │ SkipSummarization=true
//	                                             ▼
//	                          emits an "adk_request_confirmation"
//	                          FunctionCall event (wraps the ORIGINAL
//	                          call as "originalFunctionCall") and
//	                          returns tool.ErrConfirmationRequired.
//	                          The handler body is NOT executed.
//	                                             │
//	                                             ▼
//	                               agent loop stops for this turn
//
// Out of band - your application decides:
//
//	┌──────────────┐  show prompt   ┌───────────┐
//	│ your app/UI  │ ─────────────▶ │  human    │
//	│ (listens for │ ◀───────────── │ approves/ │
//	│  the event)  │   yes / no     │  rejects  │
//	└──────┬───────┘                └───────────┘
//	       │ build a genai.FunctionResponse:
//	       │   Name = toolconfirmation.FunctionCallName
//	       │          ("adk_request_confirmation")
//	       │   ID   = <same ID as the request FunctionCall>
//	       │   Response = {"confirmed": bool, "payload": ...}
//	       ▼
//	   send it back into the session via runner.Run (Role: user)
//
// Turn 2 - the agent resumes:
//
//	confirmation FunctionResponse
//	    │
//	    ▼
//	┌────────────────────────────────--──┐  matches response to the
//	│ RequestConfirmationRequestProcessor│  pending request by call ID,
//	│ (internal/llminternal)             │  rebuilds the original call
//	└────────────────┬─────────────────-─┘
//	                 ▼
//	┌───────────────────────────────-─-──┐  now ctx.ToolConfirmation()
//	│ functiontool.Run (re-invoked)      │  is non-nil:
//	│  confirmation.Confirmed == true   ─┼─▶ run the real handler
//	│  confirmation.Confirmed == false  ─┼─▶ tool.ErrConfirmationRejected
//	└────────────────────────────────-──-┘
//
// So from the handler's point of view, the same function can be entered twice:
// once with ctx.ToolConfirmation() == nil (the gate, before approval) and once
// with a populated ToolConfirmation (after approval). The simple options below
// hide that bookkeeping; if you need full control over the payload and the
// pending/resume bookkeeping, drive ctx.RequestConfirmation() and
// ctx.ToolConfirmation() yourself inside the handler instead.
//
// ---------------------------------------------------------------------------

// WithRequireConfirmation forces a tool to always ask for user approval before
// it runs (Human-in-the-Loop).
//
// When set, the ADK does not execute the handler on the first call. Instead it
// emits a confirmation request and returns tool.ErrConfirmationRequired,
// pausing the tool until the user responds. The handler only runs once a
// FunctionResponse carrying a confirmed ToolConfirmation comes back; a rejected
// confirmation fails the call with tool.ErrConfirmationRejected.
//
// This is the static, unconditional form of confirmation: every invocation is
// gated regardless of the arguments. For a decision that depends on the call's
// arguments, use WithRequireConfirmationProvider instead (the provider takes
// precedence over this flag).
//
// Use this for irreversible or high-impact actions where you always want a
// human in the loop.
//
// Example: a "delete_account" tool should never run unattended:
//
//	agentd.NewTool("delete_account", "Permanently delete a user account", deleteAccount, agentd.WithRequireConfirmation(true))
func WithFunctionToolRequireConfirmation(requireConfirmation bool) FnToolOption {
	return func(cfg *functiontool.Config) {
		cfg.RequireConfirmation = requireConfirmation
	}
}

// WithRequireConfirmationProvider gates a tool behind user approval only when a
// runtime predicate says so, based on the decoded call arguments.
//
// The provider is called with the tool's input (the same TArgs the handler
// receives) and returns true to require Human-in-the-Loop confirmation for that
// specific invocation, or false to let it run straight through. When it returns
// true the call is paused with tool.ErrConfirmationRequired exactly as with
// WithRequireConfirmation, and resumes only on a confirmed ToolConfirmation.
//
// This is the dynamic counterpart to WithRequireConfirmation: it lets you wave
// through low-risk calls while still stopping for dangerous ones. If both are
// set, the provider takes precedence over the static flag.
//
// The TArgs of the provider must match the handler's argument type. Because the
// ADK stores the provider as an untyped value and recovers its signature via a
// runtime type assertion, a mismatched type is only reported as an error when
// the tool is constructed, not at compile time. The explicit type parameter
// here (WithRequireConfirmationProvider[ArgsType]) helps keep the signature
// honest.
//
// Example: a "transfer_funds" tool runs freely for small amounts but asks for
// confirmation on large transfers:
//
//	type TransferArgs struct {
//		To     string  `json:"to"`
//		Amount float64 `json:"amount"`
//	}
//
//	needsApproval := func(args TransferArgs) bool {
//		return args.Amount >= 1000
//	}
//
//	agentd.NewTool("transfer_funds", "Transfer money between accounts", transferFunds,
//		agentd.WithRequireConfirmationProvider(needsApproval))
func WithFunctionToolRequireConfirmationProvider[TArgs any](provider func(TArgs) bool) FnToolOption {
	return func(cfg *functiontool.Config) {
		cfg.RequireConfirmationProvider = provider
	}
}

type ToolRegistry struct {
	tools map[string]tool.Tool
}

func (t *ToolRegistry) AddTool(tool tool.Tool) {
	t.tools[tool.Name()] = tool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]tool.Tool),
	}
}

var defaultToolRegistry = NewToolRegistry()

func AddTool[TArgs, TResults any](name string, description string, handler ToolHandler[TArgs, TResults], options ...FnToolOption) error {
	cfg := functiontool.Config{
		Name:        name,
		Description: description,
	}
	for _, option := range options {
		option(&cfg)
	}
	tool, err := functiontool.New(cfg, func(ctx tool.Context, args TArgs) (TResults, error) {
		return handler(ctx, args)
	})
	if err != nil {
		return fmt.Errorf("failed to create tool: %w", err)
	}

	defaultToolRegistry.AddTool(tool)
	return nil
}
