package agentd

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
)

type LoopAgentOption func(*loopagent.Config) error

func WithLoopAgentMaxIterations(maxIterations uint) LoopAgentOption {
	return func(cfg *loopagent.Config) error {
		cfg.MaxIterations = maxIterations
		return nil
	}
}

func LoopAgent(name string, options ...LoopAgentOption) (agent.Agent, error) {
	cfg := loopagent.Config{
		AgentConfig: agent.Config{
			Name: name,
		},
		MaxIterations: 3,
	}
	for _, option := range options {
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}

	return loopagent.New(cfg)
}
