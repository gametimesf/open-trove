.PHONY: test build run watch clean lint docs integration-test test-all

BINARY=server

# ─── Docker-based (primary) ─────────────────────────────────────
run:
	docker compose up app

watch:
	docker compose watch app

build:
	docker compose build

test:
	docker compose run --rm test

lint:
	docker compose run --rm lint

# ─── Integration tests (docker-compose + minio) ────────────────
TEST_COMPOSE=COMPOSE_FILE=docker-compose.yml:docker-compose.test.yml

integration-test:
	HOST_PORT=0 $(TEST_COMPOSE) docker compose run --rm --build integration-test
	$(TEST_COMPOSE) docker compose down

# ─── All tests ─────────────────────────────────────────────────
test-all: lint test integration-test

# ─── Direct (if you prefer local go) ────────────────────────────
local-build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) ./cmd/server

local-test:
	go test ./... -v -count=1

local-run:
	go run ./cmd/server

local-lint:
	golangci-lint run ./...

# ─── Documentation ─────────────────────────────────────────────
docs:
	@if ! command -v swag &> /dev/null; then \
		echo "Installing swaggo..."; \
		go install github.com/swaggo/swag/cmd/swag@latest; \
	fi
	@echo "Generating Swagger v2 spec..."
	swag init -g cmd/server/main.go --output ./docs/swagger --parseDependency --parseInternal
	@echo "Validating generated files..."
	test -f ./docs/swagger/swagger.json
	test -f ./docs/swagger/swagger.yaml
	test -f ./docs/swagger/docs.go
	@echo "Converting Swagger v2 to OpenAPI v3..."
	npx --yes swagger2openapi ./docs/swagger/swagger.json -o ./docs/openapi.json --patch
	@echo "Documentation generation complete."

clean:
	docker compose down -v
	rm -rf bin/
