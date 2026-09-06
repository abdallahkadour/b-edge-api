// slots.go — availability: turning an artist's hours, bookings, buffers and
// travel time into the list of times a customer may pick.
//
// Split out of service.go on 2026-09-06. That file had grown to 2,015 lines
// holding six unrelated responsibilities; this is the one a new contributor
// most often needs to reason about, and it does not belong beside the booking
// lifecycle. Behaviour is unchanged — slots_golden_test.go pins the exact
// output and passes unmodified either side of the move.
//
// The interval algebra itself lives in occupancy.go; this file is the five
// steps that feed it.
package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/billing"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/openinghours"
)

// toDayHours adapts this domain's BusinessHours row onto the shared
// openinghours value type. Returns nil for a nil row - the repository
// returns (nil, nil) when a weekday has no hours configured, which
// openinghours.Resolve reads as closed.
func toDayHours(bh *BusinessHours) *openinghours.DayHours {
	if bh == nil {
		return nil
	}
	return &openinghours.DayHours{
		IsOpen:    bh.IsOpen,
		OpenTime:  bh.OpenTime,
		CloseTime: bh.CloseTime,
	}
}

// toException adapts this domain's BusinessHoursException row onto the
// shared openinghours value type.
func toException(e *BusinessHoursException) *openinghours.Exception {
	if e == nil {
		return nil
	}
	return &openinghours.Exception{
		IsClosed:  e.IsClosed,
		OpenTime:  e.OpenTime,
		CloseTime: e.CloseTime,
	}
}

// SubscriptionStatusReader is the one capability this domain needs from the
// billing domain: given an artist, what is their subscription (if any)?
// Defined here rather than depending on billing.Repository's entire
// interface - this domain reads exactly one thing about billing and has no
// business depending on billing's whole persistence surface (plans,
// invoices, admin overview) just to satisfy a type. *billing.pgRepo (via
// billing.NewRepository) satisfies this structurally with no changes on
// that side.
type SubscriptionStatusReader interface {
	GetSubscriptionByArtistID(ctx context.Context, artistID uuid.UUID) (*billing.Subscription, error)
}

// cancellationWindow is how far before the appointment a customer
// can cancel and receive a full refund.
const cancellationWindow = 24 * time.Hour

// depositDeadlineDefault is the default number of hours before the
// appointment by which the deposit must be paid.
const depositDeadlineDefault = 48 * time.Hour

// depositGraceWindow is the fallback deposit window used when a service's
// configured deposit_deadline_hours doesn't fit before the appointment - a
// pending request that sat unapproved long enough that start_time minus the
// service's deposit window has already passed by the time the artist
// approves it. Rather than storing an instantly-expired deadline (and
// telling the customer they have "24 hours" while the real field already
// lapsed), the customer gets this much time from the moment of approval,
// capped so it never extends past the appointment itself.
const depositGraceWindow = 2 * time.Hour

// defaultPageSize is the number of bookings returned per page.
const defaultPageSize = 20

// weekdays are Mon-Thu. Fri-Sun are weekends for travel buffer purposes.
// In Lebanon, the weekend is Friday-Sunday.
var weekdays = map[time.Weekday]bool{
	time.Monday:    true,
	time.Tuesday:   true,
	time.Wednesday: true,
	time.Thursday:  true,
}

// Service handles all booking business logic.
// It knows nothing about HTTP - no fiber.Ctx, no status codes.
// It knows nothing about SQL - all DB access goes through Repository.
type Service struct {
	repo      Repository
	subReader SubscriptionStatusReader
	validate  *validator.Validate
	log       *zap.Logger
	// discounts is OPTIONAL - nil means promo codes are ignored entirely and
	// every booking prices exactly as it did before the feature existed. That
	// keeps every existing newTestService working untouched and makes the
	// feature impossible to half-wire.
	discounts DiscountResolver
}

