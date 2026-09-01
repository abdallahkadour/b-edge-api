// Tests for the bidi control stripper.
//
// Moved here from internal/share when a second caller appeared
// (internal/notification, which interpolates a customer-supplied name into
// a notification body another person reads). The cases are unchanged - they
// are the ones that found the original bug and the ones that pin the
// deliberate Arabic carve-out.
package bidi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The exact payload that produced the finding: this renders in a WhatsApp
// preview as "Book now https://evil.com" if U+202E survives.
func TestStripControls_RemovesRTLOverride(t *testing.T) {
	got := StripControls("Book now ‮moc.live//:sptth")

	assert.NotContains(t, got, "‮", "the override must not survive into a meta tag")
	assert.Equal(t, "Book now moc.live//:sptth", got,
		"only the control character is removed; the visible text is left alone")
}

func TestStripControls_RemovesAllOverridesAndIsolates(t *testing.T) {
	for _, r := range []rune{
		'‪', '‫', '‬', '‭', '‮', // embeddings, PDF, overrides
		'⁦', '⁧', '⁨', '⁩', // isolates
	} {
		in := "a" + string(r) + "b"
		assert.Equal(t, "ab", StripControls(in),
			"U+%04X must be stripped", r)
	}
}

// Arabic renders correctly through the bidi algorithm alone, so stripping
// controls must not touch the script itself.
func TestStripControls_LeavesArabicIntact(t *testing.T) {
	const arabic = "صالون الجمال"
	assert.Equal(t, arabic, StripControls(arabic))

	const mixed = "Rania صالون 2026"
	assert.Equal(t, mixed, StripControls(mixed))
}

// LRM and RLM are weak directional MARKS, not overrides, and are
// legitimately used to disambiguate a phone number inside an Arabic
// sentence. Stripping them would degrade exactly the Arabic typography this
// product is supposed to be good at.
func TestStripControls_KeepsDirectionalMarks(t *testing.T) {
	withLRM := "اتصل ‎+96170123456"
	assert.Equal(t, withLRM, StripControls(withLRM),
		"U+200E is a hint, not an override - it must survive")

	withRLM := "call ‏صالون"
	assert.Equal(t, withRLM, StripControls(withRLM))
}

func TestStripControls_NoControls_Unchanged(t *testing.T) {
	assert.Equal(t, "Best bridal makeup artist in Beirut.",
		StripControls("Best bridal makeup artist in Beirut."))
	assert.Equal(t, "", StripControls(""))
}
