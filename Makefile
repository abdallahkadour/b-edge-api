.PHONY: run dev test coverage migrate migrate-test swagger build docker-up docker-down lint docs-check docs-facts verify-uc1 verify-uc2 verify-uc6 verify-uc7 e2e-suite22 verify verify-security

run:
	go run cmd/main.go

dev:
	air

test:
	go test ./...

coverage:
	go test ./... -cover

migrate:
	go run cmd/migrate/main.go

migrate-test:
	TEST_DB=true go run cmd/migrate/main.go

swagger:
	swag init -g cmd/main.go -o docs

build:
	go build -o bin/b-edge cmd/main.go

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

lint:
	golangci-lint run

# Fails when the documentation contradicts the code. See scripts/check-docs.sh
# for why this is a failing check rather than a report: docs here go stale
# precisely because nothing else fails when they do.
docs-check:
	./scripts/check-docs.sh

# The raw facts the docs make claims about, as key=value.
docs-facts:
	./scripts/doc-facts.sh

# Executable verification of UC-1 (guest booking) against a running stack.
# Exits non-zero on any failure. Needs the API on :3000 and bedge-postgres up.
# See project-docs/B-Edge-UC1-Guest-Booking-Verification-v1.md.
verify-uc1:
	python3 scripts/verify-uc1.py

# Money: deposits and refunds (UC-2/UC-3). Needs the API and database up.
verify-uc2:
	python3 scripts/verify-uc2.py

# Every executable verification suite.
verify: verify-uc1 verify-uc2 verify-uc6 verify-uc7

# Subscription enforcement across all three layers (UC-6).
verify-uc6:
	python3 scripts/verify-uc6.py

## verify-uc7: the salon owner/member authorisation boundary
##
## Borrows one real salon: it moves salons.owner_id to a stand-in, logs the
## same account back in as a member, attempts every owner-only write, and puts
## ownership back in a finally block. Run it against a stack you are willing to
## have briefly mutated.
verify-uc7:
	python3 scripts/verify-uc7.py

## e2e-suite22: the multi-artist salon acceptance test
##
## Drives the whole flow against a running stack: the owner invites, the
## invitee accepts, an admin approves, the member sets their own hours, a
## customer sees them narrowed, and removal refuses to strand a booking.
##
## It MUTATES the database: it registers an account, puts a second artist in
## the launch artist's salon, and writes a booking. Everything is undone in a
## finally block, except the audit_events rows - those are immutable history
## and the test account is soft-deleted rather than removed because of them.
## Run it against a stack you are willing to have briefly changed.
e2e-suite22:
	python3 scripts/e2e-suite22.py

# Security plan §3.4b, executable. Needs the API and database up.
# See project-docs/B-Edge-Test-Execution-2026-09-21.md for the last run.
verify-security:
	python3 scripts/security-batch1.py
	python3 scripts/security-batch2.py
