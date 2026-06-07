// Package artist contains unit tests for the artist service layer.
// These tests use mock repositories — no database required.
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
	getArtistByIDProfile     *ArtistProfile
	getArtistByIDErr         error
	getArtistByUserIDProfile *ArtistProfile
	getArtistByUserIDErr     error
	updateArtistProfileErr   error
	getStoresByArtistStores  []*Store
	getStoresByArtistErr     error
	getStoresBySalonStores   []*Store
	getStoresBySalonErr      error
	getServicesBySalonSvcs   []*SalonServiceRecord
	getServicesBySalonErr    error
	getServiceByIDSvc        *SalonServiceRecord
	getServiceByIDErr        error
	createServiceErr         error
	updateServiceErr         error
	deleteServiceErr         error
	getBusinessHoursBH       []*BusinessHours
	getBusinessHoursErr      error
	setBusinessHoursErr      error
	getExceptionsEx          []*BusinessHoursException
	getExceptionsErr         error
	createExceptionErr       error
	deleteExceptionErr       error
}

func (m *mockRepo) GetArtistByID(_ context.Context, _ uuid.UUID) (*ArtistProfile, error) {
	return m.getArtistByIDProfile, m.getArtistByIDErr
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

	// Different user ID — not the owner
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

	// Different salonID — should be forbidden
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
	repo := &mockRepo{setBusinessHoursErr: nil}
	svc := newTestService(repo)

	req := SetBusinessHoursRequest{
		DayOfWeek: 1,
		OpenTime:  "09:00:00",
		CloseTime: "18:00:00",
		IsOpen:    true,
	}

	err := svc.SetBusinessHours(context.Background(), uuid.New(), req)
	require.NoError(t, err)
}

func TestSetBusinessHours_InvalidTimeFormat(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	req := SetBusinessHoursRequest{
		DayOfWeek: 1,
		OpenTime:  "9am", // invalid format
		CloseTime: "6pm", // invalid format
		IsOpen:    true,
	}

	err := svc.SetBusinessHours(context.Background(), uuid.New(), req)
	require.Error(t, err)
}

// ── CreateException tests ─────────────────────────────────────────────────────

func TestCreateException_Success(t *testing.T) {
	repo := &mockRepo{createExceptionErr: nil}
	svc := newTestService(repo)

	req := CreateExceptionRequest{
		ExceptionDate: "2027-12-25",
		IsClosed:      true,
		Reason:        strPtr("Christmas"),
	}

	err := svc.CreateException(context.Background(), uuid.New(), req)
	require.NoError(t, err)
}

func TestCreateException_InvalidDate(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	req := CreateExceptionRequest{
		ExceptionDate: "25-12-2027", // wrong format
		IsClosed:      true,
	}

	err := svc.CreateException(context.Background(), uuid.New(), req)
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
