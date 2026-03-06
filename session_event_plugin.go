package agentd

import (
	"context"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// newSessionEventPlugin creates an ADK plugin that bridges ADK lifecycle
// callbacks into SessionEvents emitted through the given emitter.
func newSessionEventPlugin(sessionID string, emitter *SessionEventEmitter) (*plugin.Plugin, error) {
	emit := func(ctx context.Context, eventType SessionEventType, data map[string]any) {
		emitter.Emit(ctx, SessionEvent{
			Type:      eventType,
			Timestamp: time.Now(),
			SessionID: sessionID,
			Data:      data,
		})
	}

	return plugin.New(plugin.Config{
		Name: "session_event_bridge",

		OnUserMessageCallback: func(ctx agent.InvocationContext, content *genai.Content) (*genai.Content, error) {
			var text string
			if content != nil {
				for _, p := range content.Parts {
					if p.Text != "" {
						text = p.Text
						break
					}
				}
			}
			emit(ctx, EventUserMessage, map[string]any{
				"agent":       ctx.Agent().Name(),
				"content_len": len(text),
			})
			return nil, nil
		},

		BeforeAgentCallback: func(ctx agent.CallbackContext) (*genai.Content, error) {
			emit(ctx, EventAgentStarted, map[string]any{
				"agent": ctx.AgentName(),
			})
			return nil, nil
		},

		AfterAgentCallback: func(ctx agent.CallbackContext) (*genai.Content, error) {
			emit(ctx, EventAgentEnded, map[string]any{
				"agent": ctx.AgentName(),
			})
			return nil, nil
		},

		BeforeModelCallback: func(ctx agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
			emit(ctx, EventModelRequest, map[string]any{
				"agent": ctx.AgentName(),
				"model": req.Model,
			})
			return nil, nil
		},

		AfterModelCallback: func(ctx agent.CallbackContext, resp *model.LLMResponse, respErr error) (*model.LLMResponse, error) {
			data := map[string]any{
				"agent": ctx.AgentName(),
			}
			if resp != nil && resp.UsageMetadata != nil {
				data["prompt_tokens"] = resp.UsageMetadata.PromptTokenCount
				data["completion_tokens"] = resp.UsageMetadata.CandidatesTokenCount
				data["total_tokens"] = resp.UsageMetadata.TotalTokenCount
			}
			if respErr != nil {
				data["error"] = respErr.Error()
			}
			emit(ctx, EventModelResponse, data)
			return nil, nil
		},

		OnModelErrorCallback: func(ctx agent.CallbackContext, req *model.LLMRequest, err error) (*model.LLMResponse, error) {
			emit(ctx, EventModelError, map[string]any{
				"agent": ctx.AgentName(),
				"model": req.Model,
				"error": err.Error(),
			})
			return nil, nil
		},

		BeforeToolCallback: func(ctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
			emit(ctx, EventToolStarted, map[string]any{
				"agent":     ctx.AgentName(),
				"tool_name": t.Name(),
			})
			return nil, nil
		},

		AfterToolCallback: func(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
			data := map[string]any{
				"agent":     ctx.AgentName(),
				"tool_name": t.Name(),
			}
			if err != nil {
				data["error"] = err.Error()
			}
			emit(ctx, EventToolCompleted, data)
			return nil, nil
		},

		OnToolErrorCallback: func(ctx tool.Context, t tool.Tool, args map[string]any, err error) (map[string]any, error) {
			emit(ctx, EventToolError, map[string]any{
				"agent":     ctx.AgentName(),
				"tool_name": t.Name(),
				"error":     err.Error(),
			})
			return nil, nil
		},

		OnEventCallback: func(ctx agent.InvocationContext, event *session.Event) (*session.Event, error) {
			return event, nil
		},
	})
}

