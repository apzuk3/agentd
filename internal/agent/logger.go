package agent

// import (
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"log/slog"

// 	"google.golang.org/adk/session"
// )

// // ExecutionLoggerLegacy defines the interface for storing traceable execution data.
// type ExecutionLoggerLegacy interface {
// 	// LogEvent stores an ADK session event for a specific session.
// 	LogEvent(ctx context.Context, sessionID string, event *session.Event) error
// }

// // StdoutLogger implements ExecutionLogger by printing events to stdout.
// type StdoutLogger struct{}

// func (l *StdoutLogger) LogEvent(ctx context.Context, sessionID string, event *session.Event) error {
// 	// We'll log a summary of the event to slog, and optionally pretty-print the whole thing

// 	entry := map[string]any{
// 		"session_id": sessionID,
// 		"event_id":   event.ID,
// 		"author":     event.Author,
// 		"timestamp":  event.Timestamp,
// 	}

// 	if event.UsageMetadata != nil {
// 		entry["usage"] = map[string]int32{
// 			"prompt":     event.UsageMetadata.PromptTokenCount,
// 			"candidates": event.UsageMetadata.CandidatesTokenCount,
// 			"total":      event.UsageMetadata.TotalTokenCount,
// 		}
// 	}

// 	// Extract text content if available
// 	if event.Content != nil {
// 		var text string
// 		for _, part := range event.Content.Parts {
// 			if part.Text != "" {
// 				text += part.Text
// 			}
// 			if part.FunctionCall != nil {
// 				entry["tool_call"] = part.FunctionCall.Name
// 				entry["tool_args"] = part.FunctionCall.Args
// 			}
// 			if part.FunctionResponse != nil {
// 				entry["tool_response"] = part.FunctionResponse.Name
// 				entry["tool_output"] = part.FunctionResponse.Response
// 			}
// 		}
// 		if text != "" {
// 			entry["content"] = text
// 		}
// 	}

// 	data, err := json.MarshalIndent(entry, "", "  ")
// 	if err != nil {
// 		return fmt.Errorf("failed to marshal event entry: %w", err)
// 	}

// 	fmt.Printf("--- EXECUTION EVENT ---\n%s\n-----------------------\n", string(data))

// 	slog.Info("Logged execution event", "session_id", sessionID, "event_id", event.ID, "author", event.Author)
// 	return nil
// }
