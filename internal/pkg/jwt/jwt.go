// Package jwt provides JWT token generation and validation for B-Edge authentication.
// Access tokens expire in 15 minutes. Refresh tokens expire in 7 days.
package jwt

import (
	"fmt"
	"os"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// accessTokenDuration is the lifetime of a JWT access token.
const accessTokenDuration = 15 * time.Minute

// refreshTokenDuration is the lifetime of a JWT refresh token.
const refreshTokenDuration = 7 * 24 * time.Hour

// issuer identifies the token issuer in the JWT claims.
const issuer = "b-edge"

// Claims is the JWT payload for B-Edge access tokens.
// SalonID is nil for platform admins who are not tied to a specific salon.
type Claims struct {
	UserID  uuid.UUID  `json:"user_id"`
	SalonID *uuid.UUID `json:"salon_id,omitempty"`
	Role    string     `json:"role"`

	// SalonRole is the holder's standing within SalonID - owner or member.
	//
	// Distinct from Role, which is the platform role (artist, admin,
	// customer). An artist is always Role "artist"; whether they may change
	// the salon's prices depends on SalonRole.
	//
	// Derived from salons.owner_id at issue time by salonrole.Resolve and
	// never stored. Two consequences follow from embedding it rather than
	// looking it up per request:
	//
	//  1. Transferring ownership must invalidate both parties' tokens, or
	//     the former owner keeps owner capabilities until this access token
	//     expires - at most accessTokenDuration.
	//  2. A token issued before this field existed decodes with the zero
	//     value, salonrole.None, which holds no capabilities. Old tokens
	//     therefore lose salon writes rather than keeping them, which is the
	//     safe direction: the holder re-authenticates and gets a correct
	//     token. This is why the field is omitempty-free - an absent claim
	//     and an explicit None must mean the same thing.
	SalonRole salonrole.Role `json:"salon_role"`

	gojwt.RegisteredClaims
}

// TokenPair holds an access token and a refresh token.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// GenerateAccessToken creates a signed JWT access token for the given user.
// The token embeds user_id, salon_id, role and salon_role so handlers never
// query the DB for this.
//
// salonRole must come from salonrole.Resolve. An unrecognised value is
// coerced to salonrole.None rather than signed as-is, so a bug upstream
// cannot mint a token carrying a role the matrix does not know.
func GenerateAccessToken(userID uuid.UUID, salonID *uuid.UUID, role string, salonRole salonrole.Role) (string, error) {
	if !salonrole.Valid(salonRole) {
		salonRole = salonrole.None
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return "", fmt.Errorf("JWT_SECRET is not configured")
	}

	claims := Claims{
		UserID:    userID,
		SalonID:   salonID,
		Role:      role,
		SalonRole: salonRole,
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(accessTokenDuration)),
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
			Issuer:    issuer,
			ID:        uuid.New().String(),
		},
	}

	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("failed to sign access token: %w", err)
	}
	return signed, nil
}

// GenerateRefreshToken creates a signed long-lived JWT refresh token.
// It only embeds the user ID - no business data.
//
// The ID (jti) claim is a random UUID, not left empty. Without it, two
// refresh tokens generated for the same user within the same second are
// byte-for-byte identical - every claim (sub, iat, exp, iss) matches
// exactly, since JWT signing is deterministic. That's not just a
// theoretical concern: refresh_tokens.token_hash has a UNIQUE constraint,
// so a real collision (e.g. two refresh calls landing in the same second)
// would make the second StoreRefreshToken call fail with a genuine 23505
// constraint violation, not just "unlikely." Found via a test asserting
// token rotation actually changes the token, which caught this before it
// could surface as an intermittent production failure.
func GenerateRefreshToken(userID uuid.UUID) (string, error) {
	secret := os.Getenv("JWT_REFRESH_SECRET")
	if secret == "" {
		return "", fmt.Errorf("JWT_REFRESH_SECRET is not configured")
	}

	claims := gojwt.RegisteredClaims{
		Subject:   userID.String(),
		ExpiresAt: gojwt.NewNumericDate(time.Now().Add(refreshTokenDuration)),
		IssuedAt:  gojwt.NewNumericDate(time.Now()),
		Issuer:    issuer,
		ID:        uuid.New().String(),
	}

	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("failed to sign refresh token: %w", err)
	}
	return signed, nil
}

// GenerateTokenPair creates both an access token and a refresh token for a user.
func GenerateTokenPair(userID uuid.UUID, salonID *uuid.UUID, role string, salonRole salonrole.Role) (*TokenPair, error) {
	accessToken, err := GenerateAccessToken(userID, salonID, role, salonRole)
	if err != nil {
		return nil, err
	}

	refreshToken, err := GenerateRefreshToken(userID)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// VerifyAccessToken parses and validates a JWT access token.
// Returns the full Claims on success, error on invalid or expired token.
func VerifyAccessToken(tokenStr string) (*Claims, error) {
	secret := os.Getenv("JWT_SECRET")
	// Fail closed. Generation already refuses without a secret, but
	// verification did not check - so an empty JWT_SECRET meant tokens signed
	// with an empty key VERIFIED, turning a misconfiguration into a complete
	// authentication bypass rather than an outage.
	//
	// config.ValidateEnv refuses to boot without a long-enough secret, so this
	// was not reachable in a running process. It is here so the property holds
	// in this package rather than depending on a caller three layers away
	// remembering to check.
	if secret == "" {
		return nil, fmt.Errorf("JWT_SECRET is not configured")
	}

	token, err := gojwt.ParseWithClaims(tokenStr, &Claims{}, func(t *gojwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}

// VerifyRefreshToken parses and validates a JWT refresh token.
// Returns the user UUID on success, error on invalid or expired token.
func VerifyRefreshToken(tokenStr string) (uuid.UUID, error) {
	secret := os.Getenv("JWT_REFRESH_SECRET")
	// Same reasoning as VerifyAccessToken: fail closed rather than verifying
	// against an empty key. A forged refresh token is worse - it is good for
	// seven days, not fifteen minutes.
	if secret == "" {
		return uuid.Nil, fmt.Errorf("JWT_REFRESH_SECRET is not configured")
	}

	token, err := gojwt.ParseWithClaims(tokenStr, &gojwt.RegisteredClaims{}, func(t *gojwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid refresh token: %w", err)
	}

	claims, ok := token.Claims.(*gojwt.RegisteredClaims)
	if !ok || !token.Valid {
		return uuid.Nil, fmt.Errorf("invalid refresh token claims")
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid user ID in refresh token: %w", err)
	}

	return userID, nil
}
