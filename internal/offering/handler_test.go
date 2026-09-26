package offering

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// The handlers over the REAL service and the house error handler, driven by
// HTTP requests. The capability guards are not mounted here: routecoverage
// and salonrole test those. What these pin is the handler's own job - which
// ids it reads from the token and which from the URL, and that every failure
// it can meet reaches the client with the right status and code.

// spyRepo records the ids the service was handed, so a test can prove the
// handler passed the TOKEN's salon and user and the URL's artist.
type spyRepo struct {
	*mockRepo
	userAsked, listSalon, listArtist uuid.UUID
}

func (s *spyRepo) ArtistIDForUser(ctx context.Context, u uuid.UUID) (uuid.UUID, error) {
	s.userAsked = u
	return s.mockRepo.ArtistIDForUser(ctx, u)
}

func (s *spyRepo) List(ctx context.Context, salonID, artistID uuid.UUID) ([]*Offering, error) {
	s.listSalon, s.listArtist = salonID, artistID
	return s.mockRepo.List(ctx, salonID, artistID)
}

type caller struct{ user, salon uuid.UUID }

// newTestApp mounts the four handlers behind a stand-in for RequireAuth that
// puts the caller's identity where the real middleware does.
func newTestApp(repo Repository, aud *captureAudit, who caller) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", who.user)
		salon := who.salon
		c.Locals("salon_id", &salon)
		return c.Next()
	})
	h := &Handler{svc: NewService(repo, aud)}
	app.Get("/my-services", h.ListMine)
	app.Put("/my-services/:serviceId", h.UpdateMine)
	app.Get("/members/:artistId/services", h.ListForMember)
	app.Put("/members/:artistId/services/:serviceId", h.UpdateForMember)
	return app
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func do(t *testing.T, app *fiber.App, method, path, body string) (int, envelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req, -1)
	require.NoError(t, err)
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	var env envelope
	require.NoError(t, json.Unmarshal(raw, &env), "body: %s", raw)
	return res.StatusCode, env
}

func errCode(env envelope) string {
	if env.Error == nil {
		return ""
	}
	return env.Error.Code
}

func TestListMine_ReadsTheTokensSalonAndUser_200(t *testing.T) {
	who := caller{user: uuid.New(), salon: uuid.New()}
	spy := &spyRepo{mockRepo: &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}}

	st, env := do(t, newTestApp(spy, &captureAudit{}, who), "GET", "/my-services", "")

	require.Equal(t, 200, st, "error %q", errCode(env))
	assert.Equal(t, who.user, spy.userAsked, "her artist is resolved from the token's user")
	assert.Equal(t, who.salon, spy.listSalon, "the menu is the token's salon")
	assert.Equal(t, spy.artistID, spy.listArtist)
	var rows []Offering
	require.NoError(t, json.Unmarshal(env.Data, &rows))
	assert.Len(t, rows, 1)
}

func TestListMine_NoLongerInTheSalon_404(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: false}

	st, env := do(t, newTestApp(repo, &captureAudit{}, caller{uuid.New(), uuid.New()}), "GET", "/my-services", "")

	assert.Equal(t, 404, st)
	assert.Equal(t, "MEMBER_NOT_FOUND", errCode(env))
}

func TestUpdateMine_ServiceIDNotAUUID_400(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}

	st, env := do(t, newTestApp(repo, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"PUT", "/my-services/not-a-uuid", `{"offered":true}`)

	assert.Equal(t, 400, st)
	assert.Equal(t, "INVALID_ID", errCode(env))
	assert.Empty(t, repo.upserts)
}

func TestUpdateMine_MalformedBody_400_NothingWritten(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}

	st, _ := do(t, newTestApp(repo, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"PUT", "/my-services/"+repo.current.ServiceID.String(), `{"offered":`)

	assert.Equal(t, 400, st)
	assert.Empty(t, repo.upserts)
	assert.Zero(t, repo.deletes)
}

func TestUpdateMine_SetsHerPrice_200_AuditsTheCaller(t *testing.T) {
	who := caller{user: uuid.New(), salon: uuid.New()}
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}
	aud := &captureAudit{}

	st, env := do(t, newTestApp(repo, aud, who),
		"PUT", "/my-services/"+repo.current.ServiceID.String(), `{"offered":true,"price":"200.00"}`)

	require.Equal(t, 200, st, "error %q", errCode(env))
	require.Len(t, repo.upserts, 1)
	assert.Equal(t, repo.current.ServiceID, repo.upserts[0].ServiceID, "the URL's service")
	assert.Equal(t, repo.artistID, repo.upserts[0].ArtistID, "her own artist, from the token")
	require.Len(t, aud.events, 1)
	require.NotNil(t, aud.events[0].ActorID)
	assert.Equal(t, who.user, *aud.events[0].ActorID)
	assert.NotEmpty(t, aud.events[0].IPAddress, "the caller's IP reaches the audit row")
}

