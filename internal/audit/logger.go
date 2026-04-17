package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditEntry struct {
	EntityType string
	EntityID   uuid.UUID
	EventType  string
	Payload    interface{}
}

func Log(ctx context.Context, db *pgxpool.Pool, entry AuditEntry) error {
	return LogWithConn(ctx, db, entry)
}

func LogWithConn(ctx context.Context, db interface{}, entry AuditEntry) error {
	payloadJSON, err := json.Marshal(entry.Payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	query := `
		INSERT INTO audit_log (entity_type, entity_id, event_type, payload)
		VALUES ($1, $2, $3, $4)
	`

	switch conn := db.(type) {
	case *pgxpool.Pool:
		_, err = conn.Exec(ctx, query, entry.EntityType, entry.EntityID, entry.EventType, payloadJSON)
	case pgx.Tx:
		_, err = conn.Exec(ctx, query, entry.EntityType, entry.EntityID, entry.EventType, payloadJSON)
	default:
		return fmt.Errorf("unsupported connection type")
	}

	if err != nil {
		return fmt.Errorf("failed to write audit log: %w", err)
	}

	return nil
}
