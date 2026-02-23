export {
  AgentdClient,
  type ClientOptions,
  type RunOptions,
  type Event,
  type ToolHandler,
  type Agent,
  type Tool,
  type UsageSummary,
  type ErrorCode,
} from "./client";

export {
  AgentSchema,
  LlmAgentSchema,
  SequentialAgentSchema,
  ParallelAgentSchema,
  LoopAgentSchema,
  ToolSchema,
  TokenUsageSchema,
  UsageSummarySchema,
  ErrorCode as ErrorCodeEnum,
} from "../../gen/proto/ts/agentd/v1/types_pb";

export { create } from "@bufbuild/protobuf";
