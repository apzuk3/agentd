# agentd — Agent Orchestration Service

**Experimental.** This project aims to provide multi-provider, multi-agent orchestration while keeping privacy in mind.

## What this project is

agentd is a server-side agent orchestration service built in Go. It uses **Google ADK** (Agent Development Kit) under the hood to orchestrate agents and sub-agents, while exposing a **ConnectRPC bidirectional streaming** API to clients.

The core architectural principle: **agents run on the server, but tool calls execute on the client**. This keeps private data on the client side — the server never sees raw tool outputs beyond what the client explicitly sends back. The client communicates directly with the LLM provider through the server's mediation of the agent loop.

## Quick start

**1. Start the server**

```bash
docker run -p 8080:8080 -e GEMINI_API_KEY=${GEMINI_API_KEY} ghcr.io/apzuk3/agentd:latest
```

**2. Run an agent with a client-side tool**

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/apzuk3/agentd/client"
	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

type GreetInput struct {
	Name string `json:"name"`
}

type GreetOutput struct {
	Greeting string `json:"greeting"`
}

func main() {
	clnt := client.New("http://localhost:8080")

	// Register a tool — the handler runs on the client, keeping data private.
	// Use concrete struct types so AddTool[T] can generate the JSON schema.
	client.AddTool(clnt, "greet", "Returns a greeting", func(ctx context.Context, input GreetInput) (any, error) {
		return GreetOutput{Greeting: "Hello, " + input.Name + "!"}, nil
	})

	// Define an agent that can use the tool.
	agent := &agentdv1.Agent{
		Name: "greeter",
		AgentType: &agentdv1.Agent_Llm{
			Llm: &agentdv1.LlmAgent{
				Model:     "gemini-2.5-flash",
				ToolNames: []string{"greet"},
			},
		},
	}

	// Run the agent and stream the response.
	for event, err := range clnt.Run(context.Background(), agent, "Say hi to Alice") {
		if err != nil {
			log.Fatal(err)
		}
		if event.OutputChunk != nil {
			fmt.Print(event.OutputChunk.Content)
		}
		if event.End != nil {
			break
		}
	}
}
```

See [`examples/`](examples/) for more complete examples.

## Architecture overview

```
┌─────────-┐         ConnectRPC bidi stream          ┌──────────┐
│  Client  │ ◄─────────────────────────────────────► │  agentd  │
│          │   RunRequest / RunResponse (oneof)      │ (server) │
│  - holds │                                         │          │
│    tools │                                         │  Google  │
│  - holds │                                         │   ADK    │
│    data  │                                         │  agents  │
└─────────-┘                                         └──────────┘
```

### Ping-pong streaming protocol

The `Run` RPC is a single bidirectional stream. Client and server exchange messages in a ping-pong pattern using `oneof` request/response envelopes:

**Client → Server (`RunRequest`):**

- `ExecuteRequest` — start a new agent session (or resume an existing one via optional `session_id`), sending the full agent tree definition and the `user_prompt` for this invocation
- `HeartbeatRequest` — keep the session alive
- `ToolCallResponse` — return tool execution results back to the server (oneof `output` or `error`)
- `CancelRequest` — cancel the current generation or a specific tool call
- `EndRequest` — gracefully terminate the session

**Server → Client (`RunResponse`):**

- `ExecuteResponse` — acknowledge session creation, return `session_id`
- `HeartbeatResponse` — heartbeat ack
- `ToolCallRequest` — ask the client to execute a tool with given input, includes `session_id` and `agent_path` for attribution
- `OutputChunk` — stream a chunk of LLM-generated text, tagged with `agent_path`; `last = true` signals the specific agent is done producing output; `is_thought = true` indicates model thinking content rather than final response
- `ErrorResponse` — a structured error with `ErrorCode`, human-readable `message`, and `retryable` flag
- `EndResponse` — session ended; `completed = true` when the agent tree finished naturally, `false` for client-initiated ends; includes `UsageSummary`

### Typical message flow

```
Client                           Server
  │                                │
  │─── ExecuteRequest ───────────► │  (agent tree, optional session_id)
  │◄── ExecuteResponse ──────────  │  (session_id assigned)
  │                                │
  │◄── OutputChunk [root, planner] │  (planner streams, last=false)
  │◄── ToolCallRequest ──────────  │  (session_id, tool_call_id)
  │─── ToolCallResponse ─────────► │  (oneof output/error)
  │◄── OutputChunk [root, planner] │  (planner continues, last=true)
  │                                │
  │◄── OutputChunk [root, writer]  │  (writer streams, last=true)
  │                                │
  │◄── EndResponse (completed) ─── │  (agent tree done, usage_summary)
