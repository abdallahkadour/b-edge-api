// Package booking implements the booking domain for B-Edge.
package booking

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/billing"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/subscription"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
	"github.com/abdallahkadour/b-edge-api/internal/promo"
)

// NewService creates a new booking Service.
//
// subReader is required, unlike the variadic logger below - it gates
// whether a new booking is even allowed to be created (see
// checkArtistAcceptsNewBookings), so a nil default here would mean a
// wiring mistake silently disables Phase 4 enforcement rather than failing
// loudly. See B-Edge-Monetization-Implementation-Spec-v1.md section 6.1.
//
// The logger is variadic rather than a required parameter purely to avoid
// churning every existing call site (including every test's
// newTestService) for what is an observability addition. Omitting it
// yields a no-op logger, so behaviour is unchanged for callers that don't
// pass one - but the production wiring in RegisterRoutes does.
// DiscountResolver prices a booking with an optional promo code.
//
// Declared here rather than importing promo.Service, for the same reason
// SubscriptionStatusReader exists: booking states the one method it needs and
// *promo.Service satisfies it structurally. The dependency stays
// one-directional - booking knows about discounts, promo knows nothing about
// bookings.
type DiscountResolver interface {
	Resolve(ctx context.Context, salonID, customerID uuid.UUID, code string,
		base, surcharge, deposit decimal.Decimal) (*promo.Result, error)
	// ReleaseForBooking implements D3.5 - a customer who did not break the
	// booking keeps their code.
	ReleaseForBooking(ctx context.Context, bookingID uuid.UUID) error
	// Preview prices a code without committing to it.
	Preview(ctx context.Context, salonID, customerID uuid.UUID, code string,
		base, surcharge, deposit decimal.Decimal) (*promo.PreviewResponse, error)
}

func NewService(repo Repository, subReader SubscriptionStatusReader, log ...*zap.Logger) *Service {
	l := zap.NewNop()
	if len(log) > 0 && log[0] != nil {
		l = log[0]
	}
	return &Service{
		repo:      repo,
		subReader: subReader,
		validate:  validation.New(),
		log:       l,
	}
}

// discountAmountOf and discountCodeOf read an optional AppliedDiscount into
// the two columns, so the booking literal stays readable instead of carrying
// two nil checks inline.
func discountAmountOf(a *AppliedDiscount) decimal.Decimal {
	if a == nil {
		return zeroDecimal()
	}
	return a.Amount
}

func discountCodeOf(a *AppliedDiscount) *string {
	if a == nil {
		return nil
	}
	code := a.Code
	return &code
}

// PreviewDiscount answers "what would this code do to this booking", without
// committing anything.
//
// Takes a booking rather than a service + slot because the price being
// discounted is already computed and stored on the held row - including the
// early-bird surcharge. Re-deriving it here would be a second implementation
// of the pricing that could disagree with the first.
//
// PUBLIC, because guest holds are. A held guest booking carries
// SystemGuestPlaceholderID, so the per-customer checks (already used,
// first-time-only) cannot be evaluated yet and the preview reports the code's
// face value. Those checks run for real at submit, which is the first moment a
// customer exists - see SubmitGuestBooking.
func (s *Service) PreviewDiscount(ctx context.Context, bookingID uuid.UUID, code string) (*promo.PreviewResponse, error) {
	if s.discounts == nil {
		return &promo.PreviewResponse{Code: code, Valid: false, Reason: "That code isn't valid."}, nil
	}

	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("preview discount: %w", err)
	}

	return s.discounts.Preview(ctx, b.SalonID, b.CustomerID, code,
		b.FinalPrice, decimal.Zero, b.DepositAmount)
}

// WithDiscounts attaches a resolver. Separate from NewService so the dozen
// existing call sites - including every test - keep working untouched.
func (s *Service) WithDiscounts(r DiscountResolver) *Service {
	s.discounts = r
	return s
}

// applyDiscount prices a booking and returns the redemption to write with it.
//
// A REFUSED CODE IS NOT AN ERROR. A customer who mistypes a code still gets
// their booking, at full price, rather than a failure they have to recover
// from mid-checkout. Only an infrastructure failure propagates.
func (s *Service) applyDiscount(
	ctx context.Context,
	salonID, customerID uuid.UUID,
	code *string,
	subtotal, deposit decimal.Decimal,
) (decimal.Decimal, *AppliedDiscount, error) {
	if s.discounts == nil || code == nil || *code == "" {
		return subtotal, nil, nil
	}

	// subtotal already includes the early-bird surcharge, which is why it is
	// passed as the base with a zero surcharge: D3.4 requires the discount to
	// come off the price the customer was shown, and by this point that is
	// exactly what subtotal is.
	res, err := s.discounts.Resolve(ctx, salonID, customerID, *code,
		subtotal, decimal.Zero, deposit)
	if err != nil {
		return subtotal, nil, fmt.Errorf("apply discount: %w", err)
	}
	if !res.Applied() {
		return subtotal, nil, nil
	}

	return res.Breakdown.Final, &AppliedDiscount{
		DiscountID: *res.DiscountID,
		CustomerID: customerID,
		Code:       res.Breakdown.AppliedCode,
		Amount:     res.Breakdown.DiscountTotal,
		FinalPrice: res.Breakdown.Final,
	}, nil
}

