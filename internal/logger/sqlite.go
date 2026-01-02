package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	domainv1 "github.com/syss-io/executor/gen/proto/go/domain/v1"
	"gorm.io/gorm"
)

type SQLiteLogger struct {
	db *gorm.DB
}

func NewSQLiteLogger(db *gorm.DB) *SQLiteLogger {
	return &SQLiteLogger{db: db}
}

func (s *SQLiteLogger) LogEvent(ctx context.Context, sessionID string, eventType string, eventData any) error {
	eventJSON, err := json.Marshal(eventData)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	return s.db.Create(&domainv1.SessionLogORM{
		SessionId: sessionID,
		Timestamp: time.Now().Format(time.RFC3339),
		EventType: eventType,
		EventData: string(eventJSON),
	}).Error
}
