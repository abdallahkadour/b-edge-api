package offering

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/optional"
)

type mockRepo struct {
	artistID    uuid.UUID
	artistIDErr error // ruling 3: a non-ErrNotFound failure from ArtistIDForUser must surface as a 500
	inSalon     bool
	current     *Offering
	getErr      error
	upserts     []UpsertParams
	upsertErr   error
	deletes     int
}

func (m *mockRepo) ArtistIDForUser(context.Context, uuid.UUID) (uuid.UUID, error) {
	return m.artistID, m.artistIDErr
}
func (m *mockRepo) ArtistInSalon(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return m.inSalon, nil
}
func (m *mockRepo) List(context.Context, uuid.UUID, uuid.UUID) ([]*Offering, error) {
	return []*Offering{m.current}, nil
}
func (m *mockRepo) Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*Offering, error) {
	return m.current, m.getErr
}
func (m *mockRepo) Upsert(_ context.Context, p UpsertParams) error {
	m.upserts = append(m.upserts, p)
	return m.upsertErr
}
func (m *mockRepo) Delete(context.Context, uuid.UUID, uuid.UUID) error { m.deletes++; return nil }

type captureAudit struct{ events []audit.Event }

func (c *captureAudit) Log(_ context.Context, e audit.Event) error {
	c.events = append(c.events, e)
	return nil
}

func ptr[T any](v T) *T { return &v }

func menuRow(offered bool) *Offering {
	return &Offering{ServiceID: uuid.New(), Offered: offered,
		SalonPrice: decimal.RequireFromString("150"), SalonDeposit: decimal.RequireFromString("30"),
		EffectivePrice: decimal.RequireFromString("150"), EffectiveDeposit: decimal.RequireFromString("30")}
}

func code(t *testing.T, err error) string {
	t.Helper()
	var e *apperror.AppError
	require.ErrorAs(t, err, &e)
	return e.Code
}

func TestUpdateMine_SetsHerPrice_AndAuditsTheActor(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}
	aud := &captureAudit{}
	svc := NewService(repo, aud)
	user := uuid.New()

	_, err := svc.UpdateMine(context.Background(), uuid.New(), user, repo.current.ServiceID,
		UpdateRequest{Offered: ptr(true), Price: optional.From("200.00")}, "")

	require.NoError(t, err)
	require.Len(t, repo.upserts, 1)
	assert.True(t, repo.upserts[0].PriceSet)
	assert.True(t, repo.upserts[0].Price.Equal(decimal.RequireFromString("200")))
	require.Len(t, aud.events, 1)
	require.NotNil(t, aud.events[0].ActorID)
	assert.Equal(t, user, *aud.events[0].ActorID, "the audit must name who changed it")
}

func TestUpdateMine_NullPrice_ClearsToSalon(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: ptr(true), Price: optional.Null[string]()}, "")
	require.NoError(t, err)
	assert.True(t, repo.upserts[0].PriceSet)
	assert.Nil(t, repo.upserts[0].Price, "an explicit null must clear, not be ignored")
}

func TestUpdateMine_InvalidMoney_400(t *testing.T) {
	// internal/pkg/money answers 400 INVALID_<FIELD> - verified against
	// money.invalid(), not assumed. "10.999" would otherwise round silently
	// to 11.00 in the NUMERIC(10,2) column.
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: ptr(true), Price: optional.From("10.999")}, "")
	assert.Equal(t, "INVALID_PRICE", code(t, err))
	assert.Empty(t, repo.upserts)
}

func TestUpdateMine_OwnDepositAboveEffectivePrice_422(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: ptr(true),
			Price: optional.From("40.00"), DepositAmount: optional.From("50.00")}, "")
	assert.Equal(t, "VALIDATION_ERROR", code(t, err))
	assert.Empty(t, repo.upserts)
}

func TestUpdateMine_SwitchOff_Deletes(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: ptr(false)}, "")
	require.NoError(t, err)
	assert.Equal(t, 1, repo.deletes)
	assert.Empty(t, repo.upserts)
}

