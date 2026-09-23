package artist

// Storage for phone verification.
//
// Small and deliberately separate from repository.go: these three queries
// belong to one flow, and a reader looking for "how does an artist verify
// their number" should find them together rather than scattered through a
// 900-line file.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// PhoneRepo implements PhoneVerifyPort.
type PhoneRepo struct {
	db *pgxpool.Pool
}

// NewPhoneRepo returns a PhoneRepo over the given pool.
func NewPhoneRepo(db *pgxpool.Pool) *PhoneRepo { return &PhoneRepo{db: db} }

// PhoneForUser returns the user's stored number and whether it is verified.
//
// Reads the number from the ACCOUNT. The service never accepts one from a
// request body - that would let an artist verify somebody else's phone onto
// their own account, which is precisely what this flow exists to prevent.
func (r *PhoneRepo) PhoneForUser(ctx context.Context, userID uuid.UUID) (string, *time.Time, error) {
	var phone *string
	var verifiedAt *time.Time
	err := r.db.QueryRow(ctx,
		`SELECT phone, phone_verified_at FROM users WHERE id = $1 AND deleted_at IS NULL`,
		userID).Scan(&phone, &verifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, apperror.NotFound("USER_NOT_FOUND", "Account not found")
	}
	if err != nil {
		return "", nil, fmt.Errorf("phone for user: %w", err)
	}
	if phone == nil {
		return "", verifiedAt, nil
	}
	return *phone, verifiedAt, nil
}

// MarkPhoneVerified stamps users.phone_verified_at.
//
// Guarded on phone_verified_at IS NULL so a replay cannot move an existing
// timestamp forward - the date a number was proven is a fact, not a
// last-touched marker, and membership.Invite reads it as one.
func (r *PhoneRepo) MarkPhoneVerified(ctx context.Context, userID uuid.UUID) error {
	if _, err := r.db.Exec(ctx,
		`UPDATE users
		    SET phone_verified_at = NOW(), updated_at = NOW()
		  WHERE id = $1 AND deleted_at IS NULL AND phone_verified_at IS NULL`,
		userID); err != nil {
		return fmt.Errorf("mark phone verified: %w", err)
	}
	return nil
}

// EnqueueOTP queues a code for delivery.
//
// QUEUED, not sent. internal/notification's worker owns transport and
// retries, and sendVia tries WhatsApp first and falls back to SMS the moment
// TWILIO_SMS_FROM is set - so this works over SMS later with no change here.
//
// $1::text, not $1: Postgres cannot infer a parameter's type inside
// jsonb_build_object and answers 42P18 without the cast.
func (r *PhoneRepo) EnqueueOTP(ctx context.Context, phone, message string) error {
	if _, err := r.db.Exec(ctx, `
		INSERT INTO notifications (template_name, channel, payload, recipient_phone, status)
		VALUES ('artist_phone_otp', 'whatsapp',
		        jsonb_build_object('message', $1::text), $2, 'pending')`,
		message, phone); err != nil {
		return fmt.Errorf("enqueue otp notification: %w", err)
	}
	return nil
}