// GetAvailableSlots runs the 7-step slot availability algorithm and returns
// a list of valid time windows for the given artist, store, service, and date.
func (s *Service) GetAvailableSlots(ctx context.Context, req GetAvailableSlotsRequest) ([]*TimeSlot, error) {
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

	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_DATE", "Date must be in YYYY-MM-DD format")
	}

	// ── Step 1: Check store is open ───────────────────────────────────────

	store, err := s.repo.GetStore(ctx, storeID)
	if err != nil {
		if errors.Is(err, ErrStoreNotFound) {
			return nil, apperror.NotFound("STORE_NOT_FOUND", "Store not found")
		}
		return nil, fmt.Errorf("get available slots: get store: %w", err)
	}

	// Check for holiday or special hours on this specific date
	exception, err := s.repo.GetBusinessHoursException(ctx, storeID, date)
	if err != nil {
		return nil, fmt.Errorf("get available slots: get exception: %w", err)
	}

	// Get regular hours for this day of week
	bh, err := s.repo.GetBusinessHours(ctx, storeID, int(date.Weekday()))
	if err != nil {
		return nil, fmt.Errorf("get available slots: get hours: %w", err)
	}

	// Resolving the trading window is shared with the public "Open now"
	// badge on the discovery profile - see internal/pkg/openinghours for
	// why it lives outside this domain rather than inline here.
	window, open, err := openinghours.Resolve(store.Timezone, date, toDayHours(bh), toException(exception))
	if err != nil {
		return nil, fmt.Errorf("get available slots: %w", err)
	}
	if !open {
		return []*TimeSlot{}, nil // store is closed this date - return empty
	}
	openTime, closeTime := window.OpenAt, window.CloseAt

	// ── Step 2: Same-day minimum notice ──────────────────────────────────

	storeLoc := openinghours.Location(store.Timezone)
	localDate := openinghours.LocalDate(date, storeLoc)
	earliestStart := openTime
	if openinghours.IsSameDayIn(date, time.Now(), storeLoc) {
		minNotice := time.Now().UTC().Add(time.Duration(store.SameDayNoticeHours) * time.Hour)
		if minNotice.After(earliestStart) {
			earliestStart = minNotice
		}
	}

	// ── Step 3: Get service info ──────────────────────────────────────────

	service, err := s.repo.GetService(ctx, serviceID)
	if err != nil {
		// A service ID that does not resolve is a client error, not a server
		// fault - mirror CreateBooking and HoldGuestSlot rather than falling
		// through to the generic 500 handler.
		return nil, apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
	}

	serviceDuration := time.Duration(service.DurationMin) * time.Minute

	// ── Step 3.5: Lazily release stale holds and deposit-lapsed approvals ──
	//
	// A held booking nobody ever submitted stays in 'held' forever unless
	// something moves it past its 10-minute window - there is no background
	// scheduler running ReleaseExpiredHolds (found live while testing this
	// endpoint: several holds from days earlier were still permanently
	// blocking their slots). The same problem exists for 'approved' bookings
	// whose deposit_deadline lapsed without payment - StatusApproved is in
	// BlockingStatuses too, so an artist who never got paid and never
	// manually cancelled leaves that slot permanently unbookable, forever,
	// exactly like a stale hold. Rather than standing up a real scheduler,
	// both self-heal opportunistically on the read path every availability
	// query already takes: best-effort, since a sweep failure here should
	// never fail the slots request itself - worst case, a stale row keeps
	// blocking for one more request, exactly like before this fix.
	if _, err := s.repo.ReleaseExpiredHolds(ctx); err != nil {
		s.log.Warn("get available slots: release expired holds failed, continuing", zap.Error(err))
	}
	if _, err := s.repo.ExpireDeadlineBookings(ctx); err != nil {
		s.log.Warn("get available slots: expire deadline bookings failed, continuing", zap.Error(err))
	}

	// ── Step 4: Build blocked ranges from existing bookings ───────────────

	existingBookings, err := s.repo.GetArtistBookingsForDate(ctx, artistID, date)
	if err != nil {
		return nil, fmt.Errorf("get available slots: get bookings: %w", err)
	}

	// The artist's occupied time, as a typed interval set rather than a flat
	// list of ranges. See occupancy.go for why kinds exist before anything
	// produces more than one of them.
	//
	// GetArtistBookingsForDate has no store filter, so this already includes
	// bookings at OTHER stores - step 5 adds travel buffers AROUND them, it
	// does not add the bookings themselves. Filtering this query by store
	// would silently stop cross-store bookings blocking their own span.
	occupied := &Occupancy{}
	for _, b := range existingBookings {
		occupied.AddRange(b.StartTime, b.EndTime, KindService)
		// Cleanup after the appointment, as its own typed interval rather
		// than a longer service span. Kept separate because the two mean
		// different things: the customer is present for one and not the
		// other, and only the buffer can be released early. BlockedUntil is
		// the authority - it equals EndTime once a buffer has been handed
		// back, and Add drops the resulting empty interval.
		occupied.AddRange(b.EndTime, b.BlockedUntil, KindBuffer)
	}

	// ── Step 5: Travel buffer for cross-store bookings ────────────────────

	crossStoreBookings, err := s.repo.GetArtistCrossStoreBookings(ctx, artistID, storeID, date)
	if err != nil {
		return nil, fmt.Errorf("get available slots: get cross store bookings: %w", err)
	}

	isWeekend := !weekdays[date.Weekday()]

	for _, csb := range crossStoreBookings {
		bufferMins := store.WeekdayBufferMin
		if isWeekend {
			bufferMins = store.WeekendBufferMin
		}

		// Try to get artist-specific buffer override
		buf, err := s.repo.GetArtistStoreBuffer(ctx, artistID, csb.StoreID, storeID)
		if err != nil {
			return nil, fmt.Errorf("get available slots: get buffer: %w", err)
		}
		if buf != nil {
			bufferMins = buf.WeekdayBufferMin
			if isWeekend {
				bufferMins = buf.WeekendBufferMin
			}
		}

		// Travel time either side: out to the other store, and back.
		//
		// Expressed as KindTravel intervals rather than anonymous ranges.
		// This is the proof the abstraction holds - an already-shipped
		// complication that has to fit without special-casing. A zero
		// buffer produces zero-length intervals, which Add drops, so no
		// caller needs to guard the common case.
		for _, tv := range TravelIntervals(csb.StartTime, csb.EndTime,
			time.Duration(bufferMins)*time.Minute) {
			occupied.Add(tv)
		}
	}

	// ── Step 6: Early bird config ─────────────────────────────────────────

	var earlyBirdCutoff *time.Time
	if store.EarlyBirdCutoff != nil {
		t, err := parseStoreTimeIn(localDate, *store.EarlyBirdCutoff, storeLoc)
		if err == nil {
			earlyBirdCutoff = &t
		}
	}

	// ── Step 7: Generate valid slots ──────────────────────────────────────

	var slots []*TimeSlot
	current := earliestStart

	for current.Add(serviceDuration).Before(closeTime) || current.Add(serviceDuration).Equal(closeTime) {
		slotEnd := current.Add(serviceDuration)

		// The candidate reserves its own cleanup as well. Without this a
		// slot could be offered that leaves no turnaround before the next
		// appointment - the buffer would protect every booking except the
		// one being made, which is the case it exists for.
		candidate := Interval{
			Start: current,
			End:   slotEnd.Add(time.Duration(service.BufferMin) * time.Minute),
			Kind:  KindService,
		}

		// BlocksArtist, not OverlapsAny: the question here is whether the
		// ARTIST can take this slot. The two are identical today because
		// every kind produced blocks her, and they diverge the moment a
		// processing gap exists - time the chair is busy and she is not.
		if !occupied.BlocksArtist(candidate) {
			slot := &TimeSlot{
				StartTime: current,
				EndTime:   slotEnd,
			}

			// Flag early bird
			if earlyBirdCutoff != nil && current.Before(*earlyBirdCutoff) {
				slot.IsEarlyBird = true
				slot.EarlyBirdFee = store.EarlyBirdFee
			}

			slots = append(slots, slot)
		}

		// Advance by 15-minute increments - standard booking granularity
		current = current.Add(15 * time.Minute)
	}

	return slots, nil
}