```

### Cancellation flow

The client can send a `CancelRequest` at any time during execution. If `tool_call_id` is set, only that specific tool call is cancelled; otherwise all generation is cancelled. Cancellation is best-effort — the server responds with either an `ErrorResponse` or continues with the next step.

```
Client                           Server
  │                                │
  │─── CancelRequest ────────────►│  (session_id, optional tool_call_id)
  │◄── EndResponse (completed=f) ─│  (or ErrorResponse, or next step)
```

### Session resumption

If a client sends an `ExecuteRequest` with a previously returned `session_id`, the server attempts to reconnect to the existing session instead of creating a new one. When `session_id` is absent, a new session is created (default behavior).

## Error codes

The `ErrorResponse` includes a structured `ErrorCode` enum:

| Code | Name                            | Retryable? | Description                                              |
| ---- | ------------------------------- | ---------- | -------------------------------------------------------- |
| 0    | `ERROR_CODE_UNSPECIFIED`        | —          | Default / unknown                                        |
| 1    | `ERROR_CODE_INTERNAL`           | No         | Internal server error                                    |
| 2    | `ERROR_CODE_INVALID_AGENT_TREE` | No         | Malformed agent tree in `ExecuteRequest`                 |
| 3    | `ERROR_CODE_RATE_LIMITED`       | Yes        | Provider rate limit hit                                  |
| 4    | `ERROR_CODE_AUTH_FAILED`        | No         | Authentication/authorization failure                     |
| 5    | `ERROR_CODE_SESSION_NOT_FOUND`  | No         | Session ID not found (expired or invalid)                |
| 6    | `ERROR_CODE_MODEL_UNAVAILABLE`  | Yes        | Requested model string not supported or temporarily down |
| 7    | `ERROR_CODE_TIMEOUT`            | Yes        | Operation timed out                                      |

## Agent types

Agents are defined as a recursive tree in proto. The server uses Google ADK to execute them:

| Agent type          | Proto message     | Purpose                                                                                                                                                              |
| ------------------- | ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **LlmAgent**        | `LlmAgent`        | Core agent — has a model, tools, instruction (system prompt), sub-agents                                                                                             |
| **SequentialAgent** | `SequentialAgent` | Runs child agents in sequence                                                                                                                                        |
| **ParallelAgent**   | `ParallelAgent`   | Runs child agents in parallel                                                                                                                                        |
| **LoopAgent**       | `LoopAgent`       | Repeats child agents up to `max_iterations`; client controls continuation via a pre-defined tool through the standard `ToolCallRequest`/`ToolCallResponse` mechanism |

## Models

Models are specified as free-form strings in `LlmAgent.model`. The server routes to the correct provider based on the model name prefix (`claude-` for Anthropic, `gpt-`/`o1-`/`o3-`/`o4-`/`chatgpt-` for OpenAI, everything else for Gemini) and returns `ERROR_CODE_MODEL_UNAVAILABLE` for unsupported values.

### Gemini models

- `gemini-2.5-pro`
- `gemini-2.5-flash`
- `gemini-3-pro-preview`
- `gemini-3-flash-preview`

### Anthropic models (via [adk-anthropic-go](https://github.com/Alcova-AI/adk-anthropic-go))

- `claude-sonnet-4-5` / `claude-sonnet-4-5-20250929`
- `claude-opus-4-5` / `claude-opus-4-5-20251101`
- `claude-sonnet-4-0` / `claude-sonnet-4-20250514`
- `claude-opus-4-0` / `claude-opus-4-20250514`
- `claude-opus-4-1-20250805`
- `claude-haiku-4-5` / `claude-haiku-4-5-20251001`
- `claude-3-5-haiku-latest` / `claude-3-5-haiku-20241022`

### OpenAI models (via [adk-go-openai](https://github.com/byebyebruce/adk-go-openai))

- `gpt-4o` / `gpt-4o-mini`
- `gpt-4.1` / `gpt-4.1-mini` / `gpt-4.1-nano`
- `gpt-5.1`
- `o1` / `o1-mini` / `o1-preview`
- `o3` / `o3-mini`
- `o4-mini`
- `chatgpt-4o-latest`

### Environment variables

| Variable            | Required | Description                                          |
| ------------------- | -------- | ---------------------------------------------------- |
| `GEMINI_API_KEY`    | No*      | API key for Google Gemini models                     |
| `ANTHROPIC_API_KEY` | No*      | API key for Anthropic Claude models                  |
| `OPENAI_API_KEY`    | No*      | API key for OpenAI models                            |

\* At least one of `GEMINI_API_KEY`, `ANTHROPIC_API_KEY`, or `OPENAI_API_KEY` must be set.

## Tech stack

| Component           | Technology                                           |
| ------------------- | ---------------------------------------------------- |
| Language            | Go 1.24                                              |
| RPC framework       | ConnectRPC (`connectrpc.com/connect`)                |
| Transport           | HTTP/2 cleartext (h2c)                               |
| Agent orchestration | Google ADK (`google.golang.org/adk`)                 |
| LLM providers       | Google GenAI (`google.golang.org/genai`), Anthropic (`adk-anthropic-go`), OpenAI (`adk-go-openai`) |
| Protobuf codegen    | `buf` with `protoc-gen-go` + `protoc-gen-connect-go` |
| Proto style         | `simple` streaming (Connect simple streams)          |

## Project structure

```
agentd/
├── cmd/main.go                          # HTTP/2 server entrypoint
├── proto/agentd/v1/
│   ├── service.proto                    # Run RPC + request/response envelopes
│   └── types.proto                      # Agent, Tool, ErrorCode, TokenUsage
├── gen/proto/go/agentd/v1/             # Generated Go code (do not edit)
│   ├── service.pb.go
│   ├── types.pb.go
│   └── agentdv1connect/
│       └── service.connect.go           # AgentdHandler interface + AgentdClient
├── buf.gen.yaml                         # Buf codegen config
├── go.mod / go.sum
└── AGENTS.md                            # This file
```

## Key conventions

- **Proto is the source of truth.** All types flow from `proto/agentd/v1/`. Run `buf generate` to regenerate Go code after proto changes.
- **Never edit files under `gen/`.** They are overwritten on every `buf generate`.
- **Tool execution is always client-side.** The server must never execute tools directly — it sends `ToolCallRequest` and waits for `ToolCallResponse`.
- **Session lifecycle.** Every agent run is scoped to a `session_id` returned in `ExecuteResponse`. All subsequent messages reference this ID. Clients may resume sessions by passing the same `session_id` in a new `ExecuteRequest`.
- **Google ADK orchestration.** The server-side implementation translates the proto `Agent` tree into ADK agent/sub-agent structures and manages the agentic loop, forwarding tool calls to the client.
- **Models as strings.** Model identifiers are plain strings validated at runtime. See the Models section for currently supported values.
- **Instruction vs. user prompt.** `LlmAgent.instruction` is the static system prompt baked into the agent definition. `ExecuteRequest.user_prompt` is the per-invocation user input, passed through to ADK's `runner.Run()`. This keeps the agent tree a reusable template while the user's query varies per session.
- **Streaming output via `OutputChunk`.** LLM-generated text is streamed to the client in real-time. Each chunk carries `repeated string agent_path` — the ordered list from root to the producing agent (e.g. `["root", "planner", "researcher"]`) — so the client always knows which agent at which depth produced the text. The `last` field signals per-agent completion. The `is_thought` field is `true` when the chunk contains model thinking (chain-of-thought) rather than final response content, allowing clients to render or hide thinking separately.
- **`EndResponse` signals completion.** When the entire agent tree finishes, the server sends `EndResponse` with `completed = true` and the `UsageSummary`. No separate `FinalResponse` exists — `EndResponse` serves both natural completion and client-initiated termination.
- **Structured errors.** `ErrorResponse` carries an `ErrorCode` enum, a human-readable `message`, and a `retryable` boolean so clients can decide whether to retry automatically.
- **Tool results are unambiguous.** `ToolCallResponse` uses a `oneof result` with `output` and `error` branches — exactly one is always set.

## Implementation notes

- The `AgentdHandler` interface (generated in `agentdv1connect`) must be implemented to handle the `Run` bidi stream.
- The server should maintain per-session state (agent tree, ADK runner, accumulated token usage) keyed by `session_id`.
- `UsageSummary` is returned in `EndResponse` to give the client a billing summary of the session.
- Cancellation via `CancelRequest` is best-effort. The server should attempt to stop in-flight LLM calls or tool dispatches, then either send an `ErrorResponse` or proceed to the next step.
