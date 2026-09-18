package phone

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Lebanon: the home market and the default ──────────────────────────────────

func TestNormalize_LebaneseNumbersInEveryFormPeopleType(t *testing.T) {
	// All of these are the same subscriber. Separators carry no information
	// and a form that rejects one of them is rejecting a real customer.
	for _, in := range []string{
		"71900001", "71 900 001", "071900001", "+96171900001",
		"+961 71 900 001", "0096171900001", "(71) 900-001",
	} {
		got, err := Normalize(in, "LB")
		require.NoError(t, err, "input %q", in)
		assert.Equal(t, "+96171900001", got, "input %q", in)
	}
}

// Lebanese mobiles are 7 or 8 digits depending on the range; both are still
// in service, so both must pass.
func TestNormalize_LebanonAcceptsSevenAndEightDigits(t *testing.T) {
	got, err := Normalize("3123456", "LB")
	require.NoError(t, err)
	assert.Equal(t, "+9613123456", got)

	got, err = Normalize("71234567", "LB")
	require.NoError(t, err)
	assert.Equal(t, "+96171234567", got)
}

// A landline cannot receive a WhatsApp message, so accepting one produces a
// booking whose confirmation silently never arrives.
func TestNormalize_RejectsALebaneseLandline(t *testing.T) {
	_, err := Normalize("1234567", "LB") // Beirut landlines start 01
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mobile")
}

// ── MENA ──────────────────────────────────────────────────────────────────────

func TestNormalize_AcceptsTheSupportedMENACountries(t *testing.T) {
	cases := map[string]string{
		"+971501234567": "AE", "+966512345678": "SA", "+97433123456": "QA",
		"+96550123456": "KW", "+97336123456": "BH", "+96891234567": "OM",
		"+962791234567": "JO", "+201012345678": "EG", "+9647712345678"[:14]: "IQ",
		"+963912345678"[:13]: "SY",
	}
	for in, iso := range cases {
		got, err := Normalize(in, "LB")
		require.NoError(t, err, "%s (%s)", in, iso)
		assert.Equal(t, in, got, "%s should round-trip unchanged", in)
	}
}

// An explicit country code always beats the form's default. A Jordanian
// number typed into a Lebanon-defaulted field is still Jordanian.
func TestNormalize_ExplicitCountryCodeBeatsTheDefault(t *testing.T) {
	got, err := Normalize("+962791234567", "LB")
	require.NoError(t, err)
	assert.Equal(t, "+962791234567", got)
}

// 00 is how people actually dial abroad in this region.
func TestNormalize_TreatsDoubleZeroAsPlus(t *testing.T) {
	got, err := Normalize("00971501234567", "LB")
	require.NoError(t, err)
	assert.Equal(t, "+971501234567", got)
}

// Egypt's +20 is two digits while Lebanon's is three. Matching the shortest
// code first would read +201012345678 as something else entirely.
func TestNormalize_LongestCallingCodeWinsTheMatch(t *testing.T) {
	got, err := Normalize("+201012345678", "LB")
	require.NoError(t, err)
	assert.Equal(t, "+201012345678", got)
}

// ── Rejection ─────────────────────────────────────────────────────────────────

func TestNormalize_RejectsWhatTheOldRuleAccepted(t *testing.T) {
	// The API's only rule was validate:"required,min=7,max=20" - a LENGTH
	// check on a string. Every one of these passed it.
	for _, in := range []string{"aaaaaaa", "not a phone", "-------", "0000000"} {
		_, err := Normalize(in, "LB")
		assert.Error(t, err, "input %q must be rejected", in)
	}
}

func TestNormalize_RejectsUnsupportedCountries(t *testing.T) {
	_, err := Normalize("+14155551234", "LB") // US
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestNormalize_RejectsEmptyAndWrongLength(t *testing.T) {
	_, err := Normalize("", "LB")
	assert.Error(t, err)
	_, err = Normalize("719", "LB")
	assert.Error(t, err)
	_, err = Normalize("719000011111", "LB")
	assert.Error(t, err)
}

// An unknown default must fall back to Lebanon rather than failing: the
// caller supplying a bad ISO is a bug, but dropping the customer is worse.
func TestNormalize_UnknownDefaultFallsBackToLebanon(t *testing.T) {
	got, err := Normalize("71900001", "ZZ")
	require.NoError(t, err)
	assert.Equal(t, "+96171900001", got)
}

// ── The stored form ───────────────────────────────────────────────────────────

// Every normalised value must be a valid Twilio destination, because
// "whatsapp:" is concatenated straight onto it.
func TestNormalize_OutputIsAlwaysE164(t *testing.T) {
	for _, in := range []string{"71900001", "+971501234567", "0096171900001"} {
		got, err := Normalize(in, "LB")
		require.NoError(t, err)
		assert.True(t, IsE164(got), "%q -> %q is not E.164", in, got)
	}
}

func TestIsE164(t *testing.T) {
	assert.True(t, IsE164("+96171900001"))
	assert.False(t, IsE164("71900001"), "bare digits are what broke WhatsApp delivery")
	assert.False(t, IsE164("+961 71900001"))
	assert.False(t, IsE164(""))
}

func TestCountries_AreExposedForAPicker(t *testing.T) {
	cs := Countries()
	assert.Len(t, cs, 11)
	assert.Equal(t, "LB", cs[0].ISO, "Lebanon is the home market and must lead the list")
}
