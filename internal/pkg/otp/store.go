package otp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means no code has been issued for that phone.
var ErrNotFound = errors.New("no otp found for this phone")

// Store is the shared persistence for one-time codes.
//
// It lives in the leaf package alongside the rule because the rule and the
// rows it judges belong together: MaxAttempts is meaningless without the
// column that counts them, and Validity is meaningless without expires_at.
// Splitting them is how the two end up disagreeing.
//
// ── On the table name ────────────────────────────────────────────────────
//
// The table is `customer_otps`, which is now a misnomer: artists verify their
// phone number through the same rows. Deliberately NOT renamed. A rename is a
// migration, a re-point of every existing query, and a window where a deploy
// half-applied breaks customer login - all to fix a word. The name is wrong;
// the comment says so; that is cheaper than being right.
//
// ── Known remaining duplication, recorded rather than hidden ─────────────
//
// internal/customerauth's Repository still has its own equivalents of these
// five queries, written before this package existed. They are the same SQL
// against the same table. Consolidating means changing that domain's
// Repository interface and its mock, which is mechanical but not free, and
// not something to do in the same change that introduces a new flow.
//
// What matters is that the RULE is no longer duplicated - Check, the
// ceilings and the ordering are here, once. Two copies of a SELECT that
// agree are a tidiness problem; two copies of a security check that drift are
// the defect this codebase keeps producing.
type Store struct {
	db *pgxpool.Pool
}

// NewStore returns a Store over the given pool.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// CountRecent reports how many codes were issued to a phone since `since`.
// Used for rate limiting, against RateLimitMax over RateLimitWindow.
func (s *Store) CountRecent(ctx context.Context, phone string, since time.Time) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM customer_otps WHERE phone = $1 AND created_at >= $2`,
		phone, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("count recent otps: %w", err)
	}
	return n, nil
}

// Create stores a hashed code. The plaintext never reaches the database, so
// a dump of this table cannot be replayed into anybody's account.
func (s *Store) Create(ctx context.Context, phone, hash string, expiresAt time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	if err := s.db.QueryRow(ctx,
		`INSERT INTO customer_otps (phone, otp_hash, expires_at)
		 VALUES ($1, $2, $3) RETURNING id`,
		phone, hash, expiresAt).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("create otp: %w", err)
	}
	return id, nil
}

// Latest returns the most recent code issued to a phone, with the id needed
// to record an attempt or a verification against it.
//
// Most recent, not "most recent unverified": a code that has already been
// used must be reported as AlreadyUsed rather than silently skipped in favour
// of an older one, which would let a stale code be replayed.
func (s *Store) Latest(ctx context.Context, phone string) (uuid.UUID, Record, error) {
	var id uuid.UUID
	var rec Record
	err := s.db.QueryRow(ctx, `
		SELECT id, otp_hash, expires_at, verified_at, attempts
		  FROM customer_otps
		 WHERE phone = $1
		 ORDER BY created_at DESC
		 LIMIT 1`, phone,
	).Scan(&id, &rec.Hash, &rec.ExpiresAt, &rec.VerifiedAt, &rec.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, Record{}, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, Record{}, fmt.Errorf("latest otp: %w", err)
	}
	return id, rec, nil
}

// IncrementAttempts records one wrong guess.
//
// Call this ONLY on a WrongCode verdict. Incrementing on Expired or
// AlreadyUsed would let anyone burn down a victim's remaining attempts
// against a code that is already dead.
func (s *Store) IncrementAttempts(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE customer_otps SET attempts = attempts + 1 WHERE id = $1`, id); err != nil {
		return fmt.Errorf("increment otp attempts: %w", err)
	}
	return nil
}

// MarkVerified stamps a code as used, so it cannot be replayed.
func (s *Store) MarkVerified(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE customer_otps SET verified_at = NOW() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("mark otp verified: %w", err)
	}
	return nil
}
