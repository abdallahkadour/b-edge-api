// Package maintenance reaps rows that have expired and are never read again.
//
// # WHY THIS EXISTS
//
// Nothing deleted expired credentials. Measured on a development database
// with 36 users and 32 bookings:
//
//	refresh_tokens   1240 rows, 1168 of them already expired  (94%)
//	customer_otps      48 rows,   48 of them already expired (100%)
//
// refresh_tokens was by a wide margin the largest table in the database, and
// every row in it is a credential hash. That is two problems, not one:
//
//   - Unbounded growth. One row per login, forever, for a value that is dead
//     after seven days. The ratio only gets worse with real traffic.
//   - Blast radius. A table of long-dead credential hashes is pure liability
//     if it is ever exposed, and it grows every day nobody looks at it.
//
// An expired token is already refused by verification, so deleting it changes
// no behaviour. This only removes rows that can never be used again.
package maintenance

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// sweepInterval is how often expired rows are reaped.
//
// Hourly, not tighter. Nothing goes wrong if a dead row survives another
// fifty-nine minutes - verification already refuses it - so the only thing a
// tight loop would buy is a DELETE every few seconds against a table that is
// almost always already clean.
const sweepInterval = time.Hour

// gracePeriod keeps rows a little past expiry.
//
// Deleting the instant a token expires destroys the evidence for the case
// most worth investigating: a refresh attempt arriving moments after expiry,
// which is what credential theft looks like. A day costs nothing and leaves
// something to look at.
const gracePeriod = 24 * time.Hour

// Worker deletes expired credentials on a timer.
type Worker struct {
	db  *pgxpool.Pool
	log *zap.Logger
	now func() time.Time
}

// NewWorker creates the maintenance sweep.
func NewWorker(db *pgxpool.Pool, log *zap.Logger) *Worker {
	return &Worker{
		db:  db,
		log: log.With(zap.String("module", "maintenance_worker")),
		now: time.Now,
	}
}

// Start runs the sweep until ctx is cancelled.
//
// Matches the notification and waitlist workers exactly, so it inherits the
// panic-recover-and-restart guarantee from superviseWorker in main.go rather
// than needing its own.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("Maintenance worker started",
		zap.Duration("sweep_interval", sweepInterval),
		zap.Duration("grace_period", gracePeriod),
	)

	// Once at boot as well as on the tick. A process that restarts more often
	// than the interval would otherwise never sweep at all.
	w.sweep(ctx)

	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("Maintenance worker stopped")
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// sweep deletes what has expired past the grace period.
//
// Each table is its own statement rather than one transaction: they are
// independent, and a failure to reap OTPs must not roll back a successful
// reap of refresh tokens. Errors are logged and the next tick tries again -
// there is nothing a caller could do about it.
func (w *Worker) sweep(ctx context.Context) {
	cutoff := w.now().Add(-gracePeriod)

	for _, t := range []struct{ name, stmt string }{
		{"refresh_tokens", `DELETE FROM refresh_tokens WHERE expires_at < $1`},
		{"customer_otps", `DELETE FROM customer_otps WHERE expires_at < $1`},
	} {
		tag, err := w.db.Exec(ctx, t.stmt, cutoff)
		if err != nil {
			w.log.Error("maintenance sweep failed", zap.String("table", t.name), zap.Error(err))
			continue
		}
		if n := tag.RowsAffected(); n > 0 {
			w.log.Info("reaped expired rows",
				zap.String("table", t.name), zap.Int64("rows", n))
		}
	}
}
