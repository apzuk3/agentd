package agentd

import (
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// newSessionPluginBridge creates an ADK plugin that delegates every ADK
// lifecycle callback to the given PluginChain. Gate methods propagate errors
// back to the ADK runner so it can short-circuit operations (e.g. block a
// tool call or halt after exceeding a token budget).
//
// usageFn is called to obtain the cumulative session usage at the time of a
// model call; it is typically bound to Session.currentUsage.
func newSessionPluginBridge(sessionID string, chain *PluginChain, usageFn func() UsageInfo) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name: "session_plugin_bridge",

		OnUserMessageCallback: func(ctx agent.InvocationContext, content *genai.Content) (*genai.Content, error) {
			var textLen int
			if content != nil {
				for _, p := range content.Parts {
					if p.Text != "" {
						textLen = len(p.Text)
						break
					}
				}
			}
			chain.OnUserMessage(ctx, UserMessageInfo{
				SessionID:  sessionID,
				AgentName:  ctx.Agent().Name(),
				ContentLen: textLen,
			})
			return nil, nil
		},

		BeforeAgentCallback: func(ctx agent.CallbackContext) (*genai.Content, error) {
			if err := chain.BeforeAgent(ctx, AgentInfo{
				SessionID: sessionID,
				AgentName: ctx.AgentName(),
			}); err != nil {
				return nil, err
			}
			return nil, nil
		},

		AfterAgentCallback: func(ctx agent.CallbackContext) (*genai.Content, error) {
			chain.AfterAgent(ctx, AgentInfo{
				SessionID: sessionID,
				AgentName: ctx.AgentName(),
			})
			return nil, nil
		},

		BeforeModelCallback: func(ctx agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
			if err := chain.BeforeModelCall(ctx, ModelCallInfo{
				SessionID:       sessionID,
				AgentName:       ctx.AgentName(),
				Model:           req.Model,
				CumulativeUsage: usageFn(),
			}); err != nil {
				return nil, err
			}
			return nil, nil
		},

		AfterModelCallback: func(ctx agent.CallbackContext, resp *model.LLMResponse, respErr error) (*model.LLMResponse, error) {
			info := ModelCallResult{
				SessionID:       sessionID,
				AgentName:       ctx.AgentName(),
				CumulativeUsage: usageFn(),
				Err:             respErr,
			}
			if resp != nil && resp.UsageMetadata != nil {
				info.PromptTokens = int32(resp.UsageMetadata.PromptTokenCount)
				info.CompletionTokens = int32(resp.UsageMetadata.CandidatesTokenCount)
				info.TotalTokens = int32(resp.UsageMetadata.TotalTokenCount)
			}
			if err := chain.AfterModelCall(ctx, info); err != nil {
				return nil, err
			}
			return nil, nil
		},

		OnModelErrorCallback: func(ctx agent.CallbackContext, req *model.LLMRequest, err error) (*model.LLMResponse, error) {
			chain.OnError(ctx, ErrorInfo{
				SessionID: sessionID,
				Message:   "model error on " + req.Model,
				Err:       err,
			})
			return nil, nil
		},

		BeforeToolCallback: func(ctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
			if err := chain.BeforeToolCall(ctx, ToolCallInfo{
				SessionID: sessionID,
				ToolName:  t.Name(),
				AgentName: ctx.AgentName(),
				Args:      args,
			}); err != nil {
				return nil, err
			}
			return nil, nil
		},

		AfterToolCallback: func(ctx tool.Context, t tool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
			chain.AfterToolCall(ctx, ToolCallResult{
				SessionID: sessionID,
				ToolName:  t.Name(),
				AgentName: ctx.AgentName(),
				Result:    result,
				Err:       toolErr,
			})
			return nil, nil
		},

		OnToolErrorCallback: func(ctx tool.Context, t tool.Tool, args map[string]any, err error) (map[string]any, error) {
			chain.OnError(ctx, ErrorInfo{
				SessionID: sessionID,
				Message:   "tool error on " + t.Name(),
				Err:       err,
			})
			return nil, nil
		},

		OnEventCallback: func(ctx agent.InvocationContext, event *session.Event) (*session.Event, error) {
			return event, nil
		},
	})
}
