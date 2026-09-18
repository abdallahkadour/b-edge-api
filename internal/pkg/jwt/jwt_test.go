package jwt

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The whole authentication model rests on this package, and it had no tests.
// Security test AUTH-04 exercises the same properties over HTTP; these pin
// them at the unit level, where a regression is caught before it ships.

const (
	testSecret        = "test-jwt-secret-at-least-32-characters-long"
	testRefreshSecret = "test-refresh-secret-at-least-32-chars-long"
)

func withSecrets(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", testSecret)
	t.Setenv("JWT_REFRESH_SECRET", testRefreshSecret)
}

// ── Generation and round trip ─────────────────────────────────────────────────

func TestAccessToken_RoundTripsWithEveryClaim(t *testing.T) {
	withSecrets(t)
	userID, salonID := uuid.New(), uuid.New()

	tok, err := GenerateAccessToken(userID, &salonID, "artist")
	require.NoError(t, err)

	claims, err := VerifyAccessToken(tok)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.UserID)
	require.NotNil(t, claims.SalonID)
	assert.Equal(t, salonID, *claims.SalonID)
	assert.Equal(t, "artist", claims.Role)
	assert.Equal(t, issuer, claims.Issuer)
}

// Platform admins are not tied to a salon. A nil salon must survive the round
// trip as nil rather than becoming a zero UUID, which would read as
// "belongs to salon 00000000-..." to every ownership check downstream.
func TestAccessToken_NilSalonSurvivesAsNil(t *testing.T) {
	withSecrets(t)

	tok, err := GenerateAccessToken(uuid.New(), nil, "admin")
	require.NoError(t, err)

	claims, err := VerifyAccessToken(tok)
	require.NoError(t, err)
	assert.Nil(t, claims.SalonID)
	assert.Equal(t, "admin", claims.Role)
}

func TestRefreshToken_RoundTripsTheUserID(t *testing.T) {
	withSecrets(t)
	userID := uuid.New()

	tok, err := GenerateRefreshToken(userID)
	require.NoError(t, err)

	got, err := VerifyRefreshToken(tok)
	require.NoError(t, err)
	assert.Equal(t, userID, got)
}

func TestGenerateTokenPair_ReturnsBothAndBothVerify(t *testing.T) {
	withSecrets(t)
	userID, salonID := uuid.New(), uuid.New()

	pair, err := GenerateTokenPair(userID, &salonID, "artist")
	require.NoError(t, err)
	require.NotEmpty(t, pair.AccessToken)
	require.NotEmpty(t, pair.RefreshToken)

	_, err = VerifyAccessToken(pair.AccessToken)
	assert.NoError(t, err)
	_, err = VerifyRefreshToken(pair.RefreshToken)
	assert.NoError(t, err)
}

// Two tokens for the same user must differ, or a leaked token could not be
// told apart from a legitimate reissue. The jti is what guarantees it.
func TestAccessToken_IsUniquePerIssue(t *testing.T) {
	withSecrets(t)
	userID := uuid.New()

	a, err := GenerateAccessToken(userID, nil, "artist")
	require.NoError(t, err)
	b, err := GenerateAccessToken(userID, nil, "artist")
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
}

// ── The separation that matters ───────────────────────────────────────────────

// Access and refresh tokens are signed with DIFFERENT secrets. If that ever
// collapsed, a 15-minute access token would be accepted as a 7-day refresh
// token and vice versa - the single most damaging mistake this package could
// make, and completely invisible in normal use.
func TestTokens_AreNotInterchangeable(t *testing.T) {
	withSecrets(t)
	userID := uuid.New()

	access, err := GenerateAccessToken(userID, nil, "artist")
	require.NoError(t, err)
	refresh, err := GenerateRefreshToken(userID)
	require.NoError(t, err)

	_, err = VerifyRefreshToken(access)
	assert.Error(t, err, "an access token must not verify as a refresh token")

	_, err = VerifyAccessToken(refresh)
	assert.Error(t, err, "a refresh token must not verify as an access token")
}

// ── Tampering ─────────────────────────────────────────────────────────────────

func TestVerifyAccessToken_RejectsAForeignSecret(t *testing.T) {
	withSecrets(t)
	tok, err := GenerateAccessToken(uuid.New(), nil, "artist")
	require.NoError(t, err)

	t.Setenv("JWT_SECRET", "a-completely-different-secret-value-here")

	_, err = VerifyAccessToken(tok)
	assert.Error(t, err)
}

