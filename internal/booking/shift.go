package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/openinghours"
)

// This file holds the pure evaluation half of bulk schedule shifting: given
// a day's bookings and the constraints around them, decide what would move
// and what would break.
//
// Kept free of the repository and the clock on purpose. Every rule here is
// a judgement about someone's working day - "this would run past closing",
// "you would not make it between branches" - and each deserves a test that
// does not need a database to express.

// shiftInput is everything evaluateShift needs, gathered by the caller.
type shiftInput struct {
	// SameStore are the artist's bookings at the store being shifted.
	SameStore []*EnrichedBooking
	// CrossStore are the artist's bookings at OTHER stores that day. They
	// do not move, but they constrain how far these ones can.
	CrossStore []*EnrichedBooking
	// OpenAt/CloseAt bound the trading day. StoreOpen false means the store
	// does not trade that date at all.
	OpenAt    time.Time
	CloseAt   time.Time
	StoreOpen bool
	// BufferFor returns the required travel minutes between a booking at
	// otherStoreID and one at the store being shifted. Injected as a
	// function because the real lookup is per-pair and hits the database.
	BufferFor func(otherStoreID uuid.UUID) int
	// ShiftMinutes may be negative.
	ShiftMinutes int
	// Now is passed rather than read so tests are deterministic.
	Now time.Time
}

// evaluateShift performs the dry run.
//
// Returns every booking that would move, everything deliberately skipped,
// and every reason the shift cannot proceed. It never stops at the first
// blocker: an artist adjusting a schedule needs the whole list, not one
// problem at a time.
func evaluateShift(in shiftInput) *ShiftPreviewResponse {
	delta := time.Duration(in.ShiftMinutes) * time.Minute

	resp := &ShiftPreviewResponse{
		ShiftMinutes: in.ShiftMinutes,
		Movable:      []ShiftPreviewItem{},
		Skipped:      []ShiftSkipped{},
		Blockers:     []ShiftBlocker{},
	}

	// A store that does not trade that day blocks outright. Shifting
	// bookings around inside a closed day is not a meaningful operation,
	// and the times to validate against do not exist.
	if !in.StoreOpen {
		resp.Blockers = append(resp.Blockers, ShiftBlocker{
			Reason: BlockerStoreClosed,
			Detail: "The store is not open on this date, so there are no opening hours to shift within.",
		})
		return resp
	}

	var moving []*EnrichedBooking
	for _, b := range in.SameStore {
		if !isMovable(b.Status) {
			resp.Skipped = append(resp.Skipped, ShiftSkipped{
				BookingID: b.ID, Reason: SkipTerminalStatus, Status: b.Status,
			})
			continue
		}
		// A booking already under way is out of scope regardless of the
		// direction of the shift.
		if !b.StartTime.After(in.Now) {
			resp.Skipped = append(resp.Skipped, ShiftSkipped{
				BookingID: b.ID, Reason: SkipInProgress, Status: b.Status,
			})
			continue
		}
		moving = append(moving, b)
	}

	for _, b := range moving {
		newStart := b.StartTime.Add(delta)
		newEnd := b.EndTime.Add(delta)

		if newEnd.After(in.CloseAt) {
			id := b.ID
			resp.Blockers = append(resp.Blockers, ShiftBlocker{
				Reason: BlockerPastClosing, BookingID: &id,
				Detail: fmt.Sprintf("%s would finish at %s, after the store closes at %s.",
					serviceLabel(b), localHM(newEnd, in.CloseAt), localHM(in.CloseAt, in.CloseAt)),
			})
		}
		if newStart.Before(in.OpenAt) {
			id := b.ID
			resp.Blockers = append(resp.Blockers, ShiftBlocker{
				Reason: BlockerBeforeOpening, BookingID: &id,
				Detail: fmt.Sprintf("%s would start at %s, before the store opens at %s.",
					serviceLabel(b), localHM(newStart, in.OpenAt), localHM(in.OpenAt, in.OpenAt)),
			})
		}
		// Only meaningful for a negative shift, but checked unconditionally
		// so a clock skew or a stale request cannot slip a booking into the
		// past unnoticed.
		if !newStart.After(in.Now) {
			id := b.ID
			resp.Blockers = append(resp.Blockers, ShiftBlocker{
				Reason: BlockerIntoPast, BookingID: &id,
				Detail: fmt.Sprintf("%s would move to a time that has already passed.", serviceLabel(b)),
			})
		}

		// Travel buffers. The artist must be able to physically get between
		// branches; a shift that erases that time produces a schedule that
		// looks fine and cannot be worked.
		for _, other := range in.CrossStore {
			if !isMovable(other.Status) {
				continue
			}
			need := time.Duration(in.BufferFor(other.StoreID)) * time.Minute
			if gap, ok := gapBetween(newStart, newEnd, other.StartTime, other.EndTime); ok && gap < need {
				id := b.ID
				resp.Blockers = append(resp.Blockers, ShiftBlocker{
					Reason: BlockerTravelBuffer, BookingID: &id,
					Detail: fmt.Sprintf(
						"%s would leave %d minutes to travel to your other branch, but %d are needed.",
						serviceLabel(b), int(gap.Minutes()), int(need.Minutes())),
				})
			}
		}

		resp.Movable = append(resp.Movable, ShiftPreviewItem{
			BookingID:    b.ID,
			CustomerName: customerLabel(b),
			ServiceName:  serviceLabel(b),
			CurrentStart: b.StartTime,
			NewStart:     newStart,
			NewEnd:       newEnd,
			Status:       b.Status,
			HasPhone:     hasPhone(b),
		})
		if hasPhone(b) {
			resp.NotifiableCount++
		}
	}

	resp.CanApply = len(resp.Blockers) == 0 && len(resp.Movable) > 0
	return resp
}

