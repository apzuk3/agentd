import type { MessageInitShape } from "@bufbuild/protobuf";
import { create } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import { createGrpcTransport } from "@connectrpc/connect-node";

import {
  Agentd,
  type RunRequest,
  RunRequestSchema,
} from "../../gen/proto/ts/agentd/v1/service_pb";

import {
  type Agent,
  type Tool,
  type UsageSummary,
  type ErrorCode,
  ToolSchema,
} from "../../gen/proto/ts/agentd/v1/types_pb";

// ---------------------------------------------------------------------------
// AsyncQueue — pushable async iterable for driving the bidi request stream
// ---------------------------------------------------------------------------

class AsyncQueue<T> implements AsyncIterable<T> {
  private buffer: T[] = [];
  private waiters: ((result: IteratorResult<T>) => void)[] = [];
  private closed = false;

  push(value: T): void {
    if (this.closed) return;
    const waiter = this.waiters.shift();
    if (waiter) {
      waiter({ value, done: false });
    } else {
      this.buffer.push(value);
    }
  }

  close(): void {
    this.closed = true;
    for (const w of this.waiters) {
      w({ value: undefined as never, done: true });
    }
    this.waiters = [];
  }

  [Symbol.asyncIterator](): AsyncIterator<T> {
    return {
      next: (): Promise<IteratorResult<T>> => {
        const buffered = this.buffer.shift();
        if (buffered !== undefined) {
          return Promise.resolve({ value: buffered, done: false });
        }
        if (this.closed) {
          return Promise.resolve({ value: undefined as never, done: true });
        }
        return new Promise<IteratorResult<T>>((resolve) =>
          this.waiters.push(resolve),
        );
      },
    };
  }
}

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export type { Agent, Tool, UsageSummary, ErrorCode };

export type Event =
  | {
      type: "output_chunk";
      agentPath: string[];
      content: string;
      last: boolean;
    }
  | {
      type: "error";
      code: ErrorCode;
      message: string;
      retryable: boolean;
    }
  | {
      type: "end";
      completed: boolean;
      usageSummary?: UsageSummary;
    };

export interface ClientOptions {
  /** Heartbeat interval in milliseconds (default: 30 000). */
  heartbeatIntervalMs?: number;
}

export interface RunOptions {
  /** Resume an existing session instead of creating a new one. */
  sessionId?: string;
  /** Abort signal to cancel the run. */
  signal?: AbortSignal;
}

/**
 * Tool handler function. Receives the parsed JSON input and returns a result
 * that will be JSON-serialised back to the server. Returning a string sends
 * it as-is; any other value is JSON.stringify'd.
 */
export type ToolHandler<T = unknown> = (
  input: T,
) => Promise<unknown> | unknown;

// ---------------------------------------------------------------------------
// Internal types
// ---------------------------------------------------------------------------

interface RegisteredTool {
  proto: Tool;
  handler: (rawInput: string) => Promise<string>;
}

// ---------------------------------------------------------------------------
// AgentdClient
// ---------------------------------------------------------------------------

export class AgentdClient {
  private heartbeatIntervalMs: number;
  private tools = new Map<string, RegisteredTool>();
  private transport;

  constructor(baseUrl: string, options?: ClientOptions) {
    this.heartbeatIntervalMs = options?.heartbeatIntervalMs ?? 30_000;
    this.transport = createGrpcTransport({
      baseUrl,
    });
  }

  /**
   * Register a tool with the client. The handler receives the parsed input
   * (JSON-deserialized to `T`) and should return a result (string or object).
   *
   * Optionally provide a JSON Schema object describing the input so the LLM
   * knows how to call the tool.
   */
  addTool<T = unknown>(
    name: string,
    description: string,
    handler: ToolHandler<T>,
    inputSchema?: Record<string, unknown>,
  ): void {
    const proto = create(ToolSchema, {
      name,
      description,
      inputSchema: inputSchema ? JSON.stringify(inputSchema) : undefined,
    });

    this.tools.set(name, {
      proto,
      handler: async (rawInput: string): Promise<string> => {
        let parsed: T = undefined as T;
        if (rawInput) {
          parsed = JSON.parse(rawInput) as T;
        }
        const result = await handler(parsed);
        return typeof result === "string" ? result : JSON.stringify(result);
      },
    });
  }

