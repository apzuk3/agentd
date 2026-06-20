package agentd

import (
	"context"
	"strings"

	"google.golang.org/adk/agent"
)

// LaunchSync runs the agent against a single user input and blocks until the
// whole reply is available, returning it as a string. It is the synchronous
// counterpart to [LaunchStreaming]; both share the same engine and the same
// [RunOption] set.
//
// It runs without incremental streaming and concatenates the text of every
// StreamEventFinal, which yields the complete answer. When multiple agents
// participate in one invocation, their final texts are joined in arrival
// order.
//
// Any error from the underlying run aborts the call and is returned with the
// text accumulated so far.
//
// Example:
//
//	out, err := agentd.LaunchSync(ctx, root, "deploy main to prod")
//	if err != nil {
//		return err
//	}
//	fmt.Println(out)
func LaunchSync(ctx context.Context, a agent.Agent, input string, opts ...RunOption) (string, error) {
	var out strings.Builder
	for ev, err := range stream(ctx, a, input, agent.StreamingModeNone, opts...) {
		if err != nil {
			return out.String(), err
		}
		if ev.Kind == StreamEventFinal {
			out.WriteString(ev.Text)
		}
	}
	return out.String(), nil
}
