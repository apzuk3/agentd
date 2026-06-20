package agentd

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/parallelagent"
)

func ParallelAgent(agents ...agent.Agent) (agent.Agent, error) {
	return parallelagent.New(parallelagent.Config{
		AgentConfig: agent.Config{
			Name:      "parallel",
			SubAgents: agents,
		},
	})
}