// checkArtistAcceptsNewBookings enforces
// B-Edge-Monetization-Implementation-Spec-v1.md section 6.1's graduated
// enforcement: an artist whose subscription has derived status past_due or
// suspended stops receiving NEW bookings, while every existing booking is
// still honored untouched (nothing here ever reads or touches the bookings
// table). An artist with no subscriptions row at all is treated the same
// as past_due, exactly matching billing.DeriveStatus's own
// CurrentPeriodEnd==nil case - this should not happen for any artist
// approved after Aug 29, 2026 (see admin.Service.Approve), but stays
// correct even if it does.
// errBookingNotFound is the single answer to "you may not have this booking",
// whether it does not exist or simply is not yours.
//
// # WHY THE TWO CASES MUST BE INDISTINGUISHABLE
//
// Until 2026-09-05 a foreign booking returned 403 and a nonexistent one 404.
// That difference is an oracle: any artist token could enumerate real booking
// IDs by watching the status code, learning which UUIDs are live without ever
// reading one. Security test AUTH-02 found it; billing invoices already did
// the right thing and were the model.
//
// It is a FUNCTION rather than two matching literals on purpose. The leak was
// possible because the not-found branch and the ownership branch were written
// separately and drifted; sharing one constructor makes drifting them again a
// deliberate act rather than an oversight. For the same reason the message is
// identical too - matching the status while differing in the body would leak
// exactly as much.
func errBookingNotFound() error {
	return apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
}

func (s *Service) checkArtistAcceptsNewBookings(ctx context.Context, artistID uuid.UUID) error {
	sub, err := s.subReader.GetSubscriptionByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, billing.ErrSubscriptionNotFound) {
		return fmt.Errorf("check artist subscription status: %w", err)
	}
	if sub == nil {
		sub = &billing.Subscription{}
	}

	// Reads the shared enforcement ladder rather than naming statuses here.
	// Which states stop new bookings is a policy decision declared once in
	// internal/pkg/subscription; this call site enforces it but does not
	// get to define it.
	if !subscription.Enforce(billing.DeriveStatus(sub, time.Now())).AcceptsNewBookings {
		return apperror.Forbidden("ARTIST_NOT_ACCEPTING_BOOKINGS",
			"This artist isn't accepting new bookings right now")
	}
	return nil
}

// CreateBooking holds a slot and creates a pending booking.
// The GIST constraint is the final atomic guard against double booking.
//
// FIXED: salon_id is now derived from the service (which owns it), not the JWT.
// This ensures authenticated customers can create bookings without a salon_id in their token.
func (s *Service) CreateBooking(ctx context.Context, req CreateBookingRequest, customerID uuid.UUID) (*BookingResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	artistID, err := uuid.Parse(req.ArtistID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_ARTIST_ID", "Invalid artist ID")
	}
	storeID, err := uuid.Parse(req.StoreID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_STORE_ID", "Invalid store ID")
	}
	serviceID, err := uuid.Parse(req.ServiceID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_SERVICE_ID", "Invalid service ID")
	}

	if err := s.checkArtistAcceptsNewBookings(ctx, artistID); err != nil {
		return nil, err
	}

	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_START_TIME", "start_time must be in RFC3339 format e.g. 2026-06-01T10:00:00Z")
	}

	// Fetch service for duration and pricing - and derive salon_id from it
	service, err := s.repo.GetService(ctx, serviceID)
	if err != nil {
		return nil, apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
	}

	store, err := s.repo.GetStore(ctx, storeID)
	if err != nil {
		return nil, fmt.Errorf("create booking: get store: %w", err)
	}

	// Same early-bird surcharge logic as HoldGuestSlot - see isEarlyBirdSlot.
	finalPrice := service.Price
	if isEarlyBirdSlot(store, startTime.UTC()) {
		finalPrice = finalPrice.Add(store.EarlyBirdFee)
	}

	endTime := startTime.Add(time.Duration(service.DurationMin) * time.Minute)
	// The span actually reserved on the calendar: the appointment plus any
	// cleanup. end_time stays the customer-facing finish; blocked_until is
	// what the exclusion constraint ranges over. Both the buffer and the
	// span are snapshotted, so changing the service's buffer later cannot
	// re-plan a booking already agreed. See migration 033.
	blockedUntil := endTime.Add(time.Duration(service.BufferMin) * time.Minute)

	// A logged-in customer is known here, so the code resolves before the row
	// is built. finalPrice already carries the early-bird surcharge - D3.4
	// requires the discount to come off the price the customer was shown.
	finalPrice, applied, err := s.applyDiscount(ctx, service.SalonID, customerID,
		req.DiscountCode, finalPrice, service.DepositAmount)
	if err != nil {
		return nil, err
	}

	// Set held_until - slot is reserved for 10 minutes during checkout
	heldUntil := time.Now().UTC().Add(SlotHoldDuration)

	b := &Booking{
		ID:              uuid.New(),
		SalonID:         service.SalonID, // DERIVED from service, not JWT
		StoreID:         storeID,
		ArtistID:        artistID,
		CustomerID:      customerID,
		ServiceID:       serviceID,
		StartTime:       startTime.UTC(),
		EndTime:         endTime.UTC(),
		BlockedUntil:    blockedUntil.UTC(),
		BufferMin:       service.BufferMin,
		HeldUntil:       &heldUntil,
		Status:          StatusHeld,
		OriginalPrice:   service.Price,
		DiscountAmount:  discountAmountOf(applied),
		DiscountCode:    discountCodeOf(applied),
		FinalPrice:      finalPrice,
		DepositAmount:   service.DepositAmount,
		Channel:         req.Channel,
		SpecialRequests: req.SpecialRequests,
	}

	if err := s.repo.CreateBooking(ctx, b, applied); err != nil {
		if errors.Is(err, ErrDiscountAlreadyRedeemed) {
			return nil, apperror.Conflict("DISCOUNT_ALREADY_USED",
				"You've already used that code. Please remove it and try again.")
		}
		if errors.Is(err, ErrSlotUnavailable) {
			return nil, apperror.Conflict("SLOT_UNAVAILABLE", "This slot was just taken. Please choose another time.")
		}
		return nil, fmt.Errorf("create booking: %w", err)
	}

	return toResponse(b), nil
}