// isEarlyBirdSlot reports whether startTime falls before the store's
// early-bird cutoff on its own calendar day. Uses the exact same comparison
// GetAvailableSlots uses to flag a slot with the early-bird badge for the
// picker UI - the two call sites must agree, or a slot shown with the badge
// could be booked without the surcharge actually being charged.
//
// The cutoff is a wall-clock LOCAL time in the store's zone ("09:00:00" means
// 9am where the salon physically is). The calendar day is therefore also the
// store's local day, not the UTC day - at 23:00 UTC it is already tomorrow in
// Beirut, and comparing against the wrong day's cutoff is off by 24 hours.
func isEarlyBirdSlot(store *Store, startTime time.Time) bool {
	if store == nil || store.EarlyBirdCutoff == nil {
		return false
	}
	loc := storeLocation(store)
	local := startTime.In(loc)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	cutoff, err := parseStoreTimeIn(date, *store.EarlyBirdCutoff, loc)
	if err != nil {
		return false
	}
	return startTime.Before(cutoff)
}

// storeLocation resolves a store's IANA timezone to a *time.Location,
// tolerating a nil store.
//
// A thin adapter over openinghours.Location, which owns the fallback
// behaviour and documents it.
func storeLocation(store *Store) *time.Location {
	if store == nil {
		return time.UTC
	}
	return openinghours.Location(store.Timezone)
}

// parseStoreTimeIn parses a PostgreSQL TIME string (e.g. "09:00:00") and
// combines it with a date to produce an instant in the given location.
//
// A thin adapter over openinghours.ParseTimeIn, kept so the two remaining
// early-bird call sites read the same as they did before the extraction.
func parseStoreTimeIn(date time.Time, timeStr string, loc *time.Location) (time.Time, error) {
	return openinghours.ParseTimeIn(date, timeStr, loc)
}
