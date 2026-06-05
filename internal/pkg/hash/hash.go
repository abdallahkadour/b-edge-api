// Package hash provides bcrypt password hashing and verification.
package hash

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

// productionCost is the bcrypt work factor used in production.
// Higher = slower = more secure.
const productionCost = 10

// testCost is the bcrypt work factor used in tests for speed.
const testCost = 1

// cost returns the appropriate bcrypt cost for the current environment.
func cost() int {
	if os.Getenv("APP_ENV") == "test" {
		return testCost
	}
	return productionCost
}

// Password hashes a plaintext password using bcrypt.
// Uses testCost in test environment for speed, productionCost otherwise.
func Password(plaintext string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(plaintext), cost())
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hashed), nil
}

// VerifyPassword checks a plaintext password against a bcrypt hash.
// Returns nil if they match. Returns bcrypt.ErrMismatchedHashAndPassword
// if they do not match — caller must not expose this distinction to the user.
func VerifyPassword(plaintext, hashed string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plaintext))
}
