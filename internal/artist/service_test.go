// Package artist contains unit tests for the artist service layer.
// These tests use mock repositories - no database required.
package artist

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// ── TestMain ──────────────────────────────────────────────────────────────────

func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", "test-secret-that-is-long-enough-for-testing-purposes-only")
	os.Setenv("JWT_REFRESH_SECRET", "test-refresh-secret-that-is-long-enough-for-testing-only")
	os.Setenv("APP_ENV", "test")
	os.Exit(m.Run())
}

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	getArtistByIDProfile          *ArtistProfile
	getArtistByIDErr              error
	getArtistByUserIDProfile      *ArtistProfile
	getArtistByUserIDErr          error
	updateArtistProfileErr        error
	getStoresByArtistStores       []*Store
	getStoresByArtistErr          error
	getStoresBySalonStores        []*Store
	getStoresBySalonErr           error
	getServicesBySalonSvcs        []*SalonServiceRecord
	getServicesBySalonErr         error
	getServiceByIDSvc             *SalonServiceRecord
	getServiceByIDErr             error
	createServiceErr              error
	updateServiceErr              error
	deleteServiceErr              error
	getBusinessHoursBH            []*BusinessHours
	getBusinessHoursErr           error
	setBusinessHoursErr           error
	getExceptionsEx               []*BusinessHoursException
	getExceptionsErr              error
	createExceptionErr            error
	deleteExceptionErr            error
	getStoreByIDStore             *Store
	getStoreByIDErr               error
	updateStoreErr                error
	createStoreErr                error
	createStoreCalledWithArtistID uuid.UUID
	getArtistIDByHandleID         uuid.UUID
	isArtistActiveValue           bool
	isArtistActiveSet             bool
	isArtistActiveErr             error
	getArtistIDByHandleErr        error
}

func (m *mockRepo) GetArtistByID(_ context.Context, _ uuid.UUID) (*ArtistProfile, error) {
	return m.getArtistByIDProfile, m.getArtistByIDErr
}
func (m *mockRepo) GetArtistIDByHandle(_ context.Context, _ string) (uuid.UUID, error) {
	return m.getArtistIDByHandleID, m.getArtistIDByHandleErr
}
func (m *mockRepo) IsArtistActive(_ context.Context, _ uuid.UUID) (bool, error) {
	if m.isArtistActiveErr != nil {
		return false, m.isArtistActiveErr
	}
	// Defaults true so every EXISTING test (written before this method
	// existed) keeps passing without needing to know about it - only
	// tests specifically about the pending/active gate set this field.
	if !m.isArtistActiveSet {
		return true, nil
	}
	return m.isArtistActiveValue, nil
}
func (m *mockRepo) GetArtistByUserID(_ context.Context, _ uuid.UUID) (*ArtistProfile, error) {
	return m.getArtistByUserIDProfile, m.getArtistByUserIDErr
}
func (m *mockRepo) UpdateArtistProfile(_ context.Context, _ uuid.UUID, _ UpdateProfileRequest) error {
	return m.updateArtistProfileErr
}
func (m *mockRepo) GetStoresByArtist(_ context.Context, _ uuid.UUID) ([]*Store, error) {
	return m.getStoresByArtistStores, m.getStoresByArtistErr
}
func (m *mockRepo) GetStoresBySalon(_ context.Context, _ uuid.UUID) ([]*Store, error) {
	return m.getStoresBySalonStores, m.getStoresBySalonErr
}
func (m *mockRepo) GetStoreByID(_ context.Context, _ uuid.UUID) (*Store, error) {
	return m.getStoreByIDStore, m.getStoreByIDErr
}
func (m *mockRepo) UpdateStore(_ context.Context, _ uuid.UUID, _ UpdateStoreRequest) error {
	return m.updateStoreErr
}
func (m *mockRepo) CreateStore(_ context.Context, store *Store, artistID uuid.UUID) error {
	if m.createStoreErr != nil {
		return m.createStoreErr
	}
	store.ID = uuid.New()
	m.createStoreCalledWithArtistID = artistID
	return nil
}

