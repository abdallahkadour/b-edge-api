// Package discovery contains unit tests for the discovery service layer.
// These tests use a mock repository - no database required.
package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	listCards         []*ArtistCardRow
	listErr           error
	profile           *ArtistProfileRow
	profileErr        error
	stores            []*StoreRow
	storesErr         error
	services          []*ServiceRow
	servicesErr       error
	hours             []*DayHoursRow
	hoursErr          error
	exceptions        []*ExceptionRow
	exceptionsErr     error
	lastListParams    ListArtistCardsParams
	lastSalonServices uuid.UUID
	// captured args for assertions
	lastHoursStoreIDs []uuid.UUID
	lastExcFrom       time.Time
	lastExcTo         time.Time
}

func (m *mockRepo) ListArtistCards(_ context.Context, f ListArtistCardsParams) ([]*ArtistCardRow, error) {
	m.lastListParams = f
	return m.listCards, m.listErr
}
func (m *mockRepo) GetArtistProfile(_ context.Context, _ uuid.UUID) (*ArtistProfileRow, error) {
	return m.profile, m.profileErr
}
func (m *mockRepo) GetArtistStores(_ context.Context, _ uuid.UUID) ([]*StoreRow, error) {
	return m.stores, m.storesErr
}
func (m *mockRepo) GetSalonServices(_ context.Context, salonID uuid.UUID) ([]*ServiceRow, error) {
	m.lastSalonServices = salonID
	return m.services, m.servicesErr
}
func (m *mockRepo) GetStoreHours(_ context.Context, storeIDs []uuid.UUID) ([]*DayHoursRow, error) {
	m.lastHoursStoreIDs = storeIDs
	return m.hours, m.hoursErr
}
func (m *mockRepo) GetStoreExceptions(_ context.Context, _ []uuid.UUID, from, to time.Time) ([]*ExceptionRow, error) {
	m.lastExcFrom, m.lastExcTo = from, to
	return m.exceptions, m.exceptionsErr
}

// fixedClock returns a service whose "now" is pinned for deterministic New-badge tests.
func newTestService(repo Repository, now time.Time) *Service {
	s := NewService(repo)
	s.now = func() time.Time { return now }
	return s
}

// ── ListArtists tests ─────────────────────────────────────────────────────────

// TestListArtists_InvalidCategory - an unknown category is rejected.
func TestListArtists_InvalidCategory(t *testing.T) {
	svc := newTestService(&mockRepo{}, time.Now())

	_, err := svc.ListArtists(context.Background(), ListArtistsParams{Category: "tattoos"})

	require.Error(t, err)
}

// TestListArtists_ValidCategoryPassesThrough - a valid category reaches the repo.
func TestListArtists_ValidCategoryPassesThrough(t *testing.T) {
	repo := &mockRepo{listCards: nil}
	svc := newTestService(repo, time.Now())

	_, err := svc.ListArtists(context.Background(), ListArtistsParams{Category: "makeup", City: "Beirut"})

	require.NoError(t, err)
	assert.Equal(t, "makeup", repo.lastListParams.Category)
	assert.Equal(t, "Beirut", repo.lastListParams.City)
}

// TestListArtists_DefaultsLimit - a zero/oversized limit is normalised.
func TestListArtists_DefaultsLimit(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo, time.Now())

	_, err := svc.ListArtists(context.Background(), ListArtistsParams{Limit: 0})
	require.NoError(t, err)
	assert.Equal(t, defaultPageSize, repo.lastListParams.Limit)

	_, err = svc.ListArtists(context.Background(), ListArtistsParams{Limit: 9999})
	require.NoError(t, err)
	assert.Equal(t, defaultPageSize, repo.lastListParams.Limit)
}

