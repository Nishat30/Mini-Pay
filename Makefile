# Makefile — Build, test, and development tasks for MiniPay.
# Owns: developer workflow targets.
# Must never: contain secrets or deployment logic.
# Depends on: Go, Docker Compose, make.

.PHONY: up down test test-integration lint sqlc migrate load demo build clean

# Start local development dependencies (Postgres, Redis).
up:
	docker-compose -f deploy/docker-compose.yml up -d
	@echo "Waiting for services to be healthy..."
	@timeout /t 3 /nobreak >nul 2>&1 || sleep 3
	@echo "Services are up. Run 'make migrate' to apply migrations."

# Stop local development dependencies.
down:
	docker-compose -f deploy/docker-compose.yml down

# Run unit tests with race detection.
test:
	go test -race -shuffle=on -count=1 ./...

# Run integration tests (requires running Postgres and Redis).
test-integration:
	go test -race -shuffle=on -count=1 -tags=integration ./...

# Run linter.
lint:
	golangci-lint run ./...
	go vet ./...

# Generate sqlc code from queries.
sqlc:
	sqlc generate

# Run database migrations up.
migrate:
	go run ./cmd/minipay migrate up

# Run load tests with k6.
load:
	k6 run test/k6/load.js

# Run the demo script.
demo:
	@powershell -ExecutionPolicy Bypass -File scripts/demo.ps1

# Build the binary.
build:
	go build -o bin/minipay ./cmd/minipay

# Clean build artifacts.
clean:
	rm -rf bin/

