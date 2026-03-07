package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// AuditEntryType identifies the kind of audit record.
type AuditEntryType string

const (
	AuditSessionStarted AuditEntryType = "session_started"
	AuditSessionEnded   AuditEntryType = "session_ended"
	AuditAgentStarted   AuditEntryType = "agent_started"
	AuditAgentEnded     AuditEntryType = "agent_ended"
	AuditModelRequest   AuditEntryType = "model_request"
	AuditModelResponse  AuditEntryType = "model_response"
	AuditToolStarted    AuditEntryType = "tool_started"
	AuditToolCompleted  AuditEntryType = "tool_completed"
	AuditToolDispatched AuditEntryType = "tool_dispatched"
	AuditToolResponse   AuditEntryType = "tool_response"
	AuditOutputChunk    AuditEntryType = "output_chunk"
	AuditUserMessage    AuditEntryType = "user_message"
	AuditError          AuditEntryType = "error"
)

// AuditEntry is a flat JSON-serializable record stored for each session event.
type AuditEntry struct {
	SessionID string         `json:"session_id"`
	Type      AuditEntryType `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

// AuditStore is the persistence interface for audit entries.
// Implement this to write to any backend (Postgres, SQLite, DynamoDB, etc.).
type AuditStore interface {
	// Store persists a single audit entry. Implementations must be safe for
	// concurrent use.
	Store(ctx context.Context, entry AuditEntry) error
}

// AuditPlugin is a SessionPlugin that serialises each lifecycle event into
// an AuditEntry and passes it to an AuditStore for persistence.
type AuditPlugin struct {
	BasePlugin
	store AuditStore
}

// NewAuditPlugin creates a plugin backed by the given store.
func NewAuditPlugin(store AuditStore) *AuditPlugin {
	return &AuditPlugin{store: store}
}

func (a *AuditPlugin) storeEntry(ctx context.Context, typ AuditEntryType, sessionID string, data map[string]any) {
	_ = a.store.Store(ctx, AuditEntry{
		SessionID: sessionID,
		Type:      typ,
		Timestamp: time.Now(),
		Data:      data,
	})
}

func (a *AuditPlugin) OnSessionStart(ctx context.Context, info SessionStartInfo) error {
	a.storeEntry(ctx, AuditSessionStarted, info.SessionID, map[string]any{
		"root_agent":  info.RootAgent,
		"tool_count":  info.ToolCount,
		"user_prompt": info.UserPrompt,
	})
	return nil
}

func (a *AuditPlugin) OnSessionEnd(ctx context.Context, info SessionEndInfo) {
	data := map[string]any{
		"prompt_tokens":     info.Usage.PromptTokens,
		"completion_tokens": info.Usage.CompletionTokens,
		"cached_tokens":     info.Usage.CachedTokens,
		"thoughts_tokens":   info.Usage.ThoughtsTokens,
		"total_tokens":      info.Usage.TotalTokens,
		"llm_calls":         info.Usage.LLMCalls,
	}
	if info.Err != nil {
		data["error"] = info.Err.Error()
	}
	a.storeEntry(ctx, AuditSessionEnded, info.SessionID, data)
}

func (a *AuditPlugin) BeforeAgent(ctx context.Context, info AgentInfo) error {
	a.storeEntry(ctx, AuditAgentStarted, info.SessionID, map[string]any{
		"agent": info.AgentName,
	})
	return nil
}

func (a *AuditPlugin) AfterAgent(ctx context.Context, info AgentInfo) {
	a.storeEntry(ctx, AuditAgentEnded, info.SessionID, map[string]any{
		"agent": info.AgentName,
	})
}

func (a *AuditPlugin) BeforeModelCall(ctx context.Context, info ModelCallInfo) error {
	a.storeEntry(ctx, AuditModelRequest, info.SessionID, map[string]any{
		"agent": info.AgentName,
		"model": info.Model,
	})
	return nil
}

func (a *AuditPlugin) AfterModelCall(ctx context.Context, info ModelCallResult) error {
	data := map[string]any{
		"agent":             info.AgentName,
		"prompt_tokens":     info.PromptTokens,
		"completion_tokens": info.CompletionTokens,
		"total_tokens":      info.TotalTokens,
	}
	if info.Err != nil {
		data["error"] = info.Err.Error()
	}
	a.storeEntry(ctx, AuditModelResponse, info.SessionID, data)
	return nil
}

func (a *AuditPlugin) BeforeToolCall(ctx context.Context, info ToolCallInfo) error {
	a.storeEntry(ctx, AuditToolStarted, info.SessionID, map[string]any{
		"agent":     info.AgentName,
		"tool_name": info.ToolName,
	})
	return nil
}

func (a *AuditPlugin) AfterToolCall(ctx context.Context, info ToolCallResult) {
	data := map[string]any{
		"agent":     info.AgentName,
		"tool_name": info.ToolName,
	}
	if info.Err != nil {
		data["error"] = info.Err.Error()
	}
	a.storeEntry(ctx, AuditToolCompleted, info.SessionID, data)
}

func (a *AuditPlugin) OnToolDispatched(ctx context.Context, info ToolDispatchInfo) {
	a.storeEntry(ctx, AuditToolDispatched, info.SessionID, map[string]any{
		"tool_call_id": info.ToolCallID,
		"tool_name":    info.ToolName,
		"agent_path":   info.AgentPath,
		"input_len":    info.InputLen,
	})
}

func (a *AuditPlugin) OnToolResponse(ctx context.Context, info ToolResponseInfo) {
	a.storeEntry(ctx, AuditToolResponse, info.SessionID, map[string]any{
		"tool_call_id": info.ToolCallID,
		"tool_name":    info.ToolName,
	})
}

func (a *AuditPlugin) OnOutputChunk(ctx context.Context, info OutputChunkInfo) {
	a.storeEntry(ctx, AuditOutputChunk, info.SessionID, map[string]any{
		"agent":       info.AgentName,
		"is_thought":  info.IsThought,
		"is_final":    info.IsFinal,
		"content_len": info.ContentLen,
	})
}

func (a *AuditPlugin) OnUserMessage(ctx context.Context, info UserMessageInfo) {
	a.storeEntry(ctx, AuditUserMessage, info.SessionID, map[string]any{
		"agent":       info.AgentName,
		"content_len": info.ContentLen,
	})
}

func (a *AuditPlugin) OnError(ctx context.Context, info ErrorInfo) {
	data := map[string]any{
		"message": info.Message,
	}
	if info.Err != nil {
		data["error"] = info.Err.Error()
	}
	a.storeEntry(ctx, AuditError, info.SessionID, data)
}

// MarshalEntry serialises an AuditEntry to JSON. Useful for transports or
// stores that accept raw bytes.
func MarshalEntry(entry AuditEntry) ([]byte, error) {
	return json.Marshal(entry)
}

// NoopAuditStore is a no-op implementation of AuditStore. Useful as a
// placeholder until a real backend is wired in.
type NoopAuditStore struct{}

func (NoopAuditStore) Store(context.Context, AuditEntry) error { return nil }

// auditEntryRow is the GORM model for the audit_entries table.
type auditEntryRow struct {
	ID        uint            `gorm:"primaryKey;autoIncrement"`
	SessionID string          `gorm:"index;not null"`
	Type      AuditEntryType  `gorm:"not null"`
	Timestamp time.Time       `gorm:"not null"`
	Data      json.RawMessage `gorm:"type:text"`
}

func (auditEntryRow) TableName() string { return "audit_entries" }

// DatabaseAuditStore persists audit entries to a relational database via GORM.
// It auto-migrates the audit_entries table on creation.
type DatabaseAuditStore struct {
	db *gorm.DB
}

// NewDatabaseAuditStore creates a store backed by the given GORM database and
// runs AutoMigrate to ensure the audit_entries table exists.
func NewDatabaseAuditStore(db *gorm.DB) (*DatabaseAuditStore, error) {
	if err := db.AutoMigrate(&auditEntryRow{}); err != nil {
		return nil, fmt.Errorf("migrating audit_entries table: %w", err)
	}
	return &DatabaseAuditStore{db: db}, nil
}

func (s *DatabaseAuditStore) Store(ctx context.Context, entry AuditEntry) error {
	data, err := json.Marshal(entry.Data)
	if err != nil {
		return fmt.Errorf("marshalling audit data: %w", err)
	}

	row := auditEntryRow{
		SessionID: entry.SessionID,
		Type:      entry.Type,
		Timestamp: entry.Timestamp,
		Data:      data,
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

var (
	_ SessionPlugin = (*AuditPlugin)(nil)
	_ AuditStore    = NoopAuditStore{}
	_ AuditStore    = (*DatabaseAuditStore)(nil)
)