// SubmitBooking transitions a held booking to pending.
// Called when the customer completes and submits the booking form.
func (s *Service) SubmitBooking(ctx context.Context, bookingID uuid.UUID, customerID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("submit booking: get booking: %w", err)
	}

	// Only the customer who created the booking can submit it
	if b.CustomerID != customerID {
		return nil, errBookingNotFound()
	}

	if b.Status != StatusHeld {
		return nil, apperror.Conflict("BOOKING_NOT_HELD", "Only held bookings can be submitted")
	}

	if err := s.repo.UpdateBookingStatus(ctx, bookingID, StatusPending); err != nil {
		return nil, fmt.Errorf("submit booking: update status: %w", err)
	}

	b.Status = StatusPending
	return toResponse(b), nil
}

// GetBookingByID returns a booking by ID.
// Validates that the requesting user is the customer, artist, or admin.
func (s *Service) GetBookingByID(ctx context.Context, bookingID uuid.UUID, requesterID uuid.UUID, requesterRole string) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("get booking by id: %w", err)
	}

	// Admins can see any booking.
	// Artists and customers can only see bookings they are part of.
	if requesterRole != "admin" && b.CustomerID != requesterID && b.ArtistID != requesterID {
		return nil, errBookingNotFound()
	}

	return toResponse(b), nil
}

// GetBookingsByArtist returns paginated bookings for an artist.
func (s *Service) GetBookingsByArtist(ctx context.Context, artistID uuid.UUID, cursor time.Time, limit int) ([]*BookingResponse, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	bookings, err := s.repo.GetBookingsByArtist(ctx, artistID, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("get bookings by artist: %w", err)
	}

	hasMore := len(bookings) > limit
	if hasMore {
		bookings = bookings[:limit]
	}

	// Non-nil so an empty result marshals as [] rather than null. A nil Go
	// slice becomes JSON null, which an @for in a template cannot iterate -
	// and only ApiService.getArray coalesces it away on the client.
	result := make([]*BookingResponse, 0)
	for _, b := range bookings {
		result = append(result, toResponse(b))
	}

	return result, hasMore, nil
}

// GetBookingsByCustomer returns paginated bookings for a customer.
func (s *Service) GetBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*BookingResponse, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	bookings, err := s.repo.GetBookingsByCustomer(ctx, customerID, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("get bookings by customer: %w", err)
	}

	hasMore := len(bookings) > limit
	if hasMore {
		bookings = bookings[:limit]
	}

	// Non-nil so an empty result marshals as [] rather than null. A nil Go
	// slice becomes JSON null, which an @for in a template cannot iterate -
	// and only ApiService.getArray coalesces it away on the client.
	result := make([]*BookingResponse, 0)
	for _, b := range bookings {
		result = append(result, toResponse(b))
	}

	return result, hasMore, nil
}

// GetEnrichedBookingByID returns one booking with joined display names.
// Access: admin, or the customer/artist on the booking.
func (s *Service) GetEnrichedBookingByID(ctx context.Context, bookingID uuid.UUID, requesterID uuid.UUID, requesterRole string) (*EnrichedBookingResponse, error) {
	e, err := s.repo.GetEnrichedBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("get enriched booking by id: %w", err)
	}

	// e.ArtistID is artists.id; requesterID is users.id - DIFFERENT ID
	// spaces. Comparing them directly (as this did) can never be true, so
	// an artist was silently 403'd on their own booking. Fails closed, so
	// it was a functional bug rather than a hole, but it's the same
	// artists.id-vs-users.id confusion already fixed in six other booking
	// methods; this occurrence was missed. Resolve first, compare like
	// with like.
	isArtist := false
	if requesterRole == RoleArtist {
		requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterID)
		if err != nil && !errors.Is(err, ErrArtistNotFound) {
			return nil, fmt.Errorf("get enriched booking by id: resolve artist: %w", err)
		}
		isArtist = err == nil && e.ArtistID == requesterArtistID
	}

	if requesterRole != RoleAdmin && e.CustomerID != requesterID && !isArtist {
		return nil, errBookingNotFound()
	}

	return toEnrichedResponse(e), nil
}