// TestListArtists_NewBadge - an artist created within the window is flagged new;
// an older one is not.
func TestListArtists_NewBadge(t *testing.T) {
	now := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	recent := now.Add(-5 * 24 * time.Hour)  // 5 days ago → new
	old := now.Add(-90 * 24 * time.Hour)     // 90 days ago → not new

	repo := &mockRepo{
		listCards: []*ArtistCardRow{
			{ID: uuid.New(), Name: "Rania", City: "Beirut", Rating: decimal.NewFromFloat(4.9), CreatedAt: recent},
			{ID: uuid.New(), Name: "Hadi", City: "Beirut", Rating: decimal.NewFromFloat(4.8), CreatedAt: old},
		},
	}
	svc := newTestService(repo, now)

	cards, err := svc.ListArtists(context.Background(), ListArtistsParams{})

	require.NoError(t, err)
	require.Len(t, cards, 2)
	assert.True(t, cards[0].IsNew, "5-day-old artist should be new")
	assert.False(t, cards[1].IsNew, "90-day-old artist should not be new")
}

// ── GetArtistProfile tests ────────────────────────────────────────────────────

// TestGetArtistProfile_NotFound - missing artist surfaces ARTIST_NOT_FOUND.
func TestGetArtistProfile_NotFound(t *testing.T) {
	repo := &mockRepo{profileErr: ErrArtistNotFound}
	svc := newTestService(repo, time.Now())

	_, err := svc.GetArtistProfile(context.Background(), uuid.New())

	require.Error(t, err)
}

// TestGetArtistProfile_Aggregates - profile, stores, and services combine.
func TestGetArtistProfile_Aggregates(t *testing.T) {
	salonID := uuid.New()
	repo := &mockRepo{
		profile: &ArtistProfileRow{
			ID: uuid.New(), Name: "Rania", Rating: decimal.NewFromFloat(4.9),
			ReviewCount: 127, IsVerified: true, SalonID: &salonID,
		},
		stores: []*StoreRow{
			{ID: uuid.New(), Name: "Beirut Downtown", City: "Beirut"},
			{ID: uuid.New(), Name: "Tripoli Center", City: "Tripoli"},
		},
		services: []*ServiceRow{
			{ID: uuid.New(), Name: "Bridal Makeup", DurationMin: 90, Price: decimal.NewFromFloat(200), DepositAmount: decimal.NewFromFloat(50)},
		},
	}
	svc := newTestService(repo, time.Now())

	profile, err := svc.GetArtistProfile(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "Rania", profile.Name)
	assert.True(t, profile.IsVerified)
	require.Len(t, profile.Stores, 2)
	require.Len(t, profile.Services, 1)
	assert.Equal(t, "Bridal Makeup", profile.Services[0].Name)
	assert.Equal(t, salonID, repo.lastSalonServices, "services fetched for the artist's salon")
}

