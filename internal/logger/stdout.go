package logger

import (
	"context"
	"fmt"
	"log/slog"
)

type StdoutLogger struct {
}

func NewStdout() *StdoutLogger {
	return &StdoutLogger{}
}

func (s *StdoutLogger) LogEvent(ctx context.Context, sessionID string, eventType string, eventData any) error {
	fmt.Printf("--- EXECUTION EVENT ---\n%s\n", eventData)
	slog.Info("Logged execution event", "session_id", sessionID, "event_type", eventType)
	fmt.Println("-----------------------")

	return nil
}
