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

// morningReminderTemplate is the same-day "your appointment is today" nudge.
//
// It is a SECOND reminder, not a replacement for the 24-hour one. They answer
// different questions: the 24-hour reminder lands while cancelling is still
// free and the slot can still be refilled from the waitlist; this one lands
// when the only useful action left is to set off on time. An artist's real
// loss is not a cancellation a day out - it is a client who forgets, arrives
// twenty minutes late, and eats the next client's slot.
//
// The prefix matters: migration 041's unique index is
// `(booking_id, template_name) WHERE template_name LIKE 'booking_reminder%'`,
// so this name is deduplicated per booking automatically. Renaming it to
// anything outside that prefix would silently permit duplicate reminders.
const morningReminderTemplate = "booking_reminder_morning"

// morningReminderHour is the store-local hour the same-day reminder is sent.
//
// 08:00 in the STORE's timezone, not the customer's and not UTC - a Beirut
// salon's client should be woken at 8am Beirut time wherever their phone
// happens to be roaming.
const morningReminderHour = 8

// minMorningLead is how long before the appointment the morning reminder must
// land to be worth sending.
//
// A reminder that arrives twenty minutes before an 08:20 appointment tells
// someone they are already too late to leave on time, which is worse than
// silence. Appointments earlier than 08:00 + this lead get no morning
// reminder at all; the 24-hour one already covered them.
const minMorningLead = 45 * time.Minute

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

// morningScheduleSQL is the scheduled_at of a morning reminder: 08:00 on the
// appointment's own local date, in the store's timezone, expressed back as a
// timestamptz.
//
// It is a CONSTANT shared by the insert and the withdrawal deliberately. The
// withdrawal deletes any reminder whose scheduled_at no longer matches what
// the insert would compute; if these two expressions ever disagree by so much
// as a cast, the sweep deletes the row it just wrote, every fifteen minutes,
// and the reminder silently never sends. Same hazard the 24-hour pair has,
// which is why that one is a single interval constant rather than two.
const morningScheduleSQL = `((b.start_time AT TIME ZONE COALESCE(st.timezone, 'Asia/Beirut'))::date
	 + make_time($2::int, 0, 0)) AT TIME ZONE COALESCE(st.timezone, 'Asia/Beirut')`

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
	defer w.sweepMorning(ctx, now)

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

// sweepMorning schedules the same-day "your appointment is today" reminder.
//
// Separate statement rather than a second branch of the 24-hour insert,
// because the two differ in the one thing that matters - when they fire - and
// merging them would mean a CASE over scheduled_at in a query that is already
// the least readable thing in this file.
//
// Every customer with an appointment that day gets exactly one, at 08:00 in
// the store's own timezone, deduplicated by migration 041's unique index.
func (w *ReminderWorker) sweepMorning(ctx context.Context, now time.Time) {
	tag, err := w.db.Exec(ctx, `
		INSERT INTO notifications
			(booking_id, user_id, template_name, channel, payload, scheduled_at)
		SELECT
			b.id,
			b.customer_id,
			$1,
			'whatsapp',
			jsonb_build_object('message',
				'Today: your ' || s.name || ' appointment with ' || au.name ||
				' is at ' ||
				to_char(b.start_time AT TIME ZONE COALESCE(st.timezone, 'Asia/Beirut'), 'HH12:MIam') ||
				' at ' || st.name ||
				'. Please arrive a few minutes early - arriving late may shorten ' ||
				'your appointment or mean it has to be rescheduled.'),
			`+morningScheduleSQL+`
		FROM bookings b
		JOIN services s  ON s.id = b.service_id
		JOIN stores   st ON st.id = b.store_id
		JOIN artists  a  ON a.id = b.artist_id
		JOIN users    au ON au.id = a.user_id
		JOIN users    u  ON u.id = b.customer_id
		WHERE b.status IN ('approved', 'deposit_paid', 'confirmed')
		  -- Still ahead of us. A morning reminder for an appointment whose
		  -- 08:00 has already passed would send immediately and read as
		  -- "today at 9am" to someone whose 9am is over.
		  AND `+morningScheduleSQL+` > $3
		  -- And far enough ahead to be actionable. See minMorningLead: an
		  -- 08:20 appointment cannot be usefully reminded about at 08:00.
		  AND b.start_time - `+morningScheduleSQL+` >= $4::interval
		  AND b.start_time < $3 + interval '7 days'
		  AND u.phone IS NOT NULL
		  AND u.deleted_at IS NULL
		ON CONFLICT DO NOTHING`,
		morningReminderTemplate, morningReminderHour, now, minMorningLead,
	)
	if err != nil {
		w.log.Error("morning reminder sweep failed", zap.Error(err))
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		w.log.Info("scheduled morning reminders", zap.Int64("count", n))
	}
}

// withdrawStale deletes unsent reminders for bookings that are no longer
// happening, or that have moved so far that the reminder is now wrong.
//
// Only 'pending' rows with no sent_at are touched. A reminder already
// delivered is history: deleting it would lose the record that the client was
// told, which is exactly the record a later dispute needs.
func (w *ReminderWorker) withdrawStale(ctx context.Context) {
	// The "has it moved?" test is PER TEMPLATE. An earlier version compared
	// every booking_reminder% row against the 24-hour formula, which was
	// correct while that was the only kind. The moment a second kind exists
	// that rule deletes it on sight - its scheduled_at is 08:00 local, never
	// start_time - 24h - so the morning reminder would be written and
	// destroyed on every fifteen-minute tick and never send, with nothing in
	// the logs but a steadily climbing withdrawal count.
	tag, err := w.db.Exec(ctx, `
		DELETE FROM notifications n
		 USING bookings b
		  LEFT JOIN stores st ON st.id = b.store_id
		 WHERE n.booking_id = b.id
		   AND n.template_name LIKE 'booking_reminder%'
		   AND n.status = 'pending'
		   AND n.sent_at IS NULL
		   AND (
		         b.status NOT IN ('approved', 'deposit_paid', 'confirmed')
		         -- The booking moved. The scheduled time no longer lines up
		         -- with the appointment, so this row is stale rather than
		         -- merely early; the INSERT half will write a correct one.
		         OR (n.template_name = $3 AND n.scheduled_at <> b.start_time - $1::interval)
		         OR (n.template_name = $4 AND n.scheduled_at <> `+morningScheduleSQL+`)
		       )`, reminderLeadTime, morningReminderHour, reminderTemplate, morningReminderTemplate)
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
