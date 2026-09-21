package membership

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This file holds the two small outbound adapters the membership service
// needs. They are concrete here rather than imported from their home domains
// because both are one statement, and importing internal/notification or
// internal/domain/auth for a single INSERT and a single UPDATE would drag
// their whole dependency graph - including internal/middleware, which
// notification's handler imports - into a package middleware already
// depends on through its route registration.

// ── Notifier ──────────────────────────────────────────────────────────────

type pgNotifier struct{ db *pgxpool.Pool }

func newNotifier(db *pgxpool.Pool) Notifier { return &pgNotifier{db: db} }

// QueueSalonInvitation writes the message the worker will try to deliver.
//
// It will not be delivered today. TWILIO_WHATSAPP_FROM is unset and every
// notification this platform has ever queued is 'dead'. The row is written
// anyway so that the moment Meta verification clears, invitations already
// sent start arriving - and so the delivery reconciler has something real to
// reconcile. The copyable link returned to the owner is the channel that
// actually works in the meantime.
//
// recipient_phone is set explicitly rather than resolved from a user_id,
// because the invitee frequently has no account yet - that is the whole
// point of an invitation.
func (n *pgNotifier) QueueSalonInvitation(ctx context.Context, toPhone, salonName, link string) error {
	_, err := n.db.Exec(ctx, `
		INSERT INTO notifications (template_name, channel, payload, recipient_phone)
		VALUES ('salon_invitation', 'whatsapp',
		        jsonb_build_object('message', $1), $2)`,
		fmt.Sprintf("You have been invited to join %s on B-Edge. "+
			"Open this link to accept: %s", salonName, link),
		toPhone,
	)
	if err != nil {
		return fmt.Errorf("queue salon invitation: %w", err)
	}
	return nil
}

// ── TokenInvalidator ──────────────────────────────────────────────────────

type pgTokenInvalidator struct{ db *pgxpool.Pool }

func newTokenInvalidator(db *pgxpool.Pool) TokenInvalidator {
	return &pgTokenInvalidator{db: db}
}

// RevokeAllForUser stamps revoked_at on every live refresh token a user
// holds, forcing them to re-authenticate and pick up a correctly-scoped
// access token.
//
// This is what makes ownership transfer take effect. salonrole.Resolve runs
// at token issue, so the role is baked into the access token: without this,
// the former owner keeps owner capabilities until their access token expires
// and their refresh token would keep minting new ones with the stale role.
// Risk R2 in the HLD, and the price of not querying the database for a role
// on every request.
//
// Same UPDATE shape as internal/domain/auth's RevokeRefreshToken, widened
// from one token hash to every token a user holds.
func (t *pgTokenInvalidator) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := t.db.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = NOW()
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("revoke refresh tokens for user: %w", err)
	}
	return nil
}
