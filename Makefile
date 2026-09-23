.PHONY: run dev test coverage migrate migrate-test swagger build docker-up docker-down lint docs-check docs-facts verify-uc1 verify-uc2 verify-uc6 verify-uc7 e2e-suite22 e2e-suite23 verify-security-salon chaos-booking verify verify-security

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

# NOTE: deliberately no -tags devbypass. The production binary must not
# contain the fixed OTP code. See internal/pkg/devbypass.
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

## e2e-suite23: a salon over time, with an adversary in it
##
## Six people, one continuous story: three artists joining and leaving, two
## customers booking, and an attacker going after the deposit reference -
## which on this platform is the entire financial attack surface, because
## money moves out of band to whatever OMT or Whish number the salon
## publishes.
##
## Builds and destroys its own salon rather than borrowing the launch
## artist's. Found the missing artists.status gate on the booking path
## (case 23.7e).
e2e-suite23:
	python3 scripts/e2e-suite23.py

## verify-security-salon: security plan section 3.4d
##
## The multi-artist salon attack surface: the invitation as a bearer
## credential, the owner/member boundary, and the per-artist rota writer.
## Builds its own salon; restores in a finally block.
##
## UNDECIDED is not a pass. It means a real behaviour was measured that needs
## a product decision, and the run exits 0 only because it is not a defect -
## read the lines.
verify-security-salon:
	python3 scripts/verify-security-salon.py

## chaos-booking: aggressive E2E against the booking state machine
##
## 3 salons x (3,3,2) artists + 2 solo artists + 20 customers, then attacks
## the state machine: payload mutation, simultaneous terminal transitions,
## reschedule into the past, clock tampering, cascading day shift with a
## concurrent cancel, mutual no-show, concurrent double refund, and a
## 20-client siege on one slot.
##
## Reports NOT APPLICABLE - never a pass - for the parts of a marketplace
## B-Edge deliberately does not have: gateway pre-auth, wallets, commission
## splits, payouts ledger, escrow, webhook idempotency.
##
## MUTATES the database and restores in a finally block.
chaos-booking:
	python3 scripts/chaos-booking.py

# Security plan §3.4b, executable. Needs the API and database up.
# See project-docs/B-Edge-Test-Execution-2026-09-21.md for the last run.
verify-security:
	python3 scripts/security-batch1.py
	python3 scripts/security-batch2.py

# Repository tests against a real PostgreSQL database.
#
# Behind a build tag so `make test` stays fast: these migrate a template
# database once and clone it per package, which costs a few seconds that the
# service-layer suite should not pay on every run. See internal/pkg/testdb.
test-db:
	go test -tags dbtest ./... -count=1

# Proves a notification can actually reach a handset.
#
# EXPECTED TO FAIL until WhatsApp business verification clears or
# TWILIO_SMS_FROM is provisioned. That failure is the point - it is the
# positive control for M1, the most important metric on the reliability
# scorecard, which has read 0 delivered for the life of the project.
#
#   make verify-delivery PHONE=+9617xxxxxxx
verify-delivery:
	@PHONE=$(PHONE) python3 scripts/verify-delivery.py

# Everything, in one target. Runs what CI should run.
verify-all: test test-db verify chaos-booking verify-security-salon
	@echo "  ── all suites complete ──"

# Mutation testing — the real measure of whether tests CONSTRAIN behaviour.
#
# Coverage says a line ran. Mutation says a line is constrained: gremlins
# changes the code and reports which changes the suite fails to notice. Every
# survivor is a line no test pins down.
#
# Why it matters here specifically: two tests in this repo were asserting a
# bug (the refund_due defect) and the suite was green and agreeing with them.
# Mutating `b.DepositAmount.IsPositive()` to a constant `true` survives that
# old suite in silence.
#
# Install: go install github.com/go-gremlins/gremlins/cmd/gremlins@latest
#
#   make mutation                      # the booking domain
#   make mutation PKG=./internal/billing/
mutation:
	@gremlins unleash $(or $(PKG),./internal/booking/)
