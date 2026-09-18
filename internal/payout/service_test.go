package payout

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRepo struct {
	listed      []*PaymentMethod
	listErr     error
	lastActive  bool
	upserted    UpsertPaymentMethodRequest
	upsertErr   error
	setActiveEr error
}

func (m *mockRepo) ListBySalon(_ context.Context, _ uuid.UUID, activeOnly bool) ([]*PaymentMethod, error) {
	m.lastActive = activeOnly
	return m.listed, m.listErr
}

func (m *mockRepo) Upsert(_ context.Context, salonID uuid.UUID, req UpsertPaymentMethodRequest) (*PaymentMethod, error) {
	m.upserted = req
	if m.upsertErr != nil {
		return nil, m.upsertErr
	}
	return &PaymentMethod{ID: uuid.New(), SalonID: salonID, Method: Method(req.Method),
		AccountName: req.AccountName, AccountRef: req.AccountRef, IsActive: true}, nil
}

func (m *mockRepo) SetActive(_ context.Context, id, salonID uuid.UUID, active bool) (*PaymentMethod, error) {
	if m.setActiveEr != nil {
		return nil, m.setActiveEr
	}
	return &PaymentMethod{ID: id, SalonID: salonID, Method: MethodWhish,
		AccountName: "Rania", AccountRef: "71900001", IsActive: active}, nil
}

func newTestService(r Repository) *Service { return NewService(r) }

// ── The security-relevant behaviour ───────────────────────────────────────────

// A client must never be offered a retired destination. An artist who has lost
// control of an account needs switching it off to take effect on the deposit
// screen immediately - that is the whole reason retiring exists rather than
// just editing the number.
func TestListPublic_RequestsActiveOnly(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.ListPublic(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.True(t, repo.lastActive, "the public read must filter to active destinations")
}

// The artist's own view deliberately includes retired ones, or they could
// never turn one back on.
func TestListForSalon_IncludesRetired(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.ListForSalon(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.False(t, repo.lastActive)
}

// The public shape must not grow fields by accident. A client is about to send
// money against this, so what it contains is a deliberate decision.
func TestListPublic_ExposesOnlyThePayingFields(t *testing.T) {
	repo := &mockRepo{listed: []*PaymentMethod{{
		ID: uuid.New(), SalonID: uuid.New(), Method: MethodWhish,
		AccountName: "Rania Khoury", AccountRef: "71900001", IsActive: true,
	}}}
	svc := newTestService(repo)

	out, err := svc.ListPublic(context.Background(), uuid.New())

	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "whish", out[0].Method)
	assert.Equal(t, "Whish", out[0].MethodLabel)
	assert.Equal(t, "Rania Khoury", out[0].AccountName)
	assert.Equal(t, "71900001", out[0].AccountRef)
}

// ── Normalisation ─────────────────────────────────────────────────────────────

// People paste "71 900 001". A client comparing what the app shows against
// what their banking app shows should not have to reason about spacing.
func TestUpsert_StripsWhitespaceFromTheReference(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	out, err := svc.Upsert(context.Background(), uuid.New(), UpsertPaymentMethodRequest{
		Method: "whish", AccountName: "  Rania Khoury  ", AccountRef: " 71 900 001 ",
	})

	require.NoError(t, err)
	assert.Equal(t, "71900001", out.AccountRef)
	assert.Equal(t, "Rania Khoury", out.AccountName)
}

// ── Validation ────────────────────────────────────────────────────────────────

func TestUpsert_RejectsUnknownMethod(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, err := svc.Upsert(context.Background(), uuid.New(), UpsertPaymentMethodRequest{
		Method: "bitcoin", AccountName: "Rania", AccountRef: "71900001",
	})

	assert.Error(t, err)
}

func TestUpsert_RejectsShortReference(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, err := svc.Upsert(context.Background(), uuid.New(), UpsertPaymentMethodRequest{
		Method: "whish", AccountName: "Rania", AccountRef: "12",
	})

	assert.Error(t, err)
}

// The account name is what lets a client notice they are paying the wrong
// person - both OMT and Whish show a recipient name at confirmation - so an
// empty one defeats the point of holding the destination at all.
func TestUpsert_RejectsEmptyAccountName(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, err := svc.Upsert(context.Background(), uuid.New(), UpsertPaymentMethodRequest{
		Method: "whish", AccountName: "   ", AccountRef: "71900001",
	})

	assert.Error(t, err)
}

func TestUpsert_AcceptsOMT(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	out, err := svc.Upsert(context.Background(), uuid.New(), UpsertPaymentMethodRequest{
		Method: "omt", AccountName: "Rania Khoury", AccountRef: "OMT-84213",
	})

	require.NoError(t, err)
	assert.Equal(t, "omt", out.Method)
	assert.Equal(t, "OMT", out.MethodLabel)
}

// ── Ownership ─────────────────────────────────────────────────────────────────

// A destination belonging to another salon must be reported exactly as a
// nonexistent one. Anything else lets a caller confirm an id is real from the
// status code alone.
func TestSetActive_ForeignOrMissingIsA404(t *testing.T) {
	svc := newTestService(&mockRepo{setActiveEr: pgx.ErrNoRows})

	_, err := svc.SetActive(context.Background(), uuid.New(), uuid.New(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSetActive_RetiresTheDestination(t *testing.T) {
	svc := newTestService(&mockRepo{})

	out, err := svc.SetActive(context.Background(), uuid.New(), uuid.New(), false)

	require.NoError(t, err)
	assert.False(t, out.IsActive)
}

func TestMethod_Valid(t *testing.T) {
	assert.True(t, MethodWhish.Valid())
	assert.True(t, MethodOMT.Valid())
	assert.False(t, Method("paypal").Valid())
	assert.False(t, Method("").Valid())
}