// ListEnrichedBookingsByArtist returns an artist's bookings with display names.
// If status is non-empty it must be a known booking status; results are then
// restricted to that status (dashboard tabs, deposit queue, refund queue).
func (s *Service) ListEnrichedBookingsByArtist(ctx context.Context, artistID uuid.UUID, requesterUserID uuid.UUID, requesterRole string, status string, cursor time.Time, limit int) ([]*EnrichedBookingResponse, bool, error) {
	// Ownership first, before any work - see assertArtistAccess. Without
	// this, any registered artist could read any other artist's entire
	// booking book (customer names, phone numbers, prices, deposits).
	if err := s.assertArtistAccess(ctx, artistID, requesterUserID, requesterRole); err != nil {
		return nil, false, err
	}

	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	// Reject unknown status values with a clear error rather than silently
	// returning an empty list (which would look like "no bookings" to the UI).
	if status != "" && !ValidBookingStatuses[status] {
		return nil, false, apperror.BadRequest("INVALID_STATUS", "Unknown booking status filter")
	}

	// Lazily expire this artist's own stale pending requests before reading -
	// same self-healing shape as GetAvailableSlots' ReleaseExpiredHolds call
	// above, best-effort so a sweep failure never fails the list itself.
	if _, err := s.repo.ExpireStalePendingBookings(ctx, artistID); err != nil {
		s.log.Warn("list bookings by artist: expire stale pending failed, continuing", zap.Error(err))
	}

	rows, err := s.repo.ListEnrichedBookingsByArtist(ctx, artistID, status, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("list enriched bookings by artist: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	result := make([]*EnrichedBookingResponse, 0, len(rows))
	for _, e := range rows {
		result = append(result, toEnrichedResponse(e))
	}
	return result, hasMore, nil
}

// ListEnrichedBookingsForWeek returns the artist's committed appointments for the
// 7-day window beginning at weekStart (calendar grid). No pagination - the whole
// week is returned, ordered by start time.
func (s *Service) ListEnrichedBookingsForWeek(ctx context.Context, artistID uuid.UUID, requesterUserID uuid.UUID, requesterRole string, weekStart time.Time) ([]*EnrichedBookingResponse, error) {
	// Ownership first - see assertArtistAccess. Without this, any registered
	// artist could read any other artist's full weekly schedule.
	if err := s.assertArtistAccess(ctx, artistID, requesterUserID, requesterRole); err != nil {
		return nil, err
	}

	// Normalise to the start of the day in UTC so the half-open window aligns to
	// midnight boundaries regardless of any time component the client sent.
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, time.UTC)

	rows, err := s.repo.ListEnrichedBookingsForWeek(ctx, artistID, weekStart)
	if err != nil {
		return nil, fmt.Errorf("list enriched bookings for week: %w", err)
	}

	result := make([]*EnrichedBookingResponse, 0, len(rows))
	for _, e := range rows {
		result = append(result, toEnrichedResponse(e))
	}
	return result, nil
}

// ListEnrichedBookingsByCustomer returns a customer's bookings with display names.
func (s *Service) ListEnrichedBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*EnrichedBookingResponse, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	rows, err := s.repo.ListEnrichedBookingsByCustomer(ctx, customerID, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("list enriched bookings by customer: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	result := make([]*EnrichedBookingResponse, 0, len(rows))
	for _, e := range rows {
		result = append(result, toEnrichedResponse(e))
	}
	return result, hasMore, nil
}

// ApproveBooking transitions a pending booking to approved.
// Sets the deposit deadline based on the service configuration.
// ApproveBooking transitions a pending booking to approved and sets the deposit deadline.
// Only the artist can approve a booking.
func (s *Service) ApproveBooking(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("approve booking: get booking: %w", err)
	}

	// Resolve the JWT user_id to the caller's artists.id. bookings.artist_id
	// references artists.id, so we must compare like with like.
	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("approve booking: resolve artist: %w", err)
	}

	// Only the artist on the booking can approve it
	if b.ArtistID != requesterArtistID {
		return nil, errBookingNotFound()
	}

	if b.Status != StatusPending {
		return nil, apperror.Conflict("BOOKING_NOT_PENDING", "Only pending bookings can be approved")
	}

	// A pending request nobody acted on before its own appointment time
	// isn't approvable anymore - there's no slot left to honor, and
	// approving it would compute a deposit deadline that's also already
	// in the past (see depositDeadline below) and send the customer a
	// WhatsApp message asking them to pay for an appointment that
	// already happened. There's currently no background job that
	// auto-expires stale pending bookings, so this guard is the only
	// thing standing between "sat too long" and a nonsensical approval.
	if b.StartTime.Before(time.Now()) {
		return nil, apperror.Conflict("BOOKING_TIME_PASSED", "This booking's appointment time has already passed and can no longer be approved")
	}

	// Fetch service to get deposit deadline hours
	service, err := s.repo.GetService(ctx, b.ServiceID)
	if err != nil {
		return nil, fmt.Errorf("approve booking: get service: %w", err)
	}

	deadlineHours := time.Duration(service.DepositDeadlineHours) * time.Hour
	if deadlineHours == 0 {
		deadlineHours = depositDeadlineDefault
	}

	depositDeadline := b.StartTime.Add(-deadlineHours)

	// The configured window doesn't fit before the appointment (the request
	// sat unapproved too long, or the appointment is simply sooner than the
	// service's usual deposit window) - fall back to a short grace period
	// from right now instead of storing a deadline that's already passed,
	// capped so it never extends past the appointment itself.
	now := time.Now()
	if depositDeadline.Before(now) {
		depositDeadline = now.Add(depositGraceWindow)
		if depositDeadline.After(b.StartTime) {
			depositDeadline = b.StartTime
		}
	}

	calendarToken, err := s.repo.ApproveBooking(ctx, bookingID, depositDeadline)
	if err != nil {
		if errors.Is(err, ErrBookingNotPending) {
			return nil, apperror.Conflict("BOOKING_NOT_PENDING", "Only pending bookings can be approved")
		}
		return nil, fmt.Errorf("approve booking: %w", err)
	}

	b.Status = StatusApproved
	b.DepositDeadline = &depositDeadline
	if calendarToken != "" {
		b.CalendarToken = &calendarToken
	}

	customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID)
	if ctxErr == nil {
		var message string
		if service.DepositAmount.IsPositive() {
			// Report the real time left until depositDeadline, not the
			// service's nominal window - when the grace-window fallback
			// above kicked in, deadlineHours would otherwise claim "24
			// hours" while the customer actually has 2 or less.
			hoursRemaining := max(int(math.Ceil(depositDeadline.Sub(now).Hours())), 1)
			message = fmt.Sprintf(
				"Hi %s! Your %s request for %s has been approved. Please send a $%s deposit within %d hours to confirm your spot.",
				customerName, serviceName, notificationTimeLabel(b.StartTime),
				service.DepositAmount.String(), hoursRemaining,
			)
		} else {
			// No deposit required, but this booking still isn't
			// StatusConfirmed yet - the artist confirms it as a separate
			// action even for $0-deposit services (see ConfirmDepositReceived).
			// Claiming "confirmed" here would say something false over
			// WhatsApp about the booking's actual state.
			message = fmt.Sprintf(
				"Hi %s! Your %s request for %s has been approved. You'll get a final confirmation shortly.",
				customerName, serviceName, notificationTimeLabel(b.StartTime),
			)
		}
		s.enqueueNotification(ctx, bookingID, b.CustomerID, "booking_approved", message)
	}

	return toResponse(b), nil
}

