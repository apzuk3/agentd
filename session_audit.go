package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// AuditEntry is a flat JSON-serializable record stored for each session event.
type AuditEntry struct {
	SessionID string           `json:"session_id"`
	Type      SessionEventType `json:"type"`
	Timestamp time.Time        `json:"timestamp"`
	Data      map[string]any   `json:"data,omitempty"`
}

// AuditStore is the persistence interface for audit entries.
// Implement this to write to any backend (Postgres, SQLite, DynamoDB, etc.).
type AuditStore interface {
	// Store persists a single audit entry. Implementations must be safe for
	// concurrent use.
	Store(ctx context.Context, entry AuditEntry) error
}

// SessionAudit is a SessionListener that serialises each session event into
// an AuditEntry and passes it to an AuditStore for persistence.
type SessionAudit struct {
	store AuditStore
}

// NewSessionAudit creates a listener backed by the given store.
func NewSessionAudit(store AuditStore) *SessionAudit {
	return &SessionAudit{store: store}
}

func (a *SessionAudit) OnSessionEvent(ctx context.Context, event SessionEvent) {
	entry := AuditEntry{
		SessionID: event.SessionID,
		Type:      event.Type,
		Timestamp: event.Timestamp,
		Data:      event.Data,
	}
	// Best-effort: store failures are intentionally swallowed so a broken
	// audit backend never takes down the session.
	_ = a.store.Store(ctx, entry)
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
	ID        uint             `gorm:"primaryKey;autoIncrement"`
	SessionID string           `gorm:"index;not null"`
	Type      SessionEventType `gorm:"not null"`
	Timestamp time.Time        `gorm:"not null"`
	Data      json.RawMessage  `gorm:"type:text"`
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
	_ SessionListener = (*SessionAudit)(nil)
	_ AuditStore      = NoopAuditStore{}
	_ AuditStore      = (*DatabaseAuditStore)(nil)
)