// alg:none is the classic JWT attack. The keyfunc rejects any non-HMAC method,
// which is what stops it - and stops RS/HS confusion in the same line.
func TestVerifyAccessToken_RejectsAlgNone(t *testing.T) {
	withSecrets(t)
	claims := Claims{
		UserID: uuid.New(),
		Role:   "admin",
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    issuer,
		},
	}
	unsigned, err := gojwt.NewWithClaims(gojwt.SigningMethodNone, claims).
		SignedString(gojwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = VerifyAccessToken(unsigned)
	assert.Error(t, err)
}

// Escalating the role in the payload must invalidate the signature.
func TestVerifyAccessToken_RejectsATamperedRole(t *testing.T) {
	withSecrets(t)
	tok, err := GenerateAccessToken(uuid.New(), nil, "artist")
	require.NoError(t, err)

	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3)
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	payload["role"] = "admin"
	mutated, err := json.Marshal(payload)
	require.NoError(t, err)
	parts[1] = base64.RawURLEncoding.EncodeToString(mutated)

	_, err = VerifyAccessToken(strings.Join(parts, "."))
	assert.Error(t, err)
}

func TestVerifyAccessToken_RejectsAnExpiredToken(t *testing.T) {
	withSecrets(t)
	claims := Claims{
		UserID: uuid.New(),
		Role:   "artist",
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(-time.Minute)),
			IssuedAt:  gojwt.NewNumericDate(time.Now().Add(-time.Hour)),
			Issuer:    issuer,
		},
	}
	expired, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).
		SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = VerifyAccessToken(expired)
	assert.Error(t, err)
}

func TestVerifyAccessToken_RejectsGarbage(t *testing.T) {
	withSecrets(t)
	for _, bad := range []string{"", "a.b.c", "not-a-token", "....", strings.Repeat("x", 500)} {
		_, err := VerifyAccessToken(bad)
		assert.Error(t, err, "expected %q to be rejected", bad)
	}
}

// A refresh token whose subject is not a UUID must be refused rather than
// yielding uuid.Nil, which would authenticate as the zero user.
func TestVerifyRefreshToken_RejectsANonUUIDSubject(t *testing.T) {
	withSecrets(t)
	tok, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, gojwt.RegisteredClaims{
		Subject:   "not-a-uuid",
		ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString([]byte(testRefreshSecret))
	require.NoError(t, err)

	got, err := VerifyRefreshToken(tok)
	assert.Error(t, err)
	assert.Equal(t, uuid.Nil, got)
}

// ── Configuration ─────────────────────────────────────────────────────────────

func TestGenerateAccessToken_RefusesWithoutASecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")

	_, err := GenerateAccessToken(uuid.New(), nil, "artist")
	assert.Error(t, err)
}

// Verification must fail CLOSED on an empty secret.
//
// This previously did not check, so an empty JWT_SECRET meant a token signed
// with an empty key verified - turning a misconfiguration into a complete
// authentication bypass instead of an outage. config.ValidateEnv refuses to
// boot without a long-enough secret, so it was never reachable in a running
// process, but a security package should not depend on a caller three layers
// away remembering to check. Found while writing this file.
func TestVerifyAccessToken_RefusesAnEmptySecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")

	forged, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, Claims{
		UserID: uuid.New(),
		Role:   "admin",
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    issuer,
		},
	}).SignedString([]byte(""))
	require.NoError(t, err)

	_, err = VerifyAccessToken(forged)
	assert.Error(t, err, "an empty secret must reject every token, not accept forged ones")
}

// The refresh path matters more, not less: a forged refresh token is good for
// seven days rather than fifteen minutes.
func TestVerifyRefreshToken_RefusesAnEmptySecret(t *testing.T) {
	t.Setenv("JWT_REFRESH_SECRET", "")

	forged, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, gojwt.RegisteredClaims{
		Subject:   uuid.New().String(),
		ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString([]byte(""))
	require.NoError(t, err)

	_, err = VerifyRefreshToken(forged)
	assert.Error(t, err)
}

func TestTokenDurations_MatchTheDocumentedPolicy(t *testing.T) {
	assert.Equal(t, 15*time.Minute, accessTokenDuration)
	assert.Equal(t, 7*24*time.Hour, refreshTokenDuration)
}
