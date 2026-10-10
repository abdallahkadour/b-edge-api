package booking

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// The no-show deposit rule, decision D29 (2026-10-09). Migration 055 holds
// the reasoning; in short: a customer who has missed at least the salon's
// no_show_deposit_after appointments THERE in the last 12 months pays half
// the price as a deposit to book a service that otherwise takes none.

// noShowWindow is how far back a missed appointment still counts. A miss
// from years ago should not follow someone forever.
const noShowWindow = 365 * 24 * time.Hour

// noShowDepositShare of the price is asked for. Half is enough to make a
// second no-show cost the customer what the first cost the artist, and short
// of charging in full for something not yet received.
var noShowDepositShare = decimal.RequireFromString("0.5")

// noShowDeposit decides the deposit for a booking given the customer's
// recent no-shows at the salon and the salon's setting. It only ever turns
// "no deposit" into one: a service that already takes a deposit is already
// protected, and a free service is asked for nothing.
func noShowDeposit(finalPrice, deposit decimal.Decimal, noShows, after int) (decimal.Decimal, bool) {
	if after <= 0 || noShows < after || deposit.IsPositive() || !finalPrice.IsPositive() {
		return deposit, false
	}
	return finalPrice.Mul(noShowDepositShare).Round(2), true
}

// applyNoShowRule runs D29 on a booking that has just been submitted, when
// its customer becomes known, and updates b to match what was stored.
//
// Best-effort, like every notification in this service: a failed lookup
// leaves the booking without the extra deposit rather than refusing a
// customer her booking over the protection for it. Logged, so a rule that
// silently stopped working shows up.
func (s *Service) applyNoShowRule(ctx context.Context, b *Booking) {
	after, count, err := s.repo.NoShowHistory(ctx, b.SalonID, b.CustomerID, time.Now().Add(-noShowWindow))
	if err != nil {
		s.log.Warn("no-show rule: history unreadable, booking kept without the deposit",
			zap.Error(err), zap.String("booking_id", b.ID.String()))
		return
	}
	amount, applies := noShowDeposit(b.FinalPrice, b.DepositAmount, count, after)
	if !applies {
		return
	}
	rows, err := s.repo.RequireNoShowDeposit(ctx, b.ID, amount)
	if err != nil || rows == 0 {
		s.log.Warn("no-show rule: deposit not recorded", zap.Error(err),
			zap.Int64("rows", rows), zap.String("booking_id", b.ID.String()))
		return
	}
	b.DepositAmount = amount
	b.NoShowDeposit = true
}
