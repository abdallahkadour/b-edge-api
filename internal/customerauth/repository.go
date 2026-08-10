package customerauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolationCode is the PostgreSQL error code for unique constraint violations.
const uniqueViolationCode = "23505"

// Repository defines all database operations for customer OTP auth.
type Repository interface {
	// CountRecentOTPs returns how many OTPs have been created for this phone
	// since `since` - powers the 3-per-5-minutes rate limit.
	CountRecentOTPs(ctx context.Context, phone string, since time.Time) (int, error)

	// CreateOTP inserts a new code.
	CreateOTP(ctx context.Context, phone, otpHash string, expiresAt time.Time) (uuid.UUID, error)

	// GetLatestOTP returns the most recently created OTP for a phone,
	// regardless of its state - the service layer decides whether it's
	// still usable (not expired, not already verified, attempts remaining).
	// Returns ErrOTPNotFound if the phone has never requested one.
	GetLatestOTP(ctx context.Context, phone string) (*CustomerOTP, error)

	// IncrementAttempts records one more failed guess against an OTP.
	IncrementAttempts(ctx context.Context, id uuid.UUID) error

	// MarkVerified stamps an OTP as successfully used - makes it permanently
	// unusable for a second verify, even within its expiry window.
	MarkVerified(ctx context.Context, id uuid.UUID) error

	// FindOrCreateCustomerByPhone resolves a customer's identity by phone
	// the same "one phone number, one account" rule migration 014 and
	// CreateGuestUser (booking domain) already established. Reuses an
	// existing user (keeping their real name if a prior guest booking set
	// one) or creates a fresh row with a generic placeholder name.
	FindOrCreateCustomerByPhone(ctx context.Context, phone string) (*CustomerInfo, error)

	// StoreRefreshToken persists a hash of a newly issued refresh token
	// mirrors internal/domain/auth's refresh_tokens usage exactly, same
	// table, same rotation/revocation model.
	StoreRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error

	// EnqueueOTPNotification queues the WhatsApp delivery. Always succeeds
	// regardless of whether Twilio is actually configured - same
	// best-effort, always-safe-to-call pattern already used for booking
	// notifications (see internal/booking's EnqueueNotification).
	// EnqueueOTPNotification queues against a bare PHONE, not a user id
	// the customer's users row deliberately doesn't exist yet at this point
	// (see PhoneEligibleForOTP and migration 018).
	EnqueueOTPNotification(ctx context.Context, phone, message string) error

	// PhoneEligibleForOTP reports whether a phone number may be used for
	// customer OTP login. False when the number is already held by a
	// non-customer account (an artist) - artists authenticate by
	// email+password only, and letting the OTP path resolve their account
	// would be a second, password-less way in.
	//
	// Deliberately read-only: it must NOT create anything. An
	// unauthenticated caller can submit any phone number, and creating a
	// users row on that basis let strangers pre-register numbers that never
	// signed up.
	PhoneEligibleForOTP(ctx context.Context, phone string) (bool, error)

	// GetRefreshTokenHash returns the stored token record for a hash, for
	// the refresh flow to validate against. Returns ErrRefreshTokenNotFound
	// if no such token exists.
	GetRefreshTokenHash(ctx context.Context, tokenHash string) (*StoredRefreshToken, error)

	// RevokeRefreshToken marks a refresh token as used - refresh tokens are
	// one-time use (rotation), exactly like the artist auth domain.
	RevokeRefreshToken(ctx context.Context, tokenHash string) error

	// GetCustomerByID fetches a customer's safe info by their user ID
	// used when restoring a session from a refresh token, which only
	// carries the ID, not the phone.
	GetCustomerByID(ctx context.Context, id uuid.UUID) (*CustomerInfo, error)
}

type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository constructs the Postgres-backed Repository implementation.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) CountRecentOTPs(ctx context.Context, phone string, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM customer_otps WHERE phone = $1 AND created_at > $2`,
		phone, since,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count recent otps: %w", err)
	}
	return count, nil
}

func (r *pgRepo) CreateOTP(ctx context.Context, phone, otpHash string, expiresAt time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		INSERT INTO customer_otps (phone, otp_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id`,
		phone, otpHash, expiresAt,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create otp: %w", err)
	}
	return id, nil
}

func (r *pgRepo) GetLatestOTP(ctx context.Context, phone string) (*CustomerOTP, error) {
	o := &CustomerOTP{}
	err := r.db.QueryRow(ctx, `
		SELECT id, phone, otp_hash, expires_at, attempts, verified_at, created_at
		FROM customer_otps
		WHERE phone = $1
		ORDER BY created_at DESC
		LIMIT 1`,
		phone,
	).Scan(&o.ID, &o.Phone, &o.OTPHash, &o.ExpiresAt, &o.Attempts, &o.VerifiedAt, &o.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOTPNotFound
		}
		return nil, fmt.Errorf("get latest otp: %w", err)
	}
	return o, nil
}

