package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	domainv1 "github.com/syss-io/executor/gen/proto/go/domain/v1"
	executorv1 "github.com/syss-io/executor/gen/proto/go/executor/v1"
	"github.com/syss-io/executor/gen/proto/go/executor/v1/executorv1connect"
	"golang.org/x/net/http2"
)

func RunClient(ctx context.Context, url string) error {
	httpClient := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	}
	client := executorv1connect.NewExecutorClient(httpClient, url)
	stream := client.AgentSession(ctx)

	// Handshake: Send RunRequest with agents and tools
	agents := []*executorv1.StartSessionRequest_Agent{
		{
			Name:        "ArithmeticAgent",
			Description: "An agent that can perform basic arithmetic operations",
			Instruction: "You are an arithmetic expert. Use your tools to solve math problems.",
			Model:       domainv1.Model_MODEL_GEMINI_2_5_FLASH,
			Tools: []*executorv1.StartSessionRequest_Agent_Tool{
				{
					Name:        "add",
					Description: "Adds two numbers",
					InputSchema: `{"type": "object", "properties": {"a": {"type": "number"}, "b": {"type": "number"}}, "required": ["a", "b"]}`,
				},
				{
					Name:        "multiply",
					Description: "Multiplies two numbers",
					InputSchema: `{"type": "object", "properties": {"a": {"type": "number"}, "b": {"type": "number"}}, "required": ["a", "b"]}`,
				},
				{
					Name:        "get_time",
					Description: "Returns the current time",
					InputSchema: `{"type": "object", "properties": {}}`,
				},
			},
		},
		{
			Name:        "StringAgent",
			Description: "An agent that can manipulate strings",
			Instruction: "You are a string manipulation expert. Use your tools to process text.",
			Model:       domainv1.Model_MODEL_GEMINI_2_5_FLASH,
			Tools: []*executorv1.StartSessionRequest_Agent_Tool{
				{
					Name:        "upper_case",
					Description: "Converts a string to uppercase",
					InputSchema: `{"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}`,
				},
				{
					Name:        "reverse_string",
					Description: "Reverses a string",
					InputSchema: `{"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}`,
				},
				{
					Name:        "get_word_count",
					Description: "Returns the number of words in a string",
					InputSchema: `{"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}`,
				},
			},
		},
	}

	runReq := &executorv1.StartSessionRequest{
		Message: &executorv1.StartSessionRequest_RunRequest_{
			RunRequest: &executorv1.StartSessionRequest_RunRequest{
				Workflow:    executorv1.StartSessionRequest_RunRequest_WORKFLOW_SEQUENTIAL,
				Instruction: "You are a coordinator that uses special agents to solve problems. First use ArithmeticAgent for math, then use StringAgent for text.",
				Agents:      agents,
				UserMessage: "Calculate 15 + 27, then multiply the result by 2. After that, take the word 'Hello' and reverse it. Finally, give me the word count of 'This is a test message'.",
				Model:       domainv1.Model_MODEL_GEMINI_2_5_FLASH,
			},
		},
	}

	if err := stream.Send(runReq); err != nil {
		return fmt.Errorf("failed to send run request: %w", err)
	}

	slog.Info("RunRequest sent, waiting for responses...")

	for {
		resp, err := stream.Receive()
		if err == io.EOF {
			slog.Info("Stream closed by server")
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to receive message: %w", err)
		}

		switch m := resp.Message.(type) {
		case *executorv1.StartSessionResponse_RunResponse_:
			fmt.Printf("\n[AGENT RESPONSE]: %s\n", m.RunResponse.Content)
			fmt.Printf("[TOKEN USAGE]: Input: %d, Output: %d\n\n", m.RunResponse.InputTokenCount, m.RunResponse.OutputTokenCount)
		case *executorv1.StartSessionResponse_ToolCallRequest_:
			req := m.ToolCallRequest
			slog.Info("Received ToolCallRequest", "agent", req.AgentName, "tool", req.ToolName, "input", req.Input)

			output, toolErr := handleToolCall(req.ToolName, req.Input)

			status := executorv1.StartSessionRequest_ToolCallResponse_STATUS_SUCCESS
			errMsg := ""
			if toolErr != nil {
				status = executorv1.StartSessionRequest_ToolCallResponse_STATUS_ERROR
				errMsg = toolErr.Error()
			}

			toolResp := &executorv1.StartSessionRequest{
				Message: &executorv1.StartSessionRequest_ToolCallResponse_{
					ToolCallResponse: &executorv1.StartSessionRequest_ToolCallResponse{
						RequestId:       req.RequestId,
						Status:          status,
						Output:          output,
						Error:           errMsg,
						ExecutionTimeMs: 0, // Optional
					},
				},
			}

			if err := stream.Send(toolResp); err != nil {
				return fmt.Errorf("failed to send tool call response: %w", err)
			}
			slog.Info("Sent ToolCallResponse", "request_id", req.RequestId)

		case *executorv1.StartSessionResponse_Error_:
			slog.Error("Received error from server", "code", m.Error.Code, "message", m.Error.Message)
		case *executorv1.StartSessionResponse_HeartbeatAck_:
			slog.Debug("Received HeartbeatAck")
		case *executorv1.StartSessionResponse_SessionEndAck_:
			slog.Info("Session ended by server")
			return nil
		}
	}
}

func handleToolCall(toolName string, inputJSON string) (string, error) {
	var input map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return "", fmt.Errorf("invalid input JSON: %w", err)
	}

	switch toolName {
	case "add":
		a, _ := input["a"].(float64)
		b, _ := input["b"].(float64)
		return fmt.Sprintf(`{"result": %f}`, a+b), nil
	case "multiply":
		a, _ := input["a"].(float64)
		b, _ := input["b"].(float64)
		return fmt.Sprintf(`{"result": %f}`, a*b), nil
	case "get_time":
		return fmt.Sprintf(`{"time": "%s"}`, time.Now().Format(time.RFC3339)), nil
	case "upper_case":
		text, _ := input["text"].(string)
		return fmt.Sprintf(`{"text": "%s"}`, strings.ToUpper(text)), nil
	case "reverse_string":
		text, _ := input["text"].(string)
		runes := []rune(text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return fmt.Sprintf(`{"text": "%s"}`, string(runes)), nil
	case "get_word_count":
		text, _ := input["text"].(string)
		// Very simple word count
		count := 0
		inWord := false
		for _, r := range text {
			if r == ' ' {
				inWord = false
			} else if !inWord {
				inWord = true
				count++
			}
		}
		return fmt.Sprintf(`{"count": %d}`, count), nil
	default:
		return "", fmt.Errorf("tool not found: %s", toolName)
	}
}
