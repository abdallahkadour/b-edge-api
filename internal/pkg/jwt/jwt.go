// Package jwt provides JWT token generation and validation for B-Edge authentication.
// Access tokens expire in 15 minutes. Refresh tokens expire in 7 days.
package jwt

import (
	"fmt"
	"os"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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
	gojwt.RegisteredClaims
}

// TokenPair holds an access token and a refresh token.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// GenerateAccessToken creates a signed JWT access token for the given user.
// The token embeds user_id, salon_id, and role so handlers never query the DB for this.
func GenerateAccessToken(userID uuid.UUID, salonID *uuid.UUID, role string) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return "", fmt.Errorf("JWT_SECRET is not configured")
	}

	claims := Claims{
		UserID:  userID,
		SalonID: salonID,
		Role:    role,
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(accessTokenDuration)),
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
			Issuer:    issuer,
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
// It only embeds the user ID — no business data.
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
	}

	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("failed to sign refresh token: %w", err)
	}
	return signed, nil
}

// GenerateTokenPair creates both an access token and a refresh token for a user.
func GenerateTokenPair(userID uuid.UUID, salonID *uuid.UUID, role string) (*TokenPair, error) {
	accessToken, err := GenerateAccessToken(userID, salonID, role)
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
