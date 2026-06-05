// Package config provides application configuration and initialisation helpers.
package config

import (
	"os"

	"go.uber.org/zap"
)

// NewLogger creates a configured Zap logger.
// Uses JSON production format when APP_ENV=production,
// human-readable development format otherwise.
func NewLogger() (*zap.Logger, error) {
	if os.Getenv("APP_ENV") == "production" {
		return zap.NewProduction()
	}
	return zap.NewDevelopment()
}
