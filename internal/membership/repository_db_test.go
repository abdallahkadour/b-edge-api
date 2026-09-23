//go:build dbtest

package membership

// Repository tests against a real PostgreSQL database.
//
// The subject is the abuse and billing guards on salon invitations. Every one
// of them is a COUNTING QUERY whose correctness lives entirely in its SQL -
// which statuses count as live, whether expiry is applied, whether the join
// reaches the right subscription. A mock returns whatever number it was
// constructed with and agrees with any of them.
//
// ArtistCeiling matters most. FRAUD-14 was `included_seats` being declared a
// ceiling in the plan table and enforced NOWHERE - a salon on Solo (1 seat)
// could invite unlimited artists. It is enforced now, on invite, using this
// query, so this query is the whole of that fix.
//
// Its FAIL-OPEN branch is deliberate and is tested here as a property rather
// than left as a comment: no live subscription returns 0, meaning unlimited.
// The reasoning is in the repository - "an under-charged salon is a far better
// failure than a blocked one" - and a future reader tempted to "fix" that into
// a refusal should have to delete a test that says why.
//
// Run with: make test-db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

type memFixture struct {
	SalonID uuid.UUID
	OwnerID uuid.UUID
	Artist  uuid.UUID
}

func newMemFixture(t *testing.T, pool *pgxpool.Pool) memFixture {
	t.Helper()
	ctx := context.Background()
	var f memFixture

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('Owner','owner@memtest.local','x','artist') RETURNING id`).Scan(&f.OwnerID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO salons (owner_id, name) VALUES ($1,'Mem Test Salon') RETURNING id`,
		f.OwnerID).Scan(&f.SalonID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`,
		f.OwnerID, f.SalonID).Scan(&f.Artist))
	return f
}

func (f memFixture) invitation(phone string, status InvitationStatus, expires time.Time) *Invitation {
	p := phone
	return &Invitation{
		ID:        uuid.New(),
		SalonID:   f.SalonID,
		InvitedBy: f.OwnerID,
		Phone:     &p,
		TokenHash: uuid.NewString(),
		Status:    status,
		ExpiresAt: expires,
	}
}

// ── the volume caps ────────────────────────────────────────────────────────

func TestCountLiveInvitations_ExcludesExpiredAndSettled(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newMemFixture(t, pool)
	ctx := context.Background()

	future := time.Now().UTC().Add(48 * time.Hour)
	past := time.Now().UTC().Add(-1 * time.Hour)

	// Two genuinely live.
	require.NoError(t, repo.CreateInvitation(ctx, f.invitation("+96170000001", StatusPending, future)))
	require.NoError(t, repo.CreateInvitation(ctx, f.invitation("+96170000002", StatusPending, future)))

	// One pending but PAST its deadline. Lazy expiry means nothing has
	// rewritten this row, so a naive `WHERE status = 'pending'` counts it -
	// and the salon loses a seat against the cap for an invitation nobody can
	// accept any more.
	require.NoError(t, repo.CreateInvitation(ctx, f.invitation("+96170000003", StatusPending, past)))

	// One already accepted - settled, not live.
	//
	// Set with a direct UPDATE, not through the struct: CreateInvitation does
	// not insert `status` at all, relying on the column default. That is
	// correct design - a caller cannot forge a pre-accepted invitation and
	// walk into a salon - but it means the Status field on the struct you
	// hand it is silently ignored. Asserted separately below.
	// accepted_by must be set in the same statement: the schema carries
	//
	//     CHECK ((status = 'accepted') = (accepted_by IS NOT NULL))
	//
	// which makes "accepted by nobody" UNREPRESENTABLE rather than merely
	// discouraged. Setting the status alone is rejected by the database - as
	// this test's first version was.
	accepted := f.invitation("+96170000004", StatusPending, future)
	require.NoError(t, repo.CreateInvitation(ctx, accepted))
	_, err := pool.Exec(ctx,
		`UPDATE salon_invitations SET status='accepted', accepted_by=$2, accepted_at=NOW()
		  WHERE id=$1`, accepted.ID, f.OwnerID)
	require.NoError(t, err)

	n, err := repo.CountLiveInvitations(ctx, f.SalonID)
	require.NoError(t, err)

	assert.Equal(t, 2, n,
		"only unexpired pending invitations are live; an expired one must not "+
			"consume a seat against MaxLiveInvitations")
}

func TestCountLiveInvitations_IsScopedToOneSalon(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newMemFixture(t, pool)
	ctx := context.Background()

	future := time.Now().UTC().Add(48 * time.Hour)
	require.NoError(t, repo.CreateInvitation(ctx, f.invitation("+96170000005", StatusPending, future)))

	// A second salon with its own invitations. If the count leaked across
	// salons, one busy salon would exhaust every other salon's cap.
	var otherOwner, otherSalon uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('Other','other@memtest.local','x','artist') RETURNING id`).Scan(&otherOwner))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO salons (owner_id, name) VALUES ($1,'Other Salon') RETURNING id`,
		otherOwner).Scan(&otherSalon))

	other := f.invitation("+96170000006", StatusPending, future)
	other.SalonID = otherSalon
	other.InvitedBy = otherOwner
	require.NoError(t, repo.CreateInvitation(ctx, other))

	n, err := repo.CountLiveInvitations(ctx, f.SalonID)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "another salon's invitations must not count against this one")
}

func TestCountInvitationsSince_BoundsTheDailyRate(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newMemFixture(t, pool)
	ctx := context.Background()

	future := time.Now().UTC().Add(48 * time.Hour)
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.CreateInvitation(ctx,
			f.invitation("+9617000010"+string(rune('0'+i)), StatusPending, future)))
	}

	// The daily cap counts CREATION, not liveness - revoking an invitation
	// must not buy another send, or the rate limit is trivially defeated by
	// invite-revoke-invite.
	recent, err := repo.CountInvitationsSince(ctx, f.SalonID, time.Now().UTC().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 3, recent)

	// A window that starts after they were made sees none.
	future_window, err := repo.CountInvitationsSince(ctx, f.SalonID, time.Now().UTC().Add(1*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 0, future_window, "the window boundary must actually be applied")
}

// ── the seat ceiling · FRAUD-14 ────────────────────────────────────────────

func TestArtistCeiling_ReadsTheSubscribedPlansSeats(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newMemFixture(t, pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO subscriptions (artist_id, plan_code, monthly_price)
		 VALUES ($1,'studio',109.00)`, f.Artist)
	require.NoError(t, err)

	var expected int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT included_seats FROM plans WHERE code='studio'`).Scan(&expected))
	require.Greater(t, expected, 0, "precondition: the studio plan must define seats")

	ceiling, code, err := repo.ArtistCeiling(ctx, f.SalonID)
	require.NoError(t, err)

	assert.Equal(t, expected, ceiling, "the ceiling must come from the subscribed plan")
	assert.Equal(t, "studio", code)
}

func TestArtistCeiling_NoSubscription_FailsOpen(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newMemFixture(t, pool)

	// No subscription row at all. This returns 0, which the caller reads as
	// UNLIMITED - a deliberate fail-open. The repository's own comment gives
	// the reason: "an under-charged salon is a far better failure than a
	// blocked one." A billing lookup failure must never stop a salon hiring.
	//
	// This is a test of a DECISION, not just of code. Anyone changing it to
	// refuse has to delete this and say why.
	ceiling, code, err := repo.ArtistCeiling(context.Background(), f.SalonID)

	require.NoError(t, err, "a missing subscription is not an error here")
	assert.Equal(t, 0, ceiling, "0 means unlimited - fail open, deliberately")
	assert.Equal(t, "", code)
}

func TestArtistCeiling_CancelledSubscription_FailsOpen(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newMemFixture(t, pool)
	ctx := context.Background()

	// The query joins `sub.cancelled_at IS NULL`. A cancelled subscription
	// must not keep enforcing its old seat limit on a salon that no longer
	// has it.
	_, err := pool.Exec(ctx,
		`INSERT INTO subscriptions (artist_id, plan_code, monthly_price, cancelled_at)
		 VALUES ($1,'solo',45.00, NOW())`, f.Artist)
	require.NoError(t, err)

	ceiling, _, err := repo.ArtistCeiling(ctx, f.SalonID)
	require.NoError(t, err)
	assert.Equal(t, 0, ceiling, "a cancelled subscription imposes no ceiling")
}

// ── status is not caller-controlled ────────────────────────────────────────

func TestCreateInvitation_IgnoresCallerSuppliedStatus(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newMemFixture(t, pool)
	ctx := context.Background()

	// CreateInvitation's INSERT does not list `status`, so the column default
	// wins and every new invitation is 'pending' regardless of what the caller
	// put on the struct.
	//
	// That is the right behaviour and worth pinning: if `status` were ever
	// added to that INSERT, a caller who could reach this repository could
	// mint an invitation that is ALREADY ACCEPTED - joining a salon without
	// the owner ever sending anything.
	inv := f.invitation("+96170000099", StatusAccepted, time.Now().UTC().Add(48*time.Hour))
	require.NoError(t, repo.CreateInvitation(ctx, inv))

	var stored string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM salon_invitations WHERE id=$1`, inv.ID).Scan(&stored))

	assert.Equal(t, string(StatusPending), stored,
		"a caller must not be able to create an invitation that is already accepted")
}

