// database.go creates and validates the PostgreSQL connection pool.
package config

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// NewDatabase creates a pgx connection pool and verifies connectivity.
// Reads DB_HOST, DB_PORT, DB_NAME, DB_USER, DB_PASSWORD from environment.
func NewDatabase(logger *zap.Logger) (*pgxpool.Pool, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s dbname=%s user=%s password=%s sslmode=disable",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
	)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to create database pool: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Info("Database connected",
		zap.String("host", os.Getenv("DB_HOST")),
		zap.String("port", os.Getenv("DB_PORT")),
		zap.String("database", os.Getenv("DB_NAME")),
	)

	return pool, nil
}
