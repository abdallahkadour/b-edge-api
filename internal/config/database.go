// database.go creates and validates the PostgreSQL connection pool.
package config

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// maxDBConns caps how many concurrent connections the pool opens to
// Postgres. Left unset, pgx defaults to max(4, NumCPU) - on the planned
// t3.medium (2 vCPU) that's ~4, which was never a deliberate choice, just
// whatever CPU count the container happens to see. Tuned instead to a size
// that leaves Postgres - colocated on the same small box per the current
// infra plan - enough of its own RAM: each pool connection costs Postgres a
// few MB of backend-process overhead, so 20 is a deliberate, generous but
// bounded ceiling. Revisit once Postgres moves off the API box.
const maxDBConns = 20

// minDBConns keeps a small number of connections warm so the first request
// after an idle period doesn't pay the connection-setup cost.
const minDBConns = 2

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

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}
	poolConfig.MaxConns = maxDBConns
	poolConfig.MinConns = minDBConns

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
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
		zap.Int32("max_conns", poolConfig.MaxConns),
	)

	return pool, nil
}
