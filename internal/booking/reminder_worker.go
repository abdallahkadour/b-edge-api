package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// reminderLeadTime is how far ahead of the appointment the reminder is sent.
//
// Twenty-four hours, matched to the cancellation policy. A client who cancels
// inside 24 hours forfeits their deposit, so a reminder landing at that
// boundary is the last moment the news is still useful to them rather than
// merely annoying. It is also the last moment the artist can still refill the
// slot from the waitlist.
const reminderLeadTime = 24 * time.Hour

// reminderSweepInterval is how often upcoming bookings are scanned.
//
// Fifteen minutes. The reminder is scheduled for an exact time and the
// notification worker sends it then, so this interval only controls how
// promptly a NEWLY confirmed booking gets its reminder row - not when the
// reminder actually arrives. Precision costs nothing here.
const reminderSweepInterval = 15 * time.Minute

// reminderTemplate is the template name, and the prefix the idempotency index
// in migration 041 matches on. Changing it without changing that index would
// silently allow duplicate reminders.
const reminderTemplate = "booking_reminder_24h"

// ReminderWorker schedules appointment reminders.
//
// # WHY A SWEEP RATHER THAN SCHEDULING AT BOOKING TIME
//
// A booking can be approved, cancelled, rescheduled or shifted in bulk after it
// is created. Scheduling the reminder when the booking is made would mean
// finding and rewriting that row on every one of those paths, and missing one
// sends a reminder for an appointment that is not happening - which is worse
// than sending none.
//
// A sweep reads the current state of the world every fifteen minutes and is
// therefore correct by construction. The unique index does the deduplication.
type ReminderWorker struct {
	db  *pgxpool.Pool
	log *zap.Logger
	now func() time.Time
}

// NewReminderWorker creates the reminder sweep.
func NewReminderWorker(db *pgxpool.Pool, log *zap.Logger) *ReminderWorker {
	return &ReminderWorker{
		db:  db,
		log: log.With(zap.String("module", "reminder_worker")),
		now: time.Now,
	}
}

// Start runs the sweep until ctx is cancelled.
//
// Supervised by superviseWorker in main.go, like the notification and waitlist
// workers, so it inherits panic-recover-and-restart rather than needing its own.
func (w *ReminderWorker) Start(ctx context.Context) {
	w.log.Info("Reminder worker started",
		zap.Duration("sweep_interval", reminderSweepInterval),
		zap.Duration("lead_time", reminderLeadTime),
	)

	w.sweep(ctx)

	ticker := time.NewTicker(reminderSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("Reminder worker stopped")
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// sweep reconciles scheduled reminders against the current state of the
// calendar, in BOTH directions: it writes a reminder for every upcoming
// booking that lacks one, and withdraws any unsent reminder for a booking that
// is no longer happening.
//
// Withdrawal lives here rather than on the cancellation path for the same
// reason scheduling does. A booking can be cancelled, marked a no-show, expire,
// or be moved by a bulk day-shift; hooking every one of those and missing one
// sends a reminder for an appointment that is not happening - which is worse
// than sending none. A sweep reads the world as it is and is correct by
// construction.
func (w *ReminderWorker) sweep(ctx context.Context) {
	now := w.now()
	w.withdrawStale(ctx)

	tag, err := w.db.Exec(ctx, `
		INSERT INTO notifications
			(booking_id, user_id, template_name, channel, payload, scheduled_at)
		SELECT
			b.id,
			b.customer_id,
			$1,
			'whatsapp',
			jsonb_build_object('message',
				'Reminder: your ' || s.name || ' appointment with ' || au.name ||
				' is tomorrow at ' ||
				to_char(b.start_time AT TIME ZONE COALESCE(st.timezone, 'Asia/Beirut'), 'HH12:MIam') ||
				' at ' || st.name || '.'),
			b.start_time - $2::interval
		FROM bookings b
		JOIN services s  ON s.id = b.service_id
		JOIN stores   st ON st.id = b.store_id
		JOIN artists  a  ON a.id = b.artist_id
		JOIN users    au ON au.id = a.user_id
		JOIN users    u  ON u.id = b.customer_id
		WHERE b.status IN ('approved', 'deposit_paid', 'confirmed')
		  -- The reminder must still be in the future. A booking confirmed
		  -- eleven hours before it starts has already passed its reminder
		  -- time, and a "tomorrow" message sent today is worse than silence.
		  AND b.start_time - $2::interval > $3
		  -- Bounded lookahead so the sweep does not rewrite the whole table
		  -- every fifteen minutes as the calendar fills up.
		  AND b.start_time < $3 + interval '7 days'
		  -- recipient_phone is deliberately left NULL: the notification
		  -- worker already resolves it from user_id, and copying the number
		  -- here would create a second place for it to go stale.
		  AND u.phone IS NOT NULL
		  AND u.deleted_at IS NULL
		ON CONFLICT DO NOTHING`,
		reminderTemplate, reminderLeadTime, now,
	)
	if err != nil {
		w.log.Error("reminder sweep failed", zap.Error(err))
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		w.log.Info("scheduled appointment reminders", zap.Int64("count", n))
	}
}

// withdrawStale deletes unsent reminders for bookings that are no longer
// happening, or that have moved so far that the reminder is now wrong.
//
// Only 'pending' rows with no sent_at are touched. A reminder already
// delivered is history: deleting it would lose the record that the client was
// told, which is exactly the record a later dispute needs.
func (w *ReminderWorker) withdrawStale(ctx context.Context) {
	tag, err := w.db.Exec(ctx, `
		DELETE FROM notifications n
		 USING bookings b
		 WHERE n.booking_id = b.id
		   AND n.template_name LIKE 'booking_reminder%'
		   AND n.status = 'pending'
		   AND n.sent_at IS NULL
		   AND (
		         b.status NOT IN ('approved', 'deposit_paid', 'confirmed')
		         -- The booking moved. The scheduled time no longer lines up
		         -- with the appointment, so this row is stale rather than
		         -- merely early; the INSERT half will write a correct one.
		         OR n.scheduled_at <> b.start_time - $1::interval
		       )`, reminderLeadTime)
	if err != nil {
		w.log.Error("reminder withdrawal failed", zap.Error(err))
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		w.log.Info("withdrew stale reminders", zap.Int64("count", n))
	}
}

// CancelRemindersFor removes any unsent reminder for a booking.
//
// The sweep already withdraws these within fifteen minutes, so this exists
// only for a caller that wants the effect immediately - cancelling a booking
// minutes before its reminder fires, say. It is deliberately NOT wired into
// the cancellation path: relying on it there would reintroduce the
// hook-every-path problem that withdrawStale exists to avoid.
//
// Only 'pending' rows are touched: a reminder already sent is history, and
// deleting it would lose the record that the client was told.
func CancelRemindersFor(ctx context.Context, db *pgxpool.Pool, bookingID any) error {
	_, err := db.Exec(ctx, `
		DELETE FROM notifications
		 WHERE booking_id = $1
		   AND template_name LIKE 'booking_reminder%'
		   AND status = 'pending'
		   AND sent_at IS NULL`, bookingID)
	if err != nil {
		return fmt.Errorf("cancel reminders: %w", err)
	}
	return nil
}