func TestSalonInvitations_AcceptedWithoutAcceptor_IsUnrepresentable(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newMemFixture(t, pool)
	ctx := context.Background()

	inv := f.invitation("+96170000077", StatusPending, time.Now().UTC().Add(48*time.Hour))
	require.NoError(t, repo.CreateInvitation(ctx, inv))

	// The schema pairs the two facts so they cannot disagree:
	//
	//     CHECK ((status = 'accepted') = (accepted_by IS NOT NULL))
	//
	// An accepted invitation with no acceptor would be a salon membership
	// nobody can be held to - there would be no record of WHO joined. The
	// constraint is bidirectional, so the reverse is also blocked: an
	// acceptor recorded on an invitation that is not accepted.
	_, err := pool.Exec(ctx,
		`UPDATE salon_invitations SET status='accepted' WHERE id=$1`, inv.ID)
	assert.Error(t, err, "accepted without an acceptor must be refused by the database")

	_, err = pool.Exec(ctx,
		`UPDATE salon_invitations SET accepted_by=$2 WHERE id=$1`, inv.ID, f.OwnerID)
	assert.Error(t, err, "an acceptor on a still-pending invitation must be refused too")

	// Together they are fine - proving the constraint is a pairing rule and
	// not a blanket refusal.
	_, err = pool.Exec(ctx,
		`UPDATE salon_invitations SET status='accepted', accepted_by=$2, accepted_at=NOW()
		  WHERE id=$1`, inv.ID, f.OwnerID)
	assert.NoError(t, err, "set together, the transition must succeed")
}
