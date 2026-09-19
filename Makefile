.PHONY: run dev test coverage migrate migrate-test swagger build docker-up docker-down lint docs-check docs-facts verify-uc1

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