// ConfirmDeposit marks a deposit as received and confirms the booking.
// Only the artist can confirm a deposit.
func (s *Service) ConfirmDeposit(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("confirm deposit: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("confirm deposit: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
		return nil, errBookingNotFound()
	}

	if b.Status != StatusDepositPaid {
		return nil, apperror.Conflict("BOOKING_NOT_DEPOSIT_PAID", "Booking must be in deposit_paid status to confirm")
	}

	if err := s.repo.ConfirmDeposit(ctx, bookingID); err != nil {
		if errors.Is(err, ErrBookingNotApproved) {
			return nil, apperror.Conflict("BOOKING_NOT_DEPOSIT_PAID", "Booking must be in deposit_paid status to confirm")
		}
		return nil, fmt.Errorf("confirm deposit: %w", err)
	}

	b.Status = StatusConfirmed

	// This route used to announce NOTHING. Both ConfirmDeposit and
	// ConfirmDepositReceived reach `confirmed`, but only the latter told
	// the customer - so whether they learned their booking was confirmed,
	// and (since the calendar link rides on the same message) whether they
	// could add it to their calendar, depended on which button the artist
	// pressed. The two-step route is the partial-payment path, which is
	// still a real confirmation the moment it completes.
	//
	// Found by enumerating the state machine, not by a test. See
	// B-Edge-Booking-State-Machine-Matrix-v1.md section 5.1.
	if customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID); ctxErr == nil {
		s.announceConfirmed(ctx, b, customerName, serviceName)
	}

	return toResponse(b), nil
}

// MarkDepositReceived transitions approved → deposit_paid.
// Called by the artist after verifying the Wish Money transfer.
func (s *Service) MarkDepositReceived(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("mark deposit received: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("mark deposit received: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
		return nil, errBookingNotFound()
	}

	if b.Status != StatusApproved {
		return nil, apperror.Conflict("BOOKING_NOT_APPROVED", "Only approved bookings can have deposit marked as received")
	}

	if err := s.repo.UpdateBookingStatus(ctx, bookingID, StatusDepositPaid); err != nil {
		return nil, fmt.Errorf("mark deposit received: %w", err)
	}

	b.Status = StatusDepositPaid
	return toResponse(b), nil
}

// ConfirmDepositReceived is the primary artist-facing deposit action: she
// checks her OMT/Wish transfer and confirms the moment it lands, so this
// moves a booking straight from approved to confirmed in one call, stamping
// deposit_paid_at along the way. The separate MarkDepositReceived / ConfirmDeposit
// pair remains available for edge cases (partial payment, disputed transfer)
// where the two steps genuinely need to happen apart.
//
// reference is an optional artist-entered note (e.g. an OMT/Wish transaction
// code) for her own reconciliation - never shown to the customer, never
// validated beyond a sanity length cap.
func (s *Service) ConfirmDepositReceived(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID, reference *string) (*BookingResponse, error) {
	if reference != nil && len(*reference) > 255 {
		return nil, apperror.BadRequest("REFERENCE_TOO_LONG", "Reference note must be 255 characters or fewer")
	}

	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("confirm deposit received: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("confirm deposit received: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
		return nil, errBookingNotFound()
	}

	if b.Status != StatusApproved {
		return nil, apperror.Conflict("BOOKING_NOT_APPROVED", "Only approved bookings can have a deposit confirmed")
	}

	if err := s.repo.ConfirmDepositReceived(ctx, bookingID, reference); err != nil {
		if errors.Is(err, ErrBookingNotApproved) {
			return nil, apperror.Conflict("BOOKING_NOT_APPROVED", "Only approved bookings can have a deposit confirmed")
		}
		return nil, fmt.Errorf("confirm deposit received: %w", err)
	}

	now := time.Now().UTC()
	b.Status = StatusConfirmed
	b.DepositPaidAt = &now
	if reference != nil {
		b.DepositReference = reference
	}

	customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID)
	if ctxErr == nil {
		s.announceConfirmed(ctx, b, customerName, serviceName)
	}

	return toResponse(b), nil
}