// gapBetween returns the free time between two intervals, and whether they
// are disjoint at all.
//
// ok is false when the two overlap, which is a harder failure than a short
// buffer - but one the overlap constraint already owns, so this reports it
// as a zero gap rather than duplicating that check.
func gapBetween(aStart, aEnd, bStart, bEnd time.Time) (time.Duration, bool) {
	switch {
	case !aEnd.After(bStart): // a finishes before b starts
		return bStart.Sub(aEnd), true
	case !bEnd.After(aStart): // b finishes before a starts
		return aStart.Sub(bEnd), true
	default:
		return 0, true // overlapping: zero travel time available
	}
}

// localHM formats an instant as HH:MM in the same location as ref.
//
// Times are shown to an artist standing in their own salon, so they must
// read in the store's zone, never the server's.
func localHM(t, ref time.Time) string {
	return t.In(ref.Location()).Format("15:04")
}

// hasPhone reports whether this customer can be notified at all. False
// means someone who has to be telephoned by hand after a bulk change.
func hasPhone(b *EnrichedBooking) bool {
	return b.CustomerPhone != nil && *b.CustomerPhone != ""
}

// customerLabel and serviceLabel degrade to a generic word rather than an
// empty string. A preview line reading "Appointment would finish after
// closing" is still actionable; a blank one is not.
func customerLabel(b *EnrichedBooking) string {
	if b.CustomerName == "" {
		return "Customer"
	}
	return b.CustomerName
}

func serviceLabel(b *EnrichedBooking) string {
	if b.ServiceName == "" {
		return "Appointment"
	}
	return b.ServiceName
}

// ── Service layer ────────────────────────────────────────────────────────────
//
// Moved here from service.go on 2026-09-06, joining the pure evaluator above.
// That file had grown to 2,015 lines holding six unrelated responsibilities;
// preview and apply belong beside evaluateShift because the three must agree
// exactly — the preview is a promise about what apply will do, and the two
// drifting apart is the failure this feature's spec spends most of its length
// guarding against.
//
// The single-statement UPDATE in ShiftDay is load-bearing: the GIST constraint
// is DEFERRABLE INITIALLY IMMEDIATE, so one statement moving a whole day
// succeeds where N statements trip row-by-row.

// PreviewShiftDay is a dry run of shifting every movable booking at one
// store on one day.
//
// Writes nothing. It exists so an artist can see the consequences before
// committing - which matters more here than for most actions, because the
// write version notifies every affected customer over WhatsApp and there is
// no unsending that.
//
// The conflicts it surfaces (past closing, travel buffers) are exactly the
// ones that would otherwise be discovered as a 409 after the artist had
// already decided to go ahead.
// PreviewShiftDay is a dry run: it resolves the day and reports what would
// move, what would be skipped, and what blocks the shift, without writing.
func (s *Service) PreviewShiftDay(ctx context.Context, userID uuid.UUID, req ShiftPreviewRequest) (*ShiftPreviewResponse, error) {
	resp, _, err := s.resolveShift(ctx, userID, req)
	return resp, err
}