// TestGetArtistProfile_NoSalon - an artist with no salon returns an empty (non-nil)
// services list and does not call GetSalonServices.
func TestGetArtistProfile_NoSalon(t *testing.T) {
	repo := &mockRepo{
		profile: &ArtistProfileRow{
			ID: uuid.New(), Name: "Newbie", Rating: decimal.Zero, SalonID: nil,
		},
		stores: nil,
	}
	svc := newTestService(repo, time.Now())

	profile, err := svc.GetArtistProfile(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.NotNil(t, profile.Services)
	assert.Empty(t, profile.Services)
	assert.Empty(t, profile.Stores)
	assert.Equal(t, uuid.Nil, repo.lastSalonServices, "no salon → GetSalonServices not called")
}

// ── Open/Closed status ────────────────────────────────────────────────────────
//
// Every case pins "now" through the service's injectable clock, so none of
// these depend on when the suite runs. The store's own timezone decides
// which weekday and which local time apply — that is the whole point of
// these tests, and the reason the fixtures below use Asia/Beirut rather
// than UTC.

const testTZ = "Asia/Beirut"

// beirutAt builds an instant at a given Beirut wall-clock time.
func beirutAt(t *testing.T, y int, mo time.Month, d, h, mi int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(testTZ)
	require.NoError(t, err)
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

func storeRowIn(tz string) *StoreRow {
	return &StoreRow{ID: uuid.New(), Name: "Beirut Downtown", City: "Beirut", Timezone: tz}
}

// weekHours returns 09:00–17:00 on every day of the week.
func weekHours(storeID uuid.UUID) []*DayHoursRow {
	rows := make([]*DayHoursRow, 0, 7)
	for d := 0; d < 7; d++ {
		rows = append(rows, &DayHoursRow{
			StoreID: storeID, DayOfWeek: d, IsOpen: true,
			OpenTime: "09:00:00", CloseTime: "17:00:00",
		})
	}
	return rows
}

func TestDeriveOpenStatus_DuringHours_ReportsOpen(t *testing.T) {
	r := storeRowIn(testTZ)
	now := beirutAt(t, 2026, time.August, 12, 13, 0) // Wednesday 1pm local

	st := deriveOpenStatus(r, weekHours(r.ID), nil, now)

	assert.True(t, st.IsOpen)
	assert.Equal(t, ReasonOpen, st.Reason)
	require.NotNil(t, st.ClosesAt, "an open store must say when it closes")
	assert.Equal(t, 17, st.ClosesAt.In(st.ClosesAt.Location()).Hour())
}

func TestDeriveOpenStatus_BeforeOpening_ReportsOpensAt(t *testing.T) {
	r := storeRowIn(testTZ)
	now := beirutAt(t, 2026, time.August, 12, 7, 30)

	st := deriveOpenStatus(r, weekHours(r.ID), nil, now)

	assert.False(t, st.IsOpen)
	assert.Equal(t, ReasonOutsideHours, st.Reason)
	require.NotNil(t, st.OpensAt, "before opening, the store must say when it opens")
	assert.True(t, st.OpensAt.After(now))
}

// After closing, OpensAt stays nil rather than pointing at tomorrow —
// answering "when do you next open" needs the whole week plus exception
// lookahead, which this deliberately does not attempt.
func TestDeriveOpenStatus_AfterClosing_NoOpensAt(t *testing.T) {
	r := storeRowIn(testTZ)
	now := beirutAt(t, 2026, time.August, 12, 20, 0)

	st := deriveOpenStatus(r, weekHours(r.ID), nil, now)

	assert.False(t, st.IsOpen)
	assert.Equal(t, ReasonOutsideHours, st.Reason)
	assert.Nil(t, st.OpensAt, "after closing we do not claim a next opening time")
}

func TestDeriveOpenStatus_NonTradingWeekday_ReportsClosedToday(t *testing.T) {
	r := storeRowIn(testTZ)
	hours := weekHours(r.ID)
	now := beirutAt(t, 2026, time.August, 12, 13, 0) // Wednesday
	hours[int(now.Weekday())].IsOpen = false

	st := deriveOpenStatus(r, hours, nil, now)

	assert.False(t, st.IsOpen)
	assert.Equal(t, ReasonClosedToday, st.Reason)
}

func TestDeriveOpenStatus_HolidayException_ReportsHoliday(t *testing.T) {
	r := storeRowIn(testTZ)
	now := beirutAt(t, 2026, time.August, 12, 13, 0)
	excs := []*ExceptionRow{{StoreID: r.ID, ExceptionDate: now, IsClosed: true}}

	st := deriveOpenStatus(r, weekHours(r.ID), excs, now)

	assert.False(t, st.IsOpen)
	assert.Equal(t, ReasonHoliday, st.Reason,
		"a dated closure must be distinguishable from simply not trading that weekday")
}

func TestDeriveOpenStatus_ExceptionCustomHours_Applied(t *testing.T) {
	r := storeRowIn(testTZ)
	now := beirutAt(t, 2026, time.August, 12, 10, 0) // inside 09-17, outside 11-15
	o, c := "11:00:00", "15:00:00"
	excs := []*ExceptionRow{{StoreID: r.ID, ExceptionDate: now, OpenTime: &o, CloseTime: &c}}

	st := deriveOpenStatus(r, weekHours(r.ID), excs, now)

	assert.False(t, st.IsOpen, "custom hours must override the regular ones")
	assert.Equal(t, ReasonOutsideHours, st.Reason)
}

// No hours configured must never render as "Closed" — telling a customer a
// salon is shut when nobody filled in the hours costs the artist bookings.
func TestDeriveOpenStatus_NoHoursConfigured_ReportsUnknown(t *testing.T) {
	r := storeRowIn(testTZ)
	now := beirutAt(t, 2026, time.August, 12, 13, 0)

	st := deriveOpenStatus(r, nil, nil, now)

	assert.False(t, st.IsOpen)
	assert.Equal(t, ReasonUnknown, st.Reason)
}

func TestDeriveOpenStatus_MalformedHours_ReportsUnknown(t *testing.T) {
	r := storeRowIn(testTZ)
	now := beirutAt(t, 2026, time.August, 12, 13, 0)
	bad := []*DayHoursRow{{
		StoreID: r.ID, DayOfWeek: int(now.Weekday()), IsOpen: true,
		OpenTime: "nonsense", CloseTime: "17:00:00",
	}}

	st := deriveOpenStatus(r, bad, nil, now)

	assert.Equal(t, ReasonUnknown, st.Reason, "bad data must degrade to no badge, not to a wrong one")
}

// The store's timezone decides the weekday, not the server's. At 23:30 UTC
// on Tuesday it is already Wednesday in Beirut, so Wednesday's hours apply.
func TestDeriveOpenStatus_LateUTCEvening_UsesStoreLocalWeekday(t *testing.T) {
	r := storeRowIn(testTZ)
	hours := weekHours(r.ID)
	// Close Wednesday specifically; Tuesday stays open.
	hours[int(time.Wednesday)].IsOpen = false

	// 23:30 UTC Tuesday Aug 11 == 02:30 Wednesday Aug 12 in Beirut.
	now := time.Date(2026, time.August, 11, 23, 30, 0, 0, time.UTC)
	require.Equal(t, time.Tuesday, now.Weekday(), "fixture must be Tuesday in UTC")

	st := deriveOpenStatus(r, hours, nil, now)

	assert.Equal(t, ReasonClosedToday, st.Reason,
		"Wednesday's hours must apply — it is already Wednesday where the store is")
}

// ── buildStoreCards ───────────────────────────────────────────────────────────

// A failed hours read must not fail the whole profile — a customer losing
// access to a salon's page because its opening hours could not be loaded
// would be far worse than a missing badge.
func TestBuildStoreCards_HoursReadFails_ProfileStillRenders(t *testing.T) {
	r := storeRowIn(testTZ)
	repo := &mockRepo{hoursErr: context.DeadlineExceeded}
	svc := newTestService(repo, time.Now())

	cards, err := svc.buildStoreCards(context.Background(), []*StoreRow{r}, time.Now())

	require.NoError(t, err, "a hours read failure must not fail the profile")
	require.Len(t, cards, 1)
	assert.Equal(t, ReasonUnknown, cards[0].OpenStatus.Reason)
}

// Hours and exceptions are fetched once for all stores, not once per store.
func TestBuildStoreCards_ManyStores_BatchesTheLookup(t *testing.T) {
	a, b, c := storeRowIn(testTZ), storeRowIn(testTZ), storeRowIn(testTZ)
	repo := &mockRepo{}
	svc := newTestService(repo, time.Now())

	_, err := svc.buildStoreCards(context.Background(), []*StoreRow{a, b, c}, time.Now())

	require.NoError(t, err)
	assert.Len(t, repo.lastHoursStoreIDs, 3,
		"all store IDs must go out in one query, not one query per store")
}

// The exception window must straddle now, because a store's local date can
// be a day ahead of or behind the server's.
func TestBuildStoreCards_ExceptionWindow_StraddlesNow(t *testing.T) {
	r := storeRowIn(testTZ)
	repo := &mockRepo{}
	svc := newTestService(repo, time.Now())
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)

	_, err := svc.buildStoreCards(context.Background(), []*StoreRow{r}, now)

	require.NoError(t, err)
	assert.True(t, repo.lastExcFrom.Before(now), "window must start before now")
	assert.True(t, repo.lastExcTo.After(now), "window must end after now")
}

func TestBuildStoreCards_NoStores_ReturnsEmptyNotNil(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo, time.Now())

	cards, err := svc.buildStoreCards(context.Background(), nil, time.Now())

	require.NoError(t, err)
	assert.NotNil(t, cards, "an empty slice, never nil — Go marshals nil as null")
	assert.Empty(t, cards)
}