// MarkRefunded records that the artist has paid an owed refund.
//
// Closes a loop that previously had no end. CancelBooking(refundDue=true)
// wrote `refund_due`, and nothing could ever transition out of it: the
// `refunded` status existed in the schema but was written by nothing, and
// CancelBooking's own guard excluded refund_due. The artist had no way to
// say "I sent the money", so the notification centre's refund_due alert was
// unresolvable by construction. See
// B-Edge-Booking-State-Machine-Matrix-v1.md section 5.2.
//
// Deliberately no customer notification. The customer is told about the
// cancellation when it happens; a second message announcing an out-of-band
// bank transfer they either have or have not received would raise more
// questions than it answers.
func (s *Service) MarkRefunded(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID, reference *string) (*BookingResponse, error) {
	if reference != nil && len(*reference) > 255 {
		return nil, apperror.BadRequest("REFERENCE_TOO_LONG", "Reference note must be 255 characters or fewer")
	}

	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("mark refunded: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("mark refunded: resolve artist: %w", err)
	}
	if b.ArtistID != requesterArtistID {
		return nil, errBookingNotFound()
	}

	if b.Status != StatusRefundDue {
		return nil, apperror.Conflict("BOOKING_NOT_REFUND_DUE", "Only bookings with a refund outstanding can be marked refunded")
	}

	if err := s.repo.MarkRefunded(ctx, bookingID, reference); err != nil {
		if errors.Is(err, ErrBookingNotRefundDue) {
			return nil, apperror.Conflict("BOOKING_NOT_REFUND_DUE", "Only bookings with a refund outstanding can be marked refunded")
		}
		return nil, fmt.Errorf("mark refunded: %w", err)
	}

	b.Status = StatusRefunded
	if reference != nil {
		b.DepositReference = reference
	}
	return toResponse(b), nil
}

// CancelBooking cancels a booking.
// Enforces the 24-hour cancellation policy for customers.
// Artists can always cancel but trigger a refund_due.
func (s *Service) CancelBooking(ctx context.Context, bookingID uuid.UUID, requesterID uuid.UUID, requesterRole string, req CancelBookingRequest) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("cancel booking: get booking: %w", err)
	}

	// Determine if requester is the customer or artist on this booking.
	// b.CustomerID is genuinely users.id, so that comparison is direct.
	// b.ArtistID is artists.id, so an "artist" requester must be resolved
	// from their JWT user_id first before comparing like with like.
	isCustomer := b.CustomerID == requesterID
	isAdmin := requesterRole == "admin"

	isArtist := false
	if requesterRole == "artist" {
		requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterID)
		if err != nil && !errors.Is(err, ErrArtistNotFound) {
			return nil, fmt.Errorf("cancel booking: resolve artist: %w", err)
		}
		isArtist = err == nil && b.ArtistID == requesterArtistID
	}

	if !isCustomer && !isArtist && !isAdmin {
		return nil, errBookingNotFound()
	}

	// A confirmed booking whose appointment has already started cannot be
	// cancelled. Past that moment the honest outcomes are `completed` or
	// `no_show`, and both already guard on the same boundary from the other
	// side - cancel was the only one of the three that did not care.
	// Recording an appointment that happened as "cancelled" also
	// misreports earnings.
	//
	// Scoped to `confirmed` DELIBERATELY, not applied to every status.
	// Working through the consequences of a blanket guard: `pending` has no
	// expiry sweep at all (only `held` and `approved` do), and cannot be
	// approved once its start time passes - so blocking cancel there would
	// make a stale pending booking permanently unresolvable. Cancel is its
	// only disposal. Same for `deposit_paid`, which no sweep covers either.
	// See B-Edge-Booking-State-Machine-Matrix-v1.md section 4.4.
	if b.Status == StatusConfirmed && b.StartTime.Before(time.Now()) {
		return nil, apperror.Conflict("BOOKING_ALREADY_STARTED",
			"This appointment has already started. Mark it completed or a no-show instead of cancelling.")
	}

	// Determine if a refund is due, and - separately - whether the customer
	// keeps their promo code.
	//
	// These two look identical and are NOT. refundDue additionally requires a
	// positive deposit, because with nothing paid there is nothing to refund.
	// D3.5's rule is about blame, not money: "a customer is not penalised for
	// a booking they did not break". An artist cancelling a no-deposit booking
	// owes no refund and must still hand the code back.
	refundDue := false
	blameless := false

	if isArtist || isAdmin {
		// Artist cancelling always triggers a refund
		refundDue = b.DepositAmount.IsPositive()
		blameless = true
	} else if isCustomer {
		// Customer cancelling: refund only if >24h before appointment
		timeUntilAppointment := time.Until(b.StartTime)
		if timeUntilAppointment > cancellationWindow {
			refundDue = b.DepositAmount.IsPositive()
			blameless = true
		}
	}

	reason := ""
	if req.Reason != nil {
		reason = *req.Reason
	}

	if err := s.repo.CancelBooking(ctx, bookingID, reason, refundDue); err != nil {
		if errors.Is(err, ErrBookingNotCancellable) {
			return nil, apperror.Conflict("BOOKING_NOT_CANCELLABLE", "This booking cannot be cancelled in its current status")
		}
		return nil, fmt.Errorf("cancel booking: %w", err)
	}

	if refundDue {
		b.Status = StatusRefundDue
	} else {
		b.Status = StatusCancelled
	}

	// D3.5: hand the code back when the customer did not break the booking.
	//
	// Deliberately AFTER the cancel has committed and deliberately not fatal.
	// The booking is already cancelled; failing the whole call because a
	// promo row could not be updated would tell the customer their
	// cancellation failed when it did not. A code that stays consumed is a
	// small, recoverable unfairness - a cancellation that appears to have
	// failed is not.
	if blameless && s.discounts != nil && b.DiscountCode != nil {
		if err := s.discounts.ReleaseForBooking(ctx, bookingID); err != nil {
			s.log.Error("could not release discount redemption",
				zap.String("booking_id", bookingID.String()),
				zap.Error(err))
		}
	}

	// Only notify if the ARTIST cancelled - a customer who just cancelled
	// their own booking doesn't need to be told about it.
	if isArtist || isAdmin {
		customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID)
		if ctxErr == nil {
			message := fmt.Sprintf(
				"Hi %s, your %s booking for %s has been cancelled.",
				customerName, serviceName, notificationTimeLabel(b.StartTime),
			)
			if refundDue {
				message += " Your deposit will be refunded."
			}
			if reason != "" {
				message += " Reason: " + reason
			}
			s.enqueueNotification(ctx, bookingID, b.CustomerID, "booking_cancelled", message)
		}
	}

	s.cascadeWaitlist(ctx, b, "cancelled")

	return toResponse(b), nil
}

