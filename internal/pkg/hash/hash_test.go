package hash

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// This package had no tests, which for password hashing is the worst place in
// the repo for a silent regression: every failure mode here is invisible
// (a hash still looks like a hash) and the consequence is account takeover.

func TestPassword_RoundTrips(t *testing.T) {
	hashed, err := Password("correct horse battery staple")
	require.NoError(t, err)

	assert.NoError(t, VerifyPassword("correct horse battery staple", hashed))
}

func TestVerifyPassword_RejectsTheWrongPassword(t *testing.T) {
	hashed, err := Password("correct horse battery staple")
	require.NoError(t, err)

	err = VerifyPassword("correct horse battery stapl", hashed)

	assert.ErrorIs(t, err, bcrypt.ErrMismatchedHashAndPassword)
}

// The plaintext must never be recoverable from, or equal to, the stored value.
// A regression that stored the password as-is would still round-trip through
// a naive equality check, so this asserts the property directly.
func TestPassword_NeverStoresThePlaintext(t *testing.T) {
	const pw = "correct horse battery staple"

	hashed, err := Password(pw)
	require.NoError(t, err)

	assert.NotEqual(t, pw, hashed)
	assert.NotContains(t, hashed, pw)
	assert.True(t, strings.HasPrefix(hashed, "$2"), "expected a bcrypt hash, got %q", hashed)
}

// Salting is what stops a stolen table being cracked in bulk. Two users with
// the same password must not share a hash.
func TestPassword_SaltsEachHashIndependently(t *testing.T) {
	a, err := Password("same password")
	require.NoError(t, err)
	b, err := Password("same password")
	require.NoError(t, err)

	assert.NotEqual(t, a, b, "identical passwords must not produce identical hashes")
	assert.NoError(t, VerifyPassword("same password", a))
	assert.NoError(t, VerifyPassword("same password", b))
}

// bcrypt silently ignores everything past 72 bytes in some implementations,
// which would make two different long passwords interchangeable. Go's returns
// an error instead. Pinned here because which of those two behaviours applies
// is a security property, not an implementation detail - and if a future
// upgrade switched to truncation, nothing else in the codebase would notice.
func TestPassword_RejectsOverlongPasswordsRatherThanTruncating(t *testing.T) {
	long := strings.Repeat("a", 80)
	variant := long[:72] + "DIFFERENT_TAIL"

	_, err := Password(long)
	if err == nil {
		// If this ever starts succeeding, the 72-byte tail must still matter.
		hashed, hErr := Password(long)
		require.NoError(t, hErr)
		assert.Error(t, VerifyPassword(variant, hashed),
			"two passwords differing only after 72 bytes must not be interchangeable")
		return
	}
	assert.ErrorIs(t, err, bcrypt.ErrPasswordTooLong)
}

// An empty password is not rejected by this package - the API's validation
// layer is what requires a minimum length. Pinned so the division of
// responsibility stays deliberate rather than becoming an accident.
func TestPassword_HashesAnEmptyStringWithoutError(t *testing.T) {
	hashed, err := Password("")
	require.NoError(t, err)

	assert.NoError(t, VerifyPassword("", hashed))
	assert.Error(t, VerifyPassword("x", hashed))
}

func TestVerifyPassword_RejectsAMalformedHash(t *testing.T) {
	assert.Error(t, VerifyPassword("anything", "not-a-bcrypt-hash"))
	assert.Error(t, VerifyPassword("anything", ""))
}

// The test cost exists for speed and must never be what production uses.
func TestCost_UsesTheProductionFactorOutsideTests(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	assert.Equal(t, productionCost, cost())

	t.Setenv("APP_ENV", "test")
	assert.Equal(t, testCost, cost())

	// Anything unrecognised must fall back to the SAFE value, not the fast one.
	t.Setenv("APP_ENV", "")
	assert.Equal(t, productionCost, cost())
	t.Setenv("APP_ENV", "Test")
	assert.Equal(t, productionCost, cost(), "the check is exact; 'Test' is not 'test'")
}

// A hash produced at the cheap test cost must still verify, or switching
// environments would lock every account created in the other one.
func TestPassword_VerifiesAcrossCostFactors(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	cheap, err := Password("portable")
	require.NoError(t, err)

	t.Setenv("APP_ENV", "production")
	assert.NoError(t, VerifyPassword("portable", cheap))
}