// ShiftDay applies a whole-day shift.
//
// All-or-nothing, gated on the same CanApply the preview reports: if
// anything blocks the shift, nothing moves. A partial shift would leave the
// day in an arrangement the artist never asked for and cannot easily reason
// about - "move my afternoon back thirty minutes" either happened or it did
// not.
//
// The day is RE-RESOLVED here rather than trusting a preview the client may
// be holding from minutes ago. Between preview and apply a customer can
// book, cancel, or pay a deposit, and applying a stale plan would move
// bookings around a slot that is no longer free. The database would catch
// the worst of it, but re-evaluating turns a constraint violation into an
// honest "this changed, look again".
func (s *Service) ShiftDay(ctx context.Context, userID uuid.UUID, req ShiftPreviewRequest) (*ShiftPreviewResponse, error) {
	plan, artistID, err := s.resolveShift(ctx, userID, req)
	if err != nil {
		return nil, err
	}

	if !plan.CanApply {
		// The response body still carries the blockers, so the caller gets
		// the reason rather than a bare refusal.
		return plan, apperror.Conflict("SHIFT_NOT_APPLICABLE",
			"This shift cannot be applied - see blockers for why")
	}

	ids := make([]uuid.UUID, 0, len(plan.Movable))
	for _, m := range plan.Movable {
		ids = append(ids, m.BookingID)
	}

	if err := s.repo.ShiftBookings(ctx, ids, plan.ShiftMinutes); err != nil {
		if errors.Is(err, ErrSlotUnavailable) {
			// The evaluation said it fitted and the constraint disagreed,
			// which means the day changed underneath us between resolving
			// and writing. Report it as a conflict to retry, not a 500.
			return nil, apperror.Conflict("SHIFT_CONFLICT",
				"The schedule changed while this was being applied. Please review and try again.")
		}
		return nil, fmt.Errorf("shift day: %w", err)
	}

	s.announceShift(ctx, artistID, plan)
	return plan, nil
}

// announceShift tells every moved customer their appointment time changed.
//
// Best-effort per booking, like every other notification here: the shift has
// already been committed and cannot be undone because a message failed to
// queue.
//
// The message states the new time in WORDS rather than relying on the
// calendar link alone. A customer who added the appointment to their phone
// but never opens the updated link keeps the old time, so the text has to
// carry the change on its own - see migration 031's closing note.
func (s *Service) announceShift(ctx context.Context, artistID uuid.UUID, plan *ShiftPreviewResponse) {
	for _, m := range plan.Movable {
		if !m.HasPhone {
			continue // nothing to send to; the preview already surfaced this
		}
		msg := fmt.Sprintf(
			"Hi %s! Your %s appointment has moved to %s. Sorry for the change - see you then!",
			m.CustomerName, m.ServiceName, notificationTimeLabel(m.NewStart),
		)
		bookingID := m.BookingID
		if err := s.repo.EnqueueNotification(ctx, &bookingID, uuid.Nil, "booking_rescheduled", msg); err != nil {
			s.log.Error("failed to queue a reschedule notice - the shift still applied",
				zap.Error(err),
				zap.String("booking_id", bookingID.String()),
			)
		}
	}
}