func TestUpdateMine_ServiceNotInHerSalon_404(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, getErr: ErrNotFound}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		uuid.New(), UpdateRequest{Offered: ptr(true)}, "")
	assert.Equal(t, "SERVICE_NOT_FOUND", code(t, err))
}

// TestUpdateMine_ArtistNoLongerInSalon_404 is ruling 9. salon_id and
// salon_role come off the access token (middleware.SalonIDFromContext reads
// c.Locals), and a token stays valid until it expires even after
// membership.Leave revokes refresh tokens. In that window, UpdateMine must
// not let her write artist_services rows for her FORMER salon just because
// repo.Get scoped the SERVICE to the token's salon - it must also confirm
// SHE is still in that salon, or a since-revoked artist could silently
// switch services back on if she later rejoins (breaking PP-7: "joining a
// salon: all switches off").
func TestUpdateMine_ArtistNoLongerInSalon_404(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: false, current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: ptr(true), Price: optional.From("200.00")}, "")
	assert.Equal(t, "MEMBER_NOT_FOUND", code(t, err))
	assert.Empty(t, repo.upserts)
}

func TestUpdateForMember_ArtistOfAnotherSalon_404(t *testing.T) {
	repo := &mockRepo{inSalon: false, current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateForMember(context.Background(), uuid.New(), uuid.New(),
		uuid.New(), repo.current.ServiceID, UpdateRequest{Offered: ptr(true)}, "")
	assert.Equal(t, "MEMBER_NOT_FOUND", code(t, err))
	assert.Empty(t, repo.upserts)
}

// TestUpdateMine_ArtistLookupFails_Is500NotMemberNotFound is ruling 3: a
// database outage from ArtistIDForUser must not read as "artist not found".
// Only errors.Is(err, ErrNotFound) may map to errMemberNotFound(); anything
// else must surface as a plain wrapped error, not an *apperror.AppError, so
// it reaches the client as a 500 rather than a misleading 404.
func TestUpdateMine_ArtistLookupFails_Is500NotMemberNotFound(t *testing.T) {
	repo := &mockRepo{artistIDErr: errors.New("connection reset by peer")}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		uuid.New(), UpdateRequest{Offered: ptr(true)}, "")

	require.Error(t, err)
	var appErr *apperror.AppError
	assert.False(t, errors.As(err, &appErr),
		"a generic ArtistIDForUser error must NOT become an AppError (would render as a 404, not a 500)")
}

