// Package audit writes to the audit_events table.
//
// That table already existed in the schema (migration 001) - salon_id,
// actor_id, actor_role, entity_type/id, action, old/new values, IP,
// append-only, 7-year retention comment right there in the SQL - and
// nothing anywhere in the codebase ever wrote to it. This package is what
// finally uses it, starting with admin logins and admin approve/reject
// decisions, the two actions that most need a permanent record: a small,
// deliberately powerful set of accounts, and every action they take.
//
// Standalone rather than living inside the auth or onboarding domain
// specifically, because logging is not owned by any one domain - the auth
// domain needs it for login events, onboarding needs it for approve/reject,
// and whatever else eventually needs an admin action logged shouldn't have
// to import a domain that has nothing to do with it.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event is one row. EntityID is required by the database's own NOT NULL
// constraint - callers with no real entity to attach to (a login attempt
// for an email that matched no user at all) should not call Log rather
// than invent a placeholder ID, which would make the log actively
// misleading instead of merely incomplete.
type Event struct {
	SalonID    *uuid.UUID
	ActorID    *uuid.UUID
	ActorRole  string
	EntityType string
	EntityID   uuid.UUID
	Action     string
	OldValues  any
	NewValues  any
	IPAddress  string
}

type Repository interface {
	Log(ctx context.Context, e Event) error
}

type pgRepo struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) Log(ctx context.Context, e Event) error {
	var oldJSON, newJSON []byte
	var err error
	if e.OldValues != nil {
		if oldJSON, err = json.Marshal(e.OldValues); err != nil {
			return fmt.Errorf("audit log: marshal old_values: %w", err)
		}
	}
	if e.NewValues != nil {
		if newJSON, err = json.Marshal(e.NewValues); err != nil {
			return fmt.Errorf("audit log: marshal new_values: %w", err)
		}
	}

	// NULLIF turns an empty IP string into a real SQL NULL rather than
	// failing the INET cast, which is what happens for any event where the
	// caller genuinely has no IP to attach (a background job, for instance).
	_, err = r.db.Exec(ctx, `
		INSERT INTO audit_events (
			salon_id, actor_id, actor_role, entity_type, entity_id,
			action, old_values, new_values, ip_address
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::inet)`,
		e.SalonID, e.ActorID, e.ActorRole, e.EntityType, e.EntityID,
		e.Action, oldJSON, newJSON, e.IPAddress,
	)
	if err != nil {
		return fmt.Errorf("audit log: insert: %w", err)
	}
	return nil
}

// NopRepository discards every event. Used as the default when a domain's
// tests construct a Service without caring about audit logging - matching
// the same variadic-optional pattern used elsewhere in this codebase for
// loggers, so adding audit logging to an existing service never breaks its
// existing test suite.
type NopRepository struct{}

func (NopRepository) Log(context.Context, Event) error { return nil }