// GetWaitlistByArtist returns an artist's active waitlist queue. Bearer
// the requester must resolve to this artist (or admin).
// assertArtistAccess verifies the caller is genuinely the artist whose data
// they're asking for. This is the ownership check that must guard EVERY
// endpoint taking an artist_id from the URL - RequireRole("artist","admin")
// only proves the caller is *an* artist, never that they are *that* artist.
// Its absence on the bookings-list and calendar endpoints was a real
// cross-tenant data leak (any registered artist could read any other
// artist's full booking book, customer phone numbers included).
//
// Admins are allowed through before the artist lookup, deliberately: an
// admin has no `artists` row at all, so resolving one would fail with
// ErrArtistNotFound and lock admins out of the very endpoints their role
// is supposed to reach.
//
// Wrong-artist and no-artist-row both return the same generic 403 - the
// response must not reveal whether the requested artist_id exists.
func (s *Service) assertArtistAccess(ctx context.Context, artistID, requesterUserID uuid.UUID, requesterRole string) error {
	if requesterRole == RoleAdmin {
		return nil
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return apperror.Forbidden("FORBIDDEN", "You do not have permission to view this artist's data")
		}
		return fmt.Errorf("assert artist access: resolve requester: %w", err)
	}
	if requesterArtistID != artistID {
		return apperror.Forbidden("FORBIDDEN", "You do not have permission to view this artist's data")
	}
	return nil
}

// CompleteBooking marks a confirmed booking as completed, generating the
// guest review-link token and enqueueing the review-request notification.
func (s *Service) CompleteBooking(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("complete booking: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("complete booking: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
		return nil, errBookingNotFound()
	}

	if b.Status != StatusConfirmed {
		return nil, apperror.Conflict("BOOKING_NOT_CONFIRMED", "Only confirmed bookings can be marked as completed")
	}

	// Mirrors ApproveBooking's and MarkNoShow's own start_time guard: a
	// service can't be "completed" before it has even started - without
	// this, an artist could mark a days-away booking complete and trigger
	// a review-request WhatsApp message for a service the customer hasn't
	// received yet.
	if b.StartTime.After(time.Now()) {
		return nil, apperror.Conflict("BOOKING_NOT_STARTED", "This booking's appointment time hasn't arrived yet, so it can't be marked as completed")
	}

	reviewToken, err := s.repo.CompleteBooking(ctx, bookingID)
	if err != nil {
		return nil, fmt.Errorf("complete booking: %w", err)
	}

	b.Status = StatusCompleted
	b.ReviewToken = &reviewToken

	customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID)
	if ctxErr == nil {
		reviewURL := fmt.Sprintf("%s/review/%s", customerPWAURL, reviewToken)
		message := fmt.Sprintf(
			"Hi %s! Thanks for booking your %s with us. We'd love to hear how it went - leave a quick review here: %s",
			customerName, serviceName, reviewURL,
		)
		s.enqueueNotification(ctx, bookingID, b.CustomerID, "review_request", message)
	}

	// Completing early hands the unused cleanup back (migration 033), which
	// reopens real time on the calendar. Fires unconditionally rather than
	// only when the release was non-zero: deciding that here would mean
	// re-deriving what the UPDATE actually did, and a cascade that finds
	// nobody waiting is a cheap no-op.
	s.cascadeWaitlist(ctx, b, "completed_early")

	return toResponse(b), nil
}