// resolveShift resolves a day and evaluates the shift, returning the plan
// and the caller's artist ID.
//
// Shared by the preview and the apply so the two can never disagree about
// what is movable. Duplicating it would mean the button that says "3 will
// move" and the write that moves them are computed by different code.
func (s *Service) resolveShift(ctx context.Context, userID uuid.UUID, req ShiftPreviewRequest) (*ShiftPreviewResponse, uuid.UUID, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, uuid.Nil, mapValidationError(err)
	}

	// Never trust a client-supplied artist ID - RequireRole("artist") proves
	// the caller is AN artist, never that they are THIS artist.
	artistID, err := s.repo.GetArtistIDByUserID(ctx, userID)
	if err != nil {
		return nil, uuid.Nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist profile not found")
	}

	// Zero gets its own error rather than falling out of `required`, whose
	// message would contradict the min/max range on the field.
	if req.ShiftMinutes == nil || *req.ShiftMinutes == 0 {
		return nil, uuid.Nil, apperror.BadRequest("SHIFT_MINUTES_ZERO",
			"shift_minutes must be a non-zero number of minutes between -240 and 240")
	}
	shiftMinutes := *req.ShiftMinutes

	storeID, err := uuid.Parse(req.StoreID)
	if err != nil {
		return nil, uuid.Nil, apperror.BadRequest("INVALID_STORE_ID", "Invalid store ID")
	}
	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		return nil, uuid.Nil, apperror.BadRequest("INVALID_DATE", "Date must be in YYYY-MM-DD format")
	}
	// Go's time.Parse happily accepts year 0, so "0000-01-01" reached the
	// query and scanned a day two millennia ago. Harmless on a read-only
	// endpoint, but there is no legitimate caller and no reason to serve it.
	// Found by E2E-TEST-PLAN.md section 12.3 (finding 3).
	if err := validateScheduleDate(date, time.Now()); err != nil {
		return nil, uuid.Nil, err
	}

	store, err := s.repo.GetStore(ctx, storeID)
	if err != nil {
		return nil, uuid.Nil, apperror.NotFound("STORE_NOT_FOUND", "Store not found")
	}

	// Resolve the trading window through the shared opening-hours package,
	// so this agrees with slot generation and the public open/closed badge
	// rather than re-deriving the same rules a third time.
	exception, err := s.repo.GetBusinessHoursException(ctx, storeID, date)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("preview shift day: get exception: %w", err)
	}
	bh, err := s.repo.GetBusinessHours(ctx, storeID, int(date.Weekday()))
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("preview shift day: get hours: %w", err)
	}
	window, storeOpen, err := openinghours.Resolve(store.Timezone, date, toDayHours(bh), toException(exception))
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("preview shift day: resolve hours: %w", err)
	}

	// The store's own calendar day, not the server's.
	loc := openinghours.Location(store.Timezone)
	dayStart := openinghours.LocalDate(date, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	// One query for the whole day across every store: the bookings to move,
	// the cross-store bookings that constrain them, and the customer and
	// service names for the response. Fetching these separately would be an
	// N+1 per booking just to render a preview.
	all, err := s.repo.ListEnrichedBookingsForDay(ctx, artistID, dayStart, dayEnd)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("preview shift day: list bookings: %w", err)
	}

	var sameStore, crossStore []*EnrichedBooking
	for _, b := range all {
		if b.StoreID == storeID {
			sameStore = append(sameStore, b)
		} else {
			crossStore = append(crossStore, b)
		}
	}

	// Buffer lookups are per store pair and hit the database, so they are
	// resolved once here rather than from inside the pure evaluation.
	isWeekend := !weekdays[date.Weekday()]
	buffers := make(map[uuid.UUID]int, len(crossStore))
	for _, csb := range crossStore {
		if _, done := buffers[csb.StoreID]; done {
			continue
		}
		mins := store.WeekdayBufferMin
		if isWeekend {
			mins = store.WeekendBufferMin
		}
		if override, err := s.repo.GetArtistStoreBuffer(ctx, artistID, csb.StoreID, storeID); err == nil && override != nil {
			mins = override.WeekdayBufferMin
			if isWeekend {
				mins = override.WeekendBufferMin
			}
		}
		buffers[csb.StoreID] = mins
	}

	resp := evaluateShift(shiftInput{
		SameStore:    sameStore,
		CrossStore:   crossStore,
		OpenAt:       window.OpenAt,
		CloseAt:      window.CloseAt,
		StoreOpen:    storeOpen,
		BufferFor:    func(id uuid.UUID) int { return buffers[id] },
		ShiftMinutes: shiftMinutes,
		Now:          time.Now(),
	})
	resp.Date = req.Date
	return resp, artistID, nil
}

// scheduleDateWindowYears bounds how far a schedule operation may address.
//
// Two years either side covers rebuilding last season's history and booking
// a wedding well ahead, while excluding the values that only ever arrive by
// accident - a year-zero date from a broken picker, or a timestamp parsed as
// a year.
const scheduleDateWindowYears = 2

// validateScheduleDate rejects dates outside a sane window.
func validateScheduleDate(date, now time.Time) error {
	earliest := now.AddDate(-scheduleDateWindowYears, 0, 0)
	latest := now.AddDate(scheduleDateWindowYears, 0, 0)
	if date.Before(earliest) || date.After(latest) {
		return apperror.BadRequest("DATE_OUT_OF_RANGE",
			fmt.Sprintf("Date must be within %d years of today", scheduleDateWindowYears))
	}
	return nil
}
