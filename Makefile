.PHONY: run dev test coverage migrate migrate-test swagger build docker-up docker-down lint

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
