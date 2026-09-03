package booking

// Background sweep for stalled waitlist queues.
//
// WHY THIS EXISTS
//
// Cascading is normally lazy: when a slot frees up, the freeing operation
// tells the next person in line (see cascadeWaitlist). That covers the happy
// path and needs no scheduler, which is the design this codebase prefers
// everywhere - expired holds, lapsed deposits and subscription status are
// all derived or self-healed on read rather than swept on a timer.
//
// One case cannot be done that way, and migration 016's header called it out
// when the feature shipped:
//
//	"If no further cancellation ever happens for that exact combination, an
//	 expired-but-uncascaded entry can sit in 'notified' state indefinitely.
//	 Acceptable for a first version - a real background sweep is a small,
//	 separate follow-up once this proves out."
//
// The stall is specific. Someone is notified that a slot opened, their
// 30-minute confirm window passes without them acting, and nothing else ever
// happens for that exact (artist, store, service, date). The entry stays
// `notified` forever, and everyone behind them in that queue waits on a
// person who has already lost their chance. Lazy expiry cannot fix it,
// because the trigger for lazy expiry is another slot opening - which is the
// thing that is not happening.
//
// This is a timer, and it is deliberate. It is not a case that can be
// derived on read, because the read that would derive it never comes.
//
// WHAT IT DOES NOT DO
//
// It does not re-implement the cascade. It finds the stalled queues and
// hands each to NotifyNextWaitlistEntry, which is the same call the lazy
// path makes and which already expires the stale entry and notifies the next
// person in one place. A second implementation of "who is next" would be a
// second thing to keep correct.

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// waitlistSweepInterval is how often stalled queues are checked.
//
// Five minutes against a 30-minute confirm window: a stalled queue moves on
// within a sixth of the window it was waiting through, which is well inside
// the resolution anyone perceives, while the query itself is an indexed scan
// that returns nothing in the overwhelmingly common case.
//
// Deliberately not tighter. The cost of being a few minutes late is that one
// person waits slightly longer for a slot they were going to be offered
// anyway; the cost of a tight loop is a query every few seconds forever, for
// a condition that is rare by construction.
const waitlistSweepInterval = 5 * time.Minute

// waitlistSweepRepo is the narrow slice of the booking repository this
// worker needs. Declared here rather than taking the full Repository so the
// worker's dependencies are visible at a glance and its test needs a
// two-method fake rather than the whole interface.
type waitlistSweepRepo interface {
	FindStaleWaitlistGroups(ctx context.Context, now time.Time) ([]WaitlistGroup, error)
	NotifyNextWaitlistEntry(ctx context.Context, artistID, storeID, serviceID uuid.UUID, date time.Time) error
}

// WaitlistWorker unstalls waitlist queues whose notified entry never
// confirmed.
type WaitlistWorker struct {
	repo waitlistSweepRepo
	log  *zap.Logger
	// now is injected so the sweep can be tested without waiting for real
	// time to pass.
	now func() time.Time
}

// NewWaitlistWorker creates the sweep.
func NewWaitlistWorker(db *pgxpool.Pool, log *zap.Logger) *WaitlistWorker {
	return &WaitlistWorker{
		repo: NewRepository(db),
		log:  log.With(zap.String("module", "waitlist_worker")),
		now:  time.Now,
	}
}

// Start runs the sweep until ctx is cancelled.
//
// Matches the notification worker's shape exactly, and is supervised by the
// same superviseWorker in main.go - which means it inherits the
// panic-recover-and-restart guarantee rather than needing its own.
func (w *WaitlistWorker) Start(ctx context.Context) {
	w.log.Info("Waitlist worker started",
		zap.Duration("sweep_interval", waitlistSweepInterval),
		zap.Duration("confirm_window", waitlistConfirmWindow),
	)

	ticker := time.NewTicker(waitlistSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("Waitlist worker stopped")
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// sweep unstalls every queue holding an expired notification.
//
// Exported behaviour worth stating: a failure on one group does not stop the
// others. A single bad row - a deleted artist, a constraint violation -
// must not be able to keep every other stalled queue frozen, which is
// exactly the failure this worker exists to prevent.
func (w *WaitlistWorker) sweep(ctx context.Context) {
	groups, err := w.repo.FindStaleWaitlistGroups(ctx, w.now())
	if err != nil {
		w.log.Error("waitlist worker: could not find stale groups", zap.Error(err))
		return
	}
	if len(groups) == 0 {
		return // the overwhelmingly common case
	}

	w.log.Info("waitlist worker: unstalling queues", zap.Int("groups", len(groups)))

	for _, g := range groups {
		if err := w.repo.NotifyNextWaitlistEntry(ctx, g.ArtistID, g.StoreID, g.ServiceID, g.Date); err != nil {
			w.log.Error("waitlist worker: could not cascade a stalled queue",
				zap.Error(err),
				zap.String("artist_id", g.ArtistID.String()),
				zap.String("service_id", g.ServiceID.String()),
			)
			continue
		}
	}
}