func (m *mockRepo) GetServicesBySalon(_ context.Context, _ uuid.UUID) ([]*SalonServiceRecord, error) {
	return m.getServicesBySalonSvcs, m.getServicesBySalonErr
}
func (m *mockRepo) GetServiceByID(_ context.Context, _ uuid.UUID) (*SalonServiceRecord, error) {
	return m.getServiceByIDSvc, m.getServiceByIDErr
}
func (m *mockRepo) CreateService(_ context.Context, s *SalonServiceRecord) error {
	s.CreatedAt = time.Now()
	s.UpdatedAt = time.Now()
	return m.createServiceErr
}
func (m *mockRepo) UpdateService(_ context.Context, _ uuid.UUID, _ UpdateServiceRequest) error {
	return m.updateServiceErr
}
func (m *mockRepo) DeleteService(_ context.Context, _ uuid.UUID) error {
	return m.deleteServiceErr
}
func (m *mockRepo) GetBusinessHours(_ context.Context, _ uuid.UUID) ([]*BusinessHours, error) {
	return m.getBusinessHoursBH, m.getBusinessHoursErr
}
func (m *mockRepo) SetBusinessHours(_ context.Context, _ uuid.UUID, _ SetBusinessHoursRequest) error {
	return m.setBusinessHoursErr
}
func (m *mockRepo) GetExceptions(_ context.Context, _ uuid.UUID) ([]*BusinessHoursException, error) {
	return m.getExceptionsEx, m.getExceptionsErr
}
func (m *mockRepo) CreateException(_ context.Context, _ uuid.UUID, _ CreateExceptionRequest) error {
	return m.createExceptionErr
}
func (m *mockRepo) DeleteException(_ context.Context, _ uuid.UUID, _ time.Time) error {
	return m.deleteExceptionErr
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestService(repo Repository) *Service {
	return NewService(repo)
}

func defaultArtistProfile() *ArtistProfile {
	bio := "Expert makeup artist based in Beirut"
	instagram := "rania.beauty"
	phone := "+96170123456"
	return &ArtistProfile{
		ID:          uuid.New(),
		UserID:      uuid.New(),
		Name:        "Rania",
		Email:       "rania@bedge.com",
		Phone:       &phone,
		Bio:         &bio,
		Instagram:   &instagram,
		Rating:      decimal.NewFromFloat(4.9),
		ReviewCount: 120,
		IsVerified:  true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

func defaultStore() *Store {
	cutoff := "09:00:00"
	return &Store{
		ID:                 uuid.New(),
		SalonID:            uuid.New(),
		Name:               "Beirut Downtown",
		City:               "Beirut",
		Country:            "Lebanon",
		SameDayNoticeHours: 4,
		EarlyBirdCutoff:    &cutoff,
		EarlyBirdFee:       decimal.NewFromFloat(10.00),
		WeekdayBufferMin:   150,
		WeekendBufferMin:   90,
		IsActive:           true,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
}

func defaultSalonServiceRecord() *SalonServiceRecord {
	desc := "Full glam makeup for special occasions"
	return &SalonServiceRecord{
		ID:                   uuid.New(),
		SalonID:              uuid.New(),
		Name:                 "Full Makeup",
		Description:          &desc,
		DurationMin:          120,
		Price:                decimal.NewFromFloat(200.00),
		DepositAmount:        decimal.NewFromFloat(50.00),
		DepositDeadlineHours: 48,
		IsActive:             true,
		IsCustom:             false,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

// ── GetArtistByID tests ───────────────────────────────────────────────────────

func TestGetArtistByID_Success(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{
		getArtistByIDProfile: profile,
		getArtistByIDErr:     nil,
	}
	svc := newTestService(repo)

	result, err := svc.GetArtistByID(context.Background(), profile.ID)

	require.NoError(t, err)
	assert.Equal(t, profile.ID, result.ID)
	assert.Equal(t, "Rania", result.Name)
	assert.True(t, result.IsVerified)
}

// ── ResolveArtistID tests ───────────────────────────────────────────────────
//
// Guards the whole point of the handle feature: a real UUID resolves without
// touching the repo at all, a handle resolves via GetArtistIDByHandle, and a
// handle that doesn't exist surfaces the same ErrArtistNotFound a bad UUID
// lookup would - not a different error a client could use to distinguish
// "this handle isn't taken" from "this UUID doesn't exist".

func TestResolveArtistID_ValidUUID_ReturnsDirectly(t *testing.T) {
	realID := uuid.New()
	repo := &mockRepo{
		// Deliberately different from realID - if ResolveArtistID
		// mistakenly fell through to the handle lookup for a valid UUID,
		// this would catch it by returning the wrong ID.
		getArtistIDByHandleID: uuid.New(),
	}
	svc := newTestService(repo)

	result, err := svc.ResolveArtistID(context.Background(), realID.String())

	require.NoError(t, err)
	assert.Equal(t, realID, result)
}

func TestResolveArtistID_Handle_ResolvesViaRepo(t *testing.T) {
	resolvedID := uuid.New()
	repo := &mockRepo{
		getArtistIDByHandleID: resolvedID,
	}
	svc := newTestService(repo)

	result, err := svc.ResolveArtistID(context.Background(), "rania")

	require.NoError(t, err)
	assert.Equal(t, resolvedID, result)
}

func TestResolveArtistID_UnknownHandle_ReturnsArtistNotFound(t *testing.T) {
	repo := &mockRepo{
		getArtistIDByHandleErr: ErrArtistNotFound,
	}
	svc := newTestService(repo)

	_, err := svc.ResolveArtistID(context.Background(), "nonexistent-handle")

	assert.ErrorIs(t, err, ErrArtistNotFound)
}

// TestResolveArtistID_PendingArtistUUID_ReturnsNotFound guards the second
// half of the review-gate fix: a caller holding a PENDING artist's raw
// UUID directly (an old shared link, or simply guessing one) must not
// bypass the review gate just because a UUID needs no handle lookup to
// parse. Before this test existed, an artist who was never approved
// could still be reached by anyone who had - or guessed - their ID.
func TestResolveArtistID_PendingArtistUUID_ReturnsNotFound(t *testing.T) {
	pendingID := uuid.New()
	repo := &mockRepo{
		isArtistActiveSet:   true,
		isArtistActiveValue: false,
	}
	svc := newTestService(repo)

	_, err := svc.ResolveArtistID(context.Background(), pendingID.String())

	assert.ErrorIs(t, err, ErrArtistNotFound)
}

func TestResolveArtistID_ActiveArtistUUID_Succeeds(t *testing.T) {
	activeID := uuid.New()
	repo := &mockRepo{
		isArtistActiveSet:   true,
		isArtistActiveValue: true,
	}
	svc := newTestService(repo)

	result, err := svc.ResolveArtistID(context.Background(), activeID.String())

	require.NoError(t, err)
	assert.Equal(t, activeID, result)
}

// ── UpdateProfile handle tests ───────────────────────────────────────────────

func TestUpdateProfile_ValidHandle_Success(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{
		getArtistByIDProfile: profile,
	}
	svc := newTestService(repo)

	handle := "rania-beauty"
	_, err := svc.UpdateProfile(context.Background(), profile.ID, profile.UserID, UpdateProfileRequest{
		Handle: &handle,
	})

	require.NoError(t, err)
}

func TestUpdateProfile_InvalidHandleFormat_BadRequest(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{
		getArtistByIDProfile: profile,
	}
	svc := newTestService(repo)

	for _, bad := range []string{"-rania", "rania-", "Rania", "ra nia", "ra_nia", "ra"} {
		handle := bad
		_, err := svc.UpdateProfile(context.Background(), profile.ID, profile.UserID, UpdateProfileRequest{
			Handle: &handle,
		})
		assert.Error(t, err, "expected %q to be rejected as an invalid handle", bad)
	}
}

func TestUpdateProfile_HandleTaken_Conflict(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{
		getArtistByIDProfile:   profile,
		updateArtistProfileErr: ErrHandleTaken,
	}
	svc := newTestService(repo)

	handle := "already-taken"
	_, err := svc.UpdateProfile(context.Background(), profile.ID, profile.UserID, UpdateProfileRequest{
		Handle: &handle,
	})

	assert.Error(t, err)
}

func TestGetArtistByID_NotFound(t *testing.T) {
	repo := &mockRepo{
		getArtistByIDErr: ErrArtistNotFound,
	}
	svc := newTestService(repo)

	result, err := svc.GetArtistByID(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, result)

	var appErr *apperror.AppError
	require.True(t, isAppError(err, &appErr))
	assert.Equal(t, "ARTIST_NOT_FOUND", appErr.Code)
}

// ── GetMyProfile tests ────────────────────────────────────────────────────────

func TestGetMyProfile_Success(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{
		getArtistByUserIDProfile: profile,
		getArtistByUserIDErr:     nil,
	}
	svc := newTestService(repo)

	result, err := svc.GetMyProfile(context.Background(), profile.UserID)

	require.NoError(t, err)
	assert.Equal(t, profile.UserID, result.UserID)
	assert.Equal(t, "rania@bedge.com", result.Email)
}

func TestGetMyProfile_NotFound(t *testing.T) {
	repo := &mockRepo{
		getArtistByUserIDErr: ErrArtistNotFound,
	}
	svc := newTestService(repo)

	result, err := svc.GetMyProfile(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, result)
}

// ── UpdateProfile tests ───────────────────────────────────────────────────────

func TestUpdateProfile_Success(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{
		getArtistByIDProfile:   profile,
		updateArtistProfileErr: nil,
	}
	svc := newTestService(repo)

	newBio := "Updated bio"
	req := UpdateProfileRequest{Bio: &newBio}

	result, err := svc.UpdateProfile(context.Background(), profile.ID, profile.UserID, req)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestUpdateProfile_NotOwner(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{
		getArtistByIDProfile: profile,
	}
	svc := newTestService(repo)

	newBio := "Updated bio"
	req := UpdateProfileRequest{Bio: &newBio}

	// Different user ID - not the owner
	result, err := svc.UpdateProfile(context.Background(), profile.ID, uuid.New(), req)

	require.Error(t, err)
	assert.Nil(t, result)

	var appErr *apperror.AppError
	require.True(t, isAppError(err, &appErr))
	assert.Equal(t, "NOT_ARTIST_OWNER", appErr.Code)
}

// ── GetStoresByArtist tests ───────────────────────────────────────────────────

func TestGetStoresByArtist_Success(t *testing.T) {
	stores := []*Store{defaultStore(), defaultStore()}
	repo := &mockRepo{
		getStoresByArtistStores: stores,
	}
	svc := newTestService(repo)

	result, err := svc.GetStoresByArtist(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestGetStoresByArtist_Empty(t *testing.T) {
	repo := &mockRepo{
		getStoresByArtistStores: nil,
	}
	svc := newTestService(repo)

	result, err := svc.GetStoresByArtist(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Empty(t, result)
}

// ── CreateService tests ───────────────────────────────────────────────────────

func TestCreateService_Success(t *testing.T) {
	repo := &mockRepo{
		createServiceErr: nil,
	}
	svc := newTestService(repo)

	req := CreateServiceRequest{
		Name:                 "Kreatin Treatment",
		DurationMin:          180,
		Price:                "250.00",
		DepositAmount:        "75.00",
		DepositDeadlineHours: 48,
	}

	result, err := svc.CreateService(context.Background(), uuid.New(), req)

	require.NoError(t, err)
	assert.Equal(t, "Kreatin Treatment", result.Name)
	assert.Equal(t, 180, result.DurationMin)
	assert.True(t, result.IsActive)
}

func TestCreateService_InvalidPrice(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	req := CreateServiceRequest{
		Name:                 "Test Service",
		DurationMin:          60,
		Price:                "not-a-number",
		DepositAmount:        "50.00",
		DepositDeadlineHours: 48,
	}

	result, err := svc.CreateService(context.Background(), uuid.New(), req)

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestCreateService_NegativePrice(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	req := CreateServiceRequest{
		Name:                 "Test Service",
		DurationMin:          60,
		Price:                "-50.00",
		DepositAmount:        "25.00",
		DepositDeadlineHours: 48,
	}

	result, err := svc.CreateService(context.Background(), uuid.New(), req)

	require.Error(t, err)
	assert.Nil(t, result)
}

// ── UpdateService tests ───────────────────────────────────────────────────────

func TestUpdateService_Success(t *testing.T) {
	salonID := uuid.New()
	existing := defaultSalonServiceRecord()
	existing.SalonID = salonID

	// After update, return updated record
	updatedName := "Updated Makeup"
	updated := defaultSalonServiceRecord()
	updated.SalonID = salonID
	updated.Name = updatedName

	repo := &mockRepo{
		getServiceByIDSvc: existing,
		updateServiceErr:  nil,
	}
	// Second call to GetServiceByID returns updated
	repo.getServiceByIDSvc = updated

	svc := newTestService(repo)

	name := "Updated Makeup"
	req := UpdateServiceRequest{Name: &name}

	result, err := svc.UpdateService(context.Background(), existing.ID, salonID, req)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestUpdateService_WrongSalon(t *testing.T) {
	existing := defaultSalonServiceRecord()
	existing.SalonID = uuid.New() // belongs to a different salon

	repo := &mockRepo{
		getServiceByIDSvc: existing,
	}
	svc := newTestService(repo)

	name := "Hacked Service"
	req := UpdateServiceRequest{Name: &name}

	// Different salonID - should be forbidden
	result, err := svc.UpdateService(context.Background(), existing.ID, uuid.New(), req)

	require.Error(t, err)
	assert.Nil(t, result)

	var appErr *apperror.AppError
	require.True(t, isAppError(err, &appErr))
	assert.Equal(t, "FORBIDDEN", appErr.Code)
}

// ── DeleteService tests ───────────────────────────────────────────────────────

func TestDeleteService_Success(t *testing.T) {
	salonID := uuid.New()
	existing := defaultSalonServiceRecord()
	existing.SalonID = salonID

	repo := &mockRepo{
		getServiceByIDSvc: existing,
		deleteServiceErr:  nil,
	}
	svc := newTestService(repo)

	err := svc.DeleteService(context.Background(), existing.ID, salonID)

	require.NoError(t, err)
}

func TestDeleteService_NotFound(t *testing.T) {
	repo := &mockRepo{
		getServiceByIDErr: ErrServiceNotFound,
	}
	svc := newTestService(repo)

	err := svc.DeleteService(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)

	var appErr *apperror.AppError
	require.True(t, isAppError(err, &appErr))
	assert.Equal(t, "SERVICE_NOT_FOUND", appErr.Code)
}

// ── SetBusinessHours tests ────────────────────────────────────────────────────

func TestSetBusinessHours_Success(t *testing.T) {
	storeID, salonID := uuid.New(), uuid.New()
	repo := &mockRepo{setBusinessHoursErr: nil, getStoreByIDStore: &Store{ID: storeID, SalonID: salonID}}
	svc := newTestService(repo)

	req := SetBusinessHoursRequest{
		DayOfWeek: 1,
		OpenTime:  "09:00:00",
		CloseTime: "18:00:00",
		IsOpen:    true,
	}

	err := svc.SetBusinessHours(context.Background(), storeID, salonID, req)
	require.NoError(t, err)
}

func TestSetBusinessHours_InvalidTimeFormat(t *testing.T) {
	storeID, salonID := uuid.New(), uuid.New()
	repo := &mockRepo{getStoreByIDStore: &Store{ID: storeID, SalonID: salonID}}
	svc := newTestService(repo)

	req := SetBusinessHoursRequest{
		DayOfWeek: 1,
		OpenTime:  "9am", // invalid format
		CloseTime: "6pm", // invalid format
		IsOpen:    true,
	}

	err := svc.SetBusinessHours(context.Background(), storeID, salonID, req)
	require.Error(t, err)
}

// ── CreateException tests ─────────────────────────────────────────────────────

func TestCreateException_Success(t *testing.T) {
	storeID, salonID := uuid.New(), uuid.New()
	repo := &mockRepo{createExceptionErr: nil, getStoreByIDStore: &Store{ID: storeID, SalonID: salonID}}
	svc := newTestService(repo)

	req := CreateExceptionRequest{
		ExceptionDate: "2027-12-25",
		IsClosed:      true,
		Reason:        strPtr("Christmas"),
	}

	err := svc.CreateException(context.Background(), storeID, salonID, req)
	require.NoError(t, err)
}

func TestCreateException_InvalidDate(t *testing.T) {
	storeID, salonID := uuid.New(), uuid.New()
	repo := &mockRepo{getStoreByIDStore: &Store{ID: storeID, SalonID: salonID}}
	svc := newTestService(repo)

	req := CreateExceptionRequest{
		ExceptionDate: "25-12-2027", // wrong format
		IsClosed:      true,
	}

	err := svc.CreateException(context.Background(), storeID, salonID, req)
	require.Error(t, err)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }

func isAppError(err error, target **apperror.AppError) bool {
	if e, ok := err.(*apperror.AppError); ok {
		*target = e
		return true
	}
	return false
}

// ── Cross-tenant authorization tests ─────────────────────────────────────────
//
// These lock in assertStoreOwnership, added after the August 2026 security
// audit found five endpoints (business hours ×2, exceptions ×3) accepting
// store_id straight from the URL with no ownership check. artistOnly proved
// the caller was AN artist; nothing proved the store was theirs. That was a
// cross-tenant WRITE hole - a competitor's opening hours could be rewritten
// to close their salon, or their holiday closures deleted.
//
// UpdateService/DeleteService/UpdateStore already had correct checks; they're
// covered here too so a future refactor can't quietly drop them.

// storeOwnedByOther builds a mock whose store belongs to a DIFFERENT salon
// than the caller - the exact shape of the attack.
func storeOwnedByOther() (*mockRepo, uuid.UUID, uuid.UUID) {
	storeID := uuid.New()
	victimSalonID := uuid.New()
	attackerSalonID := uuid.New()
	repo := &mockRepo{getStoreByIDStore: &Store{ID: storeID, SalonID: victimSalonID}}
	return repo, storeID, attackerSalonID
}

func TestSetBusinessHours_OtherSalonsStore_Denied(t *testing.T) {
	repo, storeID, attackerSalonID := storeOwnedByOther()
	svc := newTestService(repo)

	err := svc.SetBusinessHours(context.Background(), storeID, attackerSalonID, SetBusinessHoursRequest{
		DayOfWeek: 1, OpenTime: "00:00:00", CloseTime: "00:00:01", IsOpen: true,
	})

	require.Error(t, err, "an artist must not be able to rewrite another salon's opening hours")
}

func TestGetBusinessHours_OtherSalonsStore_Denied(t *testing.T) {
	repo, storeID, attackerSalonID := storeOwnedByOther()
	svc := newTestService(repo)

	_, err := svc.GetBusinessHours(context.Background(), storeID, attackerSalonID)

	require.Error(t, err)
}

func TestCreateException_OtherSalonsStore_Denied(t *testing.T) {
	repo, storeID, attackerSalonID := storeOwnedByOther()
	svc := newTestService(repo)

	err := svc.CreateException(context.Background(), storeID, attackerSalonID, CreateExceptionRequest{
		ExceptionDate: "2027-12-25", IsClosed: true,
	})

	require.Error(t, err)
}

func TestGetExceptions_OtherSalonsStore_Denied(t *testing.T) {
	repo, storeID, attackerSalonID := storeOwnedByOther()
	svc := newTestService(repo)

	_, err := svc.GetExceptions(context.Background(), storeID, attackerSalonID)

	require.Error(t, err)
}

func TestDeleteException_OtherSalonsStore_Denied(t *testing.T) {
	repo, storeID, attackerSalonID := storeOwnedByOther()
	svc := newTestService(repo)

	err := svc.DeleteException(context.Background(), storeID, attackerSalonID, "2027-12-25")

	require.Error(t, err, "an artist must not be able to delete another salon's holiday closure")
}

// The ownership failure must NOT confirm the store exists - a Forbidden
// would tell an attacker their guessed UUID is real. Both the wrong-owner
// and the doesn't-exist case must look identical from outside.
func TestStoreOwnership_WrongOwnerAndMissing_SameErrorCode(t *testing.T) {
	storeID := uuid.New()

	wrongOwner := newTestService(&mockRepo{
		getStoreByIDStore: &Store{ID: storeID, SalonID: uuid.New()},
	})
	missing := newTestService(&mockRepo{getStoreByIDErr: ErrStoreNotFound})

	_, errWrong := wrongOwner.GetBusinessHours(context.Background(), storeID, uuid.New())
	_, errMissing := missing.GetBusinessHours(context.Background(), storeID, uuid.New())

	require.Error(t, errWrong)
	require.Error(t, errMissing)
	assert.Equal(t, errWrong.Error(), errMissing.Error(),
		"wrong-owner and not-found must be indistinguishable, or the API becomes a store-UUID oracle")
}

func TestUpdateService_OtherSalonsService_Denied(t *testing.T) {
	serviceID := uuid.New()
	repo := &mockRepo{
		getServiceByIDSvc: &SalonServiceRecord{ID: serviceID, SalonID: uuid.New()},
	}
	svc := newTestService(repo)

	_, err := svc.UpdateService(context.Background(), serviceID, uuid.New(), UpdateServiceRequest{})

	require.Error(t, err, "an artist must not be able to edit another salon's service")
}

func TestDeleteService_OtherSalonsService_Denied(t *testing.T) {
	serviceID := uuid.New()
	repo := &mockRepo{
		getServiceByIDSvc: &SalonServiceRecord{ID: serviceID, SalonID: uuid.New()},
	}
	svc := newTestService(repo)

	err := svc.DeleteService(context.Background(), serviceID, uuid.New())

	require.Error(t, err)
}

func TestUpdateStore_OtherSalonsStore_Denied(t *testing.T) {
	repo, storeID, attackerSalonID := storeOwnedByOther()
	svc := newTestService(repo)

	_, err := svc.UpdateStore(context.Background(), storeID, attackerSalonID, UpdateStoreRequest{})

	require.Error(t, err)
}

// ── CreateStore tests ────────────────────────────────────────────────────────
//
// The fix for a real gap: the data model and the availability algorithm
// both already supported an artist working at more than one store
// (artist_stores, cross-store travel buffer), but nothing let an artist
// actually create a second store and get assigned to it. These tests lock
// in that the new endpoint does both halves atomically, from the caller's
// own user ID rather than a trusted request field.

func TestCreateStore_Success_AssignsCallingArtist(t *testing.T) {
	profile := defaultArtistProfile()
	salonID := uuid.New()
	repo := &mockRepo{getArtistByUserIDProfile: profile}
	svc := newTestService(repo)

	store, err := svc.CreateStore(context.Background(), profile.UserID, salonID, CreateStoreRequest{
		Name: "Branch B", City: "Sour",
	})

	require.NoError(t, err)
	assert.Equal(t, salonID, store.SalonID, "the new store must belong to the caller's own salon")
	assert.Equal(t, profile.ID, repo.createStoreCalledWithArtistID,
		"the artist assigned to the new store must be resolved from the caller's own session, not trusted from the request")
}

func TestCreateStore_NoArtistProfile_Rejected(t *testing.T) {
	repo := &mockRepo{getArtistByUserIDErr: ErrArtistNotFound}
	svc := newTestService(repo)

	_, err := svc.CreateStore(context.Background(), uuid.New(), uuid.New(), CreateStoreRequest{
		Name: "Branch B", City: "Sour",
	})

	require.Error(t, err)
}

func TestCreateStore_MissingName_Rejected(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{getArtistByUserIDProfile: profile}
	svc := newTestService(repo)

	_, err := svc.CreateStore(context.Background(), profile.UserID, uuid.New(), CreateStoreRequest{
		City: "Sour", // Name omitted
	})

	require.Error(t, err, "a store with no name must be rejected before it reaches the database")
}

func TestCreateStore_MissingCity_Rejected(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{getArtistByUserIDProfile: profile}
	svc := newTestService(repo)

	_, err := svc.CreateStore(context.Background(), profile.UserID, uuid.New(), CreateStoreRequest{
		Name: "Branch B", // City omitted
	})

	require.Error(t, err)
}

func TestCreateStore_RepositoryFailure_Surfaces(t *testing.T) {
	profile := defaultArtistProfile()
	repo := &mockRepo{
		getArtistByUserIDProfile: profile,
		createStoreErr:           assert.AnError,
	}
	svc := newTestService(repo)

	_, err := svc.CreateStore(context.Background(), profile.UserID, uuid.New(), CreateStoreRequest{
		Name: "Branch B", City: "Sour",
	})

	require.Error(t, err)
}