func (r *pgRepo) IncrementAttempts(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE customer_otps SET attempts = attempts + 1 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("increment otp attempts: %w", err)
	}
	return nil
}

func (r *pgRepo) MarkVerified(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE customer_otps SET verified_at = NOW() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("mark otp verified: %w", err)
	}
	return nil
}

// FindOrCreateCustomerByPhone mirrors the exact lookup-then-insert-with-
// race-handling shape used by booking's CreateGuestUser (see migration 014's
// reasoning) - duplicated rather than shared across domains for now, since
// this codebase doesn't have a cross-domain shared-repo pattern yet. Worth
// consolidating into one place later if a third caller ever needs it.
func (r *pgRepo) FindOrCreateCustomerByPhone(ctx context.Context, phone string) (*CustomerInfo, error) {
	existing, err := r.lookupByPhone(ctx, phone)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find or create customer: lookup: %w", err)
	}

	id := uuid.New()
	// Same synthetic-email convention as booking's CreateGuestUser - email
	// is never used for customer login, only phone is.
	email := fmt.Sprintf("customer_%s@bedge.guest", id.String())
	name := "Customer" // placeholder; real name comes from a prior guest
	// booking if one exists (handled by the lookup above), or is collected
	// later via a profile-completion step - out of scope for this domain.

	_, err = r.db.Exec(ctx, `
		INSERT INTO users (id, name, email, password_hash, role, phone, status)
		VALUES ($1, $2, $3, 'CUSTOMER_OTP_NO_PASSWORD', 'customer', $4, 'active')`,
		id, name, email, phone,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			// Same race as CreateGuestUser: another request for this exact
			// new phone won the insert first. Fall back to it.
			winner, lookupErr := r.lookupByPhone(ctx, phone)
			if lookupErr == nil {
				return winner, nil
			}
			// Unique violation on phone, yet no CUSTOMER owns it - so the
			// number belongs to an artist account. Surface a sentinel the
			// caller can swallow silently rather than a raw 500.
			return nil, ErrPhoneNotEligible
		}
		return nil, fmt.Errorf("find or create customer: insert: %w", err)
	}

	return &CustomerInfo{ID: id, Name: name, Phone: phone}, nil
}

func (r *pgRepo) lookupByPhone(ctx context.Context, phone string) (*CustomerInfo, error) {
	info := &CustomerInfo{}
	// role = 'customer' is load-bearing, not cosmetic. Without it, this
	// lookup happily returns an ARTIST's user row when someone requests an
	// OTP for that artist's phone number - minting a session bound to the
	// artist's user_id and creating a second, password-less way into an
	// account whose owner believes it's protected by the email/password
	// they chose. Artists authenticate through internal/domain/auth only.
	err := r.db.QueryRow(ctx,
		`SELECT id, name, phone FROM users
		 WHERE phone = $1 AND role = 'customer' AND deleted_at IS NULL`,
		phone,
	).Scan(&info.ID, &info.Name, &info.Phone)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (r *pgRepo) StoreRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}
	return nil
}

func (r *pgRepo) EnqueueOTPNotification(ctx context.Context, phone, message string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO notifications (user_id, recipient_phone, template_name, channel, payload)
		VALUES (NULL, $1, 'customer_otp_login', 'whatsapp', jsonb_build_object('message', $2::text))`,
		phone, message,
	)
	if err != nil {
		return fmt.Errorf("enqueue otp notification: %w", err)
	}
	return nil
}

func (r *pgRepo) PhoneEligibleForOTP(ctx context.Context, phone string) (bool, error) {
	var role string
	err := r.db.QueryRow(ctx,
		`SELECT role FROM users WHERE phone = $1 AND deleted_at IS NULL`,
		phone,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nobody holds this number yet - eligible. The users row is
			// created later, on successful verification.
			return true, nil
		}
		return false, fmt.Errorf("phone eligible for otp: %w", err)
	}
	return role == "customer", nil
}

func (r *pgRepo) GetRefreshTokenHash(ctx context.Context, tokenHash string) (*StoredRefreshToken, error) {
	t := &StoredRefreshToken{}
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, revoked_at FROM refresh_tokens WHERE token_hash = $1`,
		tokenHash,
	).Scan(&t.ID, &t.UserID, &t.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, fmt.Errorf("get refresh token: %w", err)
	}
	return t, nil
}

func (r *pgRepo) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1`,
		tokenHash,
	)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

func (r *pgRepo) GetCustomerByID(ctx context.Context, id uuid.UUID) (*CustomerInfo, error) {
	info := &CustomerInfo{}
	err := r.db.QueryRow(ctx,
		`SELECT id, name, phone FROM users WHERE id = $1 AND deleted_at IS NULL`,
		id,
	).Scan(&info.ID, &info.Name, &info.Phone)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer by id: %w", err)
	}
	return info, nil
}