// MarkNoShow marks a confirmed booking as no_show.
// Only the artist can mark a no-show.
func (s *Service) MarkNoShow(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("mark no show: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("mark no show: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
		return nil, errBookingNotFound()
	}

	if b.Status != StatusConfirmed {
		return nil, apperror.Conflict("BOOKING_NOT_CONFIRMED", "Only confirmed bookings can be marked as no-show")
	}

	// Mirrors ApproveBooking's own start_time guard: a customer can't be a
	// "no-show" for an appointment that hasn't happened yet - without this,
	// an artist could mark a booking no-show days in advance, before the
	// customer ever had the chance to show up.
	if b.StartTime.After(time.Now()) {
		return nil, apperror.Conflict("BOOKING_NOT_STARTED", "This booking's appointment time hasn't arrived yet, so it can't be marked as no-show")
	}

	if err := s.repo.MarkNoShow(ctx, bookingID); err != nil {
		return nil, fmt.Errorf("mark no show: %w", err)
	}

	b.Status = StatusNoShow
	// A no-show frees the slot: `no_show` is not a blocking status, so the
	// time is immediately bookable again. It was the largest of the three
	// silent gaps - the customer who did not turn up is exactly the case a
	// waitlist exists to backfill.
	s.cascadeWaitlist(ctx, b, "no_show")

	return toResponse(b), nil
}

// announceConfirmed tells the customer their booking is confirmed.
//
// Shared by BOTH routes to `confirmed` on purpose. Attaching it to one call
// site is exactly how it came to fire on ConfirmDepositReceived and not on
// ConfirmDeposit; anything wired to "the booking became confirmed" belongs
// here rather than at a caller.
//
// A booking whose appointment has already passed is confirmed silently. The
// bookkeeping is legitimate - an artist reconciling a bank transfer that
// landed late still needs to record it - but "You're all confirmed for last
// Tuesday. See you then!" is not, and neither is a calendar link for a date
// in the past. Mirrors ApproveBooking's BOOKING_TIME_PASSED reasoning, which
// refuses the same nonsense one step earlier.
func (s *Service) announceConfirmed(ctx context.Context, b *Booking, customerName, serviceName string) {
	if b.StartTime.Before(time.Now()) {
		return
	}

	message := fmt.Sprintf(
		"Hi %s! You're all confirmed for %s on %s. See you then!",
		customerName, serviceName, notificationTimeLabel(b.StartTime),
	)
	// The calendar link rides on the CONFIRMED message, never the approved
	// one. An approved booking whose deposit never arrives expires, and
	// putting that in someone's calendar first would leave a ghost
	// appointment they then have to clear themselves.
	if b.CalendarToken != nil && *b.CalendarToken != "" {
		message += fmt.Sprintf(" Add it to your calendar: %s/c/%s", apiPublicURL, *b.CalendarToken)
	}
	s.enqueueNotification(ctx, b.ID, b.CustomerID, "booking_confirmed", message)
}

// enqueueNotification is a best-effort wrapper around
// repo.EnqueueNotification: a queuing failure must never fail the booking
// operation that already succeeded (the artist approved the booking; that's
// done regardless of whether a WhatsApp message got queued for it). Service
// has no logger yet - swallowing the error here rather than silently
// dropping it entirely still isn't ideal, but is the honest state of things
// until a logger is threaded through this layer. Tracked, not hidden.
func (s *Service) enqueueNotification(ctx context.Context, bookingID, userID uuid.UUID, templateName, message string) {
	// Still best-effort - a queuing failure must never fail the booking
	// operation that already succeeded. But it is now LOGGED: previously a
	// persistently broken notification queue was completely invisible,
	// which is how "the customer never got their WhatsApp" becomes an
	// unexplainable support ticket instead of an alert.
	if err := s.repo.EnqueueNotification(ctx, &bookingID, userID, templateName, message); err != nil {
		s.log.Error("failed to enqueue notification - booking succeeded, message will not be sent",
			zap.Error(err),
			zap.String("booking_id", bookingID.String()),
			zap.String("user_id", userID.String()),
			zap.String("template", templateName),
		)
	}
}

// notificationTimeLabel formats a booking's start time for a WhatsApp
// message in Beirut local time - matching every other customer-facing time
// display in the app. Hardcoded for the same reason as businessLocation in
// the earnings domain: every store is currently in Lebanon.
func notificationTimeLabel(startTime time.Time) string {
	loc, err := time.LoadLocation("Asia/Beirut")
	if err != nil {
		loc = time.UTC
	}
	return startTime.In(loc).Format("Mon, 2 Jan · 3:04 PM")
}

// zeroDecimal returns a zero decimal value.
func zeroDecimal() decimal.Decimal {
	return decimal.NewFromInt(0)
}

// mapValidationError converts go-playground/validator errors
// into structured apperror types.
func mapValidationError(err error) error {
	return validation.MapError(err)
}