// TestListMine_ArtistLookupFails_Is500NotMemberNotFound is the same ruling
// applied to the read path.
func TestListMine_ArtistLookupFails_Is500NotMemberNotFound(t *testing.T) {
	repo := &mockRepo{artistIDErr: errors.New("connection reset by peer")}
	_, err := NewService(repo, &captureAudit{}).ListMine(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
	var appErr *apperror.AppError
	assert.False(t, errors.As(err, &appErr),
		"a generic ArtistIDForUser error must NOT become an AppError (would render as a 404, not a 500)")
}

// auditJSON is what audit.pgRepo.Log persists for a value: its JSON. The
// assertions below read the audit row the way someone investigating it
// later would, by key, not the Go type that happened to produce it.
func auditJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	require.NotNil(t, v, "the audit row must carry values, not NULL")
	b, err := json.Marshal(v)
	require.NoError(t, err)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// TestUpdateForMember_AuditsOwnerAsActor_AndMembersArtistID is the positive
// path of PP-3's audit: the OWNER changes MAYA's price. The row must name
// the owner as the actor AND Maya as whose service changed - an audit that
// records only "service X changed" cannot say whose price it was, and every
// artist in the salon has a row for the same service.
func TestUpdateForMember_AuditsOwnerAsActor_AndMembersArtistID(t *testing.T) {
	repo := &mockRepo{inSalon: true, current: menuRow(true)}
	aud := &captureAudit{}
	salon, owner, maya := uuid.New(), uuid.New(), uuid.New()

	_, err := NewService(repo, aud).UpdateForMember(context.Background(), salon, owner, maya,
		repo.current.ServiceID, UpdateRequest{Offered: ptr(true), Price: optional.From("120.00")}, "203.0.113.7")

	require.NoError(t, err)
	require.Len(t, aud.events, 1)
	e := aud.events[0]
	require.NotNil(t, e.ActorID)
	assert.Equal(t, owner, *e.ActorID, "the actor is the owner who made the change")
	assert.Equal(t, "artist", e.ActorRole)
	assert.Equal(t, "203.0.113.7", e.IPAddress)
	require.NotNil(t, e.SalonID)
	assert.Equal(t, salon, *e.SalonID)
	assert.Equal(t, "offering.update", e.Action)
	for name, v := range map[string]any{"old": e.OldValues, "new": e.NewValues} {
		vals := auditJSON(t, v)
		assert.Equal(t, maya.String(), vals["artist_id"], "%s values must name whose service changed", name)
		assert.Equal(t, repo.current.ServiceID.String(), vals["service_id"], "%s values", name)
	}
	assert.Equal(t, "120", auditJSON(t, e.NewValues)["price"], "the new values carry the price that was set")
}

// TestUpdateMine_SwitchOff_AuditCarriesArtistID: switching off deletes her
// row, so the audit is the only record left of whose offering it was.
func TestUpdateMine_SwitchOff_AuditCarriesArtistID(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}
	aud := &captureAudit{}
	user := uuid.New()

	_, err := NewService(repo, aud).UpdateMine(context.Background(), uuid.New(), user,
		repo.current.ServiceID, UpdateRequest{Offered: ptr(false)}, "198.51.100.4")

	require.NoError(t, err)
	require.Len(t, aud.events, 1)
	e := aud.events[0]
	assert.Equal(t, "offering.off", e.Action)
	require.NotNil(t, e.ActorID)
	assert.Equal(t, user, *e.ActorID)
	assert.Equal(t, "artist", e.ActorRole)
	assert.Equal(t, "198.51.100.4", e.IPAddress)
	oldVals, newVals := auditJSON(t, e.OldValues), auditJSON(t, e.NewValues)
	assert.Equal(t, repo.artistID.String(), oldVals["artist_id"])
	assert.Equal(t, repo.artistID.String(), newVals["artist_id"])
	assert.Equal(t, repo.current.ServiceID.String(), newVals["service_id"])
	assert.Equal(t, true, oldVals["offered"])
	assert.Equal(t, false, newVals["offered"])
}

// TestUpdateMine_OfferedAbsent_422 - "offered" is the one field a PUT cannot
// leave out. As a plain bool its zero value is false, so a body that only
// meant to change the deposit ({"deposit_amount":"20"}) read as "switch it
// off" and deleted her row and her custom price. Absent must be refused,
// not guessed.
func TestUpdateMine_OfferedAbsent_422(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{DepositAmount: optional.From("20.00")}, "")

	var e *apperror.AppError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "VALIDATION_ERROR", e.Code)
	require.Len(t, e.Details, 1)
	assert.Equal(t, "offered", e.Details[0].Field)
	assert.Equal(t, 0, repo.deletes, "a missing switch must never delete her row")
	assert.Empty(t, repo.upserts)
}

// TestUpdateMine_UpsertRefusesForeignService_404 - the repository's Upsert
// writes only when the service is on her CURRENT salon's menu and answers
// ErrNotFound otherwise (a salon change between Get and Upsert, say). That
// must reach the client as the same 404 as any other service she cannot
// have - not a 500, and not an audit row for a change that never happened.
func TestUpdateMine_UpsertRefusesForeignService_404(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true), upsertErr: ErrNotFound}
	aud := &captureAudit{}
	_, err := NewService(repo, aud).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: ptr(true), Price: optional.From("120.00")}, "")

	assert.Equal(t, "SERVICE_NOT_FOUND", code(t, err))
	assert.Empty(t, aud.events, "nothing was written, so nothing is audited")
}
