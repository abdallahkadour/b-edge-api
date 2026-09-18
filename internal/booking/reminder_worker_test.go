package booking

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The sweep is two SQL statements against a live pool, which this repository
// has no harness to test (see the sprint plan's "Explicitly not built" note on
// repository tests). What IS worth pinning are the constants and the template
// name, because each encodes a decision that a later change could reverse
// without anything failing.

// Twenty-four hours is matched to the cancellation policy, not chosen for
// roundness. A client who cancels inside 24 hours forfeits their deposit, so a
// reminder landing at that boundary is the last moment the news is still
// useful rather than merely annoying - and the last moment the artist can
// refill the slot from the waitlist.
func TestReminderLeadTime_MatchesTheCancellationBoundary(t *testing.T) {
	assert.Equal(t, 24*time.Hour, reminderLeadTime,
		"the lead time is deliberately the same 24h boundary as the deposit-forfeit rule")
}

// The sweep only controls how promptly a NEWLY confirmed booking gets its
// reminder row. The reminder itself fires at scheduled_at, sent by the
// notification worker, so a tighter interval buys nothing.
func TestReminderSweepInterval_IsNotATightLoop(t *testing.T) {
	assert.GreaterOrEqual(t, reminderSweepInterval, 5*time.Minute)
	assert.Less(t, reminderSweepInterval, reminderLeadTime,
		"the sweep must run more often than the lead time, or a booking made "+
			"inside one interval of its reminder would never get one")
}

// migration 041's unique index matches `template_name LIKE 'booking_reminder%'`.
// Renaming this constant without changing that index would silently allow
// duplicate reminders - the one failure mode whose fix is an apology rather
// than a retry.
func TestReminderTemplate_MatchesTheIdempotencyIndexPrefix(t *testing.T) {
	assert.True(t, len(reminderTemplate) > len("booking_reminder"),
		"the template name must be more specific than the index prefix")
	assert.Equal(t, "booking_reminder", reminderTemplate[:len("booking_reminder")],
		"migration 041's unique index matches on this prefix")
}
