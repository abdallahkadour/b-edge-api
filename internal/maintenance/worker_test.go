package maintenance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The sweep itself is a DELETE against a live pool, which this repository has
// no infrastructure to test (see the Explicitly-not-built note in the sprint
// plan: there is no DB test harness, and building one is its own project).
// What IS worth pinning are the two constants, because both encode a
// deliberate decision that a future change could quietly reverse.

// Deleting the instant a token expires destroys the evidence for the case most
// worth investigating: a refresh arriving moments after expiry, which is what
// credential theft looks like. If the grace period ever goes to zero, that
// signal goes with it.
func TestGracePeriod_LeavesEvidenceOfLateRefreshAttempts(t *testing.T) {
	assert.Positive(t, gracePeriod,
		"a zero grace period deletes the evidence of a just-too-late refresh")
	assert.GreaterOrEqual(t, gracePeriod, time.Hour,
		"the grace period must outlast the sweep interval, or a row can be reaped "+
			"in the same hour it expires")
}

// Hourly is deliberate. Nothing goes wrong if a dead row survives another
// fifty-nine minutes - verification already refuses it - so a tighter loop
// only buys a DELETE every few seconds against an almost-always-clean table.
func TestSweepInterval_IsNotATightLoop(t *testing.T) {
	assert.GreaterOrEqual(t, sweepInterval, 10*time.Minute)
}
