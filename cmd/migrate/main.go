// Package main runs database migrations using golang-migrate.
// Usage: go run cmd/migrate/main.go
// Test:  TEST_DB=true go run cmd/migrate/main.go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	dbName := os.Getenv("DB_NAME")
	if os.Getenv("TEST_DB") == "true" {
		dbName = os.Getenv("TEST_DB_NAME")
	}

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		dbName,
	)

	m, err := migrate.New("file://db/migrations", dsn)
	if err != nil {
		log.Fatalf("Failed to initialise migrations: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		if err == migrate.ErrNoChange {
			log.Println("No new migrations to apply")
			return
		}
		log.Fatalf("Migration failed: %v", err)
	}

	log.Println("Migrations applied successfully")
}