func TestUpdateMine_ServiceRefused_ErrorReachesTheClient(t *testing.T) {
	// A money error from the service must come back as the client's 400,
	// not be swallowed into a 200.
	repo := &mockRepo{artistID: uuid.New(), inSalon: true, current: menuRow(true)}

	st, env := do(t, newTestApp(repo, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"PUT", "/my-services/"+repo.current.ServiceID.String(), `{"offered":true,"price":"10.999"}`)

	assert.Equal(t, 400, st)
	assert.Equal(t, "INVALID_PRICE", errCode(env))
	assert.Empty(t, repo.upserts)
}

func TestListForMember_ArtistIDNotAUUID_400(t *testing.T) {
	st, env := do(t, newTestApp(&mockRepo{inSalon: true}, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"GET", "/members/not-a-uuid/services", "")

	assert.Equal(t, 400, st)
	assert.Equal(t, "INVALID_ID", errCode(env))
}

func TestListForMember_ReadsTheURLsArtist_200(t *testing.T) {
	who := caller{user: uuid.New(), salon: uuid.New()}
	member := uuid.New()
	spy := &spyRepo{mockRepo: &mockRepo{inSalon: true, current: menuRow(true)}}

	st, env := do(t, newTestApp(spy, &captureAudit{}, who), "GET", "/members/"+member.String()+"/services", "")

	require.Equal(t, 200, st, "error %q", errCode(env))
	assert.Equal(t, member, spy.listArtist, "the member named in the URL")
	assert.Equal(t, who.salon, spy.listSalon, "within the caller's own salon")
}

func TestListForMember_MemberOfAnotherSalon_404(t *testing.T) {
	st, env := do(t, newTestApp(&mockRepo{inSalon: false}, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"GET", "/members/"+uuid.NewString()+"/services", "")

	assert.Equal(t, 404, st)
	assert.Equal(t, "MEMBER_NOT_FOUND", errCode(env))
}

func TestUpdateForMember_ArtistIDNotAUUID_400(t *testing.T) {
	repo := &mockRepo{inSalon: true, current: menuRow(true)}

	st, env := do(t, newTestApp(repo, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"PUT", "/members/not-a-uuid/services/"+repo.current.ServiceID.String(), `{"offered":true}`)

	assert.Equal(t, 400, st)
	assert.Equal(t, "INVALID_ID", errCode(env))
	assert.Empty(t, repo.upserts)
}

func TestUpdateForMember_ServiceIDNotAUUID_400(t *testing.T) {
	repo := &mockRepo{inSalon: true, current: menuRow(true)}

	st, env := do(t, newTestApp(repo, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"PUT", "/members/"+uuid.NewString()+"/services/not-a-uuid", `{"offered":true}`)

	assert.Equal(t, 400, st)
	assert.Equal(t, "INVALID_ID", errCode(env))
	assert.Empty(t, repo.upserts)
}

func TestUpdateForMember_MalformedBody_400_NothingWritten(t *testing.T) {
	repo := &mockRepo{inSalon: true, current: menuRow(true)}

	st, _ := do(t, newTestApp(repo, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"PUT", "/members/"+uuid.NewString()+"/services/"+repo.current.ServiceID.String(), `{"offered":`)

	assert.Equal(t, 400, st)
	assert.Empty(t, repo.upserts)
}

func TestUpdateForMember_OwnerSetsHerPrice_200_OwnerIsTheActor(t *testing.T) {
	owner := caller{user: uuid.New(), salon: uuid.New()}
	member := uuid.New()
	repo := &mockRepo{inSalon: true, current: menuRow(true)}
	aud := &captureAudit{}

	st, env := do(t, newTestApp(repo, aud, owner),
		"PUT", "/members/"+member.String()+"/services/"+repo.current.ServiceID.String(),
		`{"offered":true,"price":"120.00"}`)

	require.Equal(t, 200, st, "error %q", errCode(env))
	require.Len(t, repo.upserts, 1)
	assert.Equal(t, member, repo.upserts[0].ArtistID, "the member's row, from the URL")
	require.Len(t, aud.events, 1)
	require.NotNil(t, aud.events[0].ActorID)
	assert.Equal(t, owner.user, *aud.events[0].ActorID, "the OWNER is recorded as the actor")
}

func TestUpdateForMember_MemberOfAnotherSalon_404_NothingWritten(t *testing.T) {
	repo := &mockRepo{inSalon: false, current: menuRow(true)}

	st, env := do(t, newTestApp(repo, &captureAudit{}, caller{uuid.New(), uuid.New()}),
		"PUT", "/members/"+uuid.NewString()+"/services/"+repo.current.ServiceID.String(), `{"offered":true}`)

	assert.Equal(t, 404, st)
	assert.Equal(t, "MEMBER_NOT_FOUND", errCode(env))
	assert.Empty(t, repo.upserts)
}
