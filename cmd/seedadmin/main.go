// Package main provides the ONLY way an admin account can ever come into
// existence in B-Edge.
//
// There is no HTTP endpoint that creates one - POST /auth/register's own
// validator hard-rejects role=admin (`validate:"required,oneof=customer
// artist"`), by design. Admin is a small, deliberately powerful role, and
// making it reachable from a public endpoint - even one that happened to
// require some other check - would be the wrong shape for something this
// sensitive. Creation is a deliberate, out-of-band, operator-run action,
// not a product feature.
//
// Usage:
//
//	go run cmd/seedadmin/main.go -email you@b-edge.com -name "Abdallah"
//
// Prompts for a password interactively rather than accepting it as a flag,
// so it never lands in shell history.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/term"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/hash"
)

// maxAdmins is a hard, deliberate cap. Not a policy that could plausibly
// need raising later without a real conversation first - going from "at
// most 2 people can approve a live artist listing and see every admin
// action ever taken" to "at most 20" is a genuine change in what kind of
// system this is, not a config tweak. Raise this number only as a
// conscious code change, never as a runtime setting.
const maxAdmins = 2

func main() {
	email := flag.String("email", "", "admin's email address")
	name := flag.String("name", "", "admin's display name")
	flag.Parse()

	if *email == "" || *name == "" {
		log.Fatal("usage: go run cmd/seedadmin/main.go -email you@b-edge.com -name \"Your Name\"")
	}

	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_NAME"),
	)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND deleted_at IS NULL`,
	).Scan(&count); err != nil {
		log.Fatalf("count existing admins: %v", err)
	}
	if count >= maxAdmins {
		log.Fatalf("refusing to create another admin: %d already exist (hard cap: %d).\n"+
			"If this is a genuine, deliberate change, it is a code change to "+
			"maxAdmins in this file, not something this tool will do for you.",
			count, maxAdmins)
	}

	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`, *email,
	).Scan(&exists); err != nil {
		log.Fatalf("check existing email: %v", err)
	}
	if exists {
		log.Fatalf("a user with email %s already exists", *email)
	}

	fmt.Print("Password (min 8 characters, not echoed): ")
	pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		log.Fatalf("read password: %v", err)
	}
	password := strings.TrimSpace(string(pwBytes))
	if len(password) < 8 {
		log.Fatal("password must be at least 8 characters")
	}

	fmt.Printf("About to create ADMIN account: %s <%s> (%d of %d admin slots used after this). Continue? [y/N] ",
		*name, *email, count+1, maxAdmins)
	reader := bufio.NewReader(os.Stdin)
	confirm, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
		fmt.Println("Aborted.")
		return
	}

	hashed, err := hash.Password(password)
	if err != nil {
		log.Fatalf("hash password: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (name, email, password_hash, role, status) VALUES ($1, $2, $3, 'admin', 'active')`,
		*name, *email, hashed,
	); err != nil {
		log.Fatalf("create admin: %v", err)
	}

	fmt.Printf("Admin account created: %s <%s>\n", *name, *email)
	fmt.Println("They can now log in at the normal artist-dashboard /login " +
		"screen (same email+password flow) - the admin review screen will " +
		"appear because of their role, not a separate login.")
}
