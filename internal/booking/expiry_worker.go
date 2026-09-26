package booking

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// expirySweepInterval is how often abandoned holds and lapsed-deposit
// approvals are expired. A hold lasts SlotHoldDuration (10 minutes); a
// minute's lag past that is invisible to customers, and the availability
// read path still sweeps on every request, so nothing a customer is about to
// see waits for this tick.
const expirySweepInterval = time.Minute

// expirySweeper is the narrow slice of the booking Service this worker needs:
// the two SERVICE-level sweeps, which also tell the waitlist a slot opened.
// The repository sweeps underneath them do not - that difference is the
// whole reason this worker calls the service.
type expirySweeper interface {
	ReleaseExpiredHolds(ctx context.Context) (int64, error)
	ExpireDeadlineBookings(ctx context.Context) (int64, error)
}

// ExpiryWorker expires abandoned holds and approvals whose deposit deadline
// lapsed, on a clock.
//
// # WHY A WORKER AS WELL AS THE READ PATH
//
// The database's overlap rule blocks a 'held' or 'approved' row whatever its
// held_until or deposit_deadline says - a constraint cannot depend on NOW().
// So an expired row keeps blocking its slot until something moves it. The
// availability read path does that on every request, which covers the
// booking funnel; this worker covers everything that never loads
// availability first (a direct hold, an artist's own booking, a reschedule),
// and it is what tells the waitlist when nobody is looking at that day.
type ExpiryWorker struct {
	svc expirySweeper
	log *zap.Logger
}

// NewExpiryWorker creates the sweep over the real booking service.
func NewExpiryWorker(db *pgxpool.Pool, log *zap.Logger) *ExpiryWorker {
	l := log.With(zap.String("module", "expiry_worker"))
	return &ExpiryWorker{svc: NewService(NewRepository(db), nil, l), log: l}
}

// Start sweeps once immediately - a restart must not leave stale rows
// blocking for a full interval - then on every tick until ctx is cancelled.
// Supervised by superviseWorker in cmd/main.go like the other workers, so a
// panic restarts it rather than silently stopping expiry.
func (w *ExpiryWorker) Start(ctx context.Context) {
	w.log.Info("Expiry worker started", zap.Duration("sweep_interval", expirySweepInterval))
	w.sweep(ctx)

	ticker := time.NewTicker(expirySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.log.Info("Expiry worker stopped")
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// sweep runs both expiries. One failing does not stop the other: a broken
// hold sweep must not also leave lapsed-deposit approvals blocking.
func (w *ExpiryWorker) sweep(ctx context.Context) {
	if n, err := w.svc.ReleaseExpiredHolds(ctx); err != nil {
		w.log.Error("expiry worker: releasing expired holds failed", zap.Error(err))
	} else if n > 0 {
		w.log.Info("expiry worker: released expired holds", zap.Int64("count", n))
	}
	if n, err := w.svc.ExpireDeadlineBookings(ctx); err != nil {
		w.log.Error("expiry worker: expiring lapsed-deposit bookings failed", zap.Error(err))
	} else if n > 0 {
		w.log.Info("expiry worker: expired lapsed-deposit bookings", zap.Int64("count", n))
	}
}
