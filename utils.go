package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

// createModel creates an ADK model from a model name string and API key.
func createModel(ctx context.Context, modelName, apiKey string) (model.LLM, error) {
	return gemini.NewModel(ctx, modelName, &genai.ClientConfig{
		APIKey: apiKey,
	})
}

// createTool creates an ADK tool from a proto Tool definition. The returned
// tool proxies execution to the client via sess.DispatchToolCall, blocking
// until the client responds.
func createTool(protoTool *agentdv1.Tool, sess *Session, agentPath []string) (tool.Tool, error) {
	cfg := functiontool.Config{
		Name:        protoTool.GetName(),
		Description: protoTool.GetDescription(),
	}

	if s := protoTool.GetInputSchema(); s != "" {
		var schema jsonschema.Schema
		if err := json.Unmarshal([]byte(s), &schema); err != nil {
			return nil, fmt.Errorf("parsing input schema for tool %q: %w", cfg.Name, err)
		}
		cfg.InputSchema = &schema
	}

	path := make([]string, len(agentPath))
	copy(path, agentPath)
	toolName := protoTool.GetName()

	return functiontool.New(cfg, func(ctx tool.Context, args map[string]any) (map[string]any, error) {
		argsJSON, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("marshalling tool args: %w", err)
		}

		resp, err := sess.DispatchToolCall(ctx, ctx.FunctionCallID(), toolName, string(argsJSON), path)
		if err != nil {
			return nil, err
		}

		switch r := resp.GetResult().(type) {
		case *agentdv1.RunRequest_ToolCallResponse_Output:
			var result map[string]any
			if err := json.Unmarshal([]byte(r.Output), &result); err != nil {
				return map[string]any{"result": r.Output}, nil
			}
			return result, nil
		case *agentdv1.RunRequest_ToolCallResponse_Error:
			return nil, errors.New(r.Error)
		}
		return nil, errors.New("empty tool call response")
	})
}

// createAgent recursively converts a proto Agent tree into ADK agent objects.
// It populates agentPaths with the path from root to each agent, keyed by name.
func createAgent(
	ctx context.Context,
	protoAgent *agentdv1.Agent,
	sess *Session,
	apiKey string,
	parentPath []string,
	agentPaths map[string][]string,
) (agent.Agent, error) {
	if protoAgent == nil {
		return nil, errors.New("agent definition is nil")
	}

	name := protoAgent.GetName()
	if name == "" {
		return nil, errors.New("agent name is required")
	}

	currentPath := append(append([]string{}, parentPath...), name)
	agentPaths[name] = currentPath

	switch {
	case protoAgent.GetLlm() != nil:
		return createLLMAgent(ctx, protoAgent, sess, apiKey, currentPath, agentPaths)
	case protoAgent.GetSequential() != nil:
		return createSequentialAgent(ctx, protoAgent, sess, apiKey, currentPath, agentPaths)
	case protoAgent.GetParallel() != nil:
		return createParallelAgent(ctx, protoAgent, sess, apiKey, currentPath, agentPaths)
	case protoAgent.GetLoop() != nil:
		return createLoopAgent(ctx, protoAgent, sess, apiKey, currentPath, agentPaths)
	default:
		return nil, fmt.Errorf("agent %q has no agent_type set", name)
	}
}

func createLLMAgent(
	ctx context.Context,
	protoAgent *agentdv1.Agent,
	sess *Session,
	apiKey string,
	currentPath []string,
	agentPaths map[string][]string,
) (agent.Agent, error) {
	llm := protoAgent.GetLlm()

	m, err := createModel(ctx, llm.GetModel(), apiKey)
	if err != nil {
		return nil, fmt.Errorf("creating model for agent %q: %w", protoAgent.GetName(), err)
	}

	var tools []tool.Tool
	for _, pt := range llm.GetTools() {
		t, err := createTool(pt, sess, currentPath)
		if err != nil {
			return nil, fmt.Errorf("creating tool %q for agent %q: %w", pt.GetName(), protoAgent.GetName(), err)
		}
		tools = append(tools, t)
	}

	var subAgents []agent.Agent
	for _, sa := range llm.GetSubAgents() {
		a, err := createAgent(ctx, sa, sess, apiKey, currentPath, agentPaths)
		if err != nil {
			return nil, fmt.Errorf("creating sub-agent for %q: %w", protoAgent.GetName(), err)
		}
		subAgents = append(subAgents, a)
	}

	return llmagent.New(llmagent.Config{
		Name:        protoAgent.GetName(),
		Description: protoAgent.GetDescription(),
		Model:       m,
		Tools:       tools,
		SubAgents:   subAgents,
		Instruction: llm.GetInstruction(),
	})
}

func createSequentialAgent(
	ctx context.Context,
	protoAgent *agentdv1.Agent,
	sess *Session,
	apiKey string,
	currentPath []string,
	agentPaths map[string][]string,
) (agent.Agent, error) {
	seq := protoAgent.GetSequential()

	subAgents, err := buildSubAgents(ctx, seq.GetAgents(), sess, apiKey, currentPath, agentPaths)
	if err != nil {
		return nil, err
	}

	return sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        protoAgent.GetName(),
			Description: protoAgent.GetDescription(),
			SubAgents:   subAgents,
		},
	})
}

func createParallelAgent(
	ctx context.Context,
	protoAgent *agentdv1.Agent,
	sess *Session,
	apiKey string,
	currentPath []string,
	agentPaths map[string][]string,
) (agent.Agent, error) {
	par := protoAgent.GetParallel()

	subAgents, err := buildSubAgents(ctx, par.GetAgents(), sess, apiKey, currentPath, agentPaths)
	if err != nil {
		return nil, err
	}

	return parallelagent.New(parallelagent.Config{
		AgentConfig: agent.Config{
			Name:        protoAgent.GetName(),
			Description: protoAgent.GetDescription(),
			SubAgents:   subAgents,
		},
	})
}

func createLoopAgent(
	ctx context.Context,
	protoAgent *agentdv1.Agent,
	sess *Session,
	apiKey string,
	currentPath []string,
	agentPaths map[string][]string,
) (agent.Agent, error) {
	loop := protoAgent.GetLoop()

	subAgents, err := buildSubAgents(ctx, loop.GetAgents(), sess, apiKey, currentPath, agentPaths)
	if err != nil {
		return nil, err
	}

	return loopagent.New(loopagent.Config{
		AgentConfig: agent.Config{
			Name:        protoAgent.GetName(),
			Description: protoAgent.GetDescription(),
			SubAgents:   subAgents,
		},
		MaxIterations: uint(loop.GetMaxIterations()),
	})
}

func buildSubAgents(
	ctx context.Context,
	protoAgents []*agentdv1.Agent,
	sess *Session,
	apiKey string,
	parentPath []string,
	agentPaths map[string][]string,
) ([]agent.Agent, error) {
	var agents []agent.Agent
	for _, pa := range protoAgents {
		a, err := createAgent(ctx, pa, sess, apiKey, parentPath, agentPaths)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, nil
}