  /**
   * Return the proto Tool definition for a registered tool, for use when
   * constructing agent trees. Returns undefined if the tool is not registered.
   */
  tool(name: string): Tool | undefined {
    return this.tools.get(name)?.proto;
  }

  /**
   * Open a bidirectional stream to the server, send the agent tree and user
   * prompt, and yield events. Tool calls and heartbeats are handled
   * internally. Breaking out of the iterator cancels the session.
   */
  async *run(
    agent: Agent,
    userPrompt: string,
    options?: RunOptions,
  ): AsyncGenerator<Event> {
    const ac = new AbortController();
    if (options?.signal) {
      options.signal.addEventListener("abort", () => ac.abort(), {
        once: true,
      });
    }

    type ReqInit = MessageInitShape<typeof RunRequestSchema>;
    const requests = new AsyncQueue<ReqInit>();

    const execValue: Record<string, unknown> = { agent, userPrompt };
    if (options?.sessionId) {
      execValue.sessionId = options.sessionId;
    }
    requests.push({
      request: { case: "execute" as const, value: execValue },
    } as ReqInit);

    const rpcClient = createClient(Agentd, this.transport);
    const responses = rpcClient.run(requests, { signal: ac.signal });

    let sessionId = "";
    let heartbeatTimer: ReturnType<typeof setInterval> | undefined;

    try {
      for await (const resp of responses) {
        switch (resp.response.case) {
          case "execute": {
            sessionId = resp.response.value.sessionId;
            heartbeatTimer = setInterval(() => {
              requests.push({
                request: {
                  case: "heartbeat" as const,
                  value: { sessionId },
                },
              } as ReqInit);
            }, this.heartbeatIntervalMs);
            break;
          }

          case "toolCall": {
            const tc = resp.response.value;
            const registered = this.tools.get(tc.toolName);

            if (!registered) {
              requests.push({
                request: {
                  case: "toolCallResponse" as const,
                  value: {
                    sessionId,
                    toolCallId: tc.toolCallId,
                    toolName: tc.toolName,
                    result: {
                      case: "error" as const,
                      value: `unknown tool: ${tc.toolName}`,
                    },
                  },
                },
              } as ReqInit);
              break;
            }

            try {
              const output = await registered.handler(tc.toolInput);
              requests.push({
                request: {
                  case: "toolCallResponse" as const,
                  value: {
                    sessionId,
                    toolCallId: tc.toolCallId,
                    toolName: tc.toolName,
                    result: { case: "output" as const, value: output },
                  },
                },
              } as ReqInit);
            } catch (err) {
              requests.push({
                request: {
                  case: "toolCallResponse" as const,
                  value: {
                    sessionId,
                    toolCallId: tc.toolCallId,
                    toolName: tc.toolName,
                    result: {
                      case: "error" as const,
                      value: String(err),
                    },
                  },
                },
              } as ReqInit);
            }
            break;
          }

          case "outputChunk": {
            const chunk = resp.response.value;
            yield {
              type: "output_chunk",
              agentPath: [...chunk.agentPath],
              content: chunk.content,
              last: chunk.last,
            };
            break;
          }

          case "error": {
            const e = resp.response.value;
            yield {
              type: "error",
              code: e.code,
              message: e.message,
              retryable: e.retryable,
            };
            break;
          }

          case "end": {
            const end = resp.response.value;
            yield {
              type: "end",
              completed: end.completed,
              usageSummary: end.usageSummary,
            };
            return;
          }

          case "heartbeat":
            break;
        }
      }
    } finally {
      if (heartbeatTimer) clearInterval(heartbeatTimer);
      requests.close();
      ac.abort();
    }
  }
}
