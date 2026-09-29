POSTGRES_PORT ?= 5432
export TEST_DATABASE_URL ?= postgres://boarding_pass:boarding_pass@127.0.0.1:$(POSTGRES_PORT)/boarding_pass?sslmode=disable

.PHONY: run build test test-integration check check-docker fuzz
run:
	go run .
build:
	go build -trimpath -o bin/boarding-pass .
test:
	go test -race ./...
test-integration:
	go test -race -tags=integration ./...
check:
	go vet -tags=integration ./...
	go test -race -tags=integration -coverpkg=./... -coverprofile=coverage.out ./...
# Tests use their own Compose project and never publish a database port.
check-docker:
	@trap 'docker compose -f compose.test.yaml down --volumes' EXIT; \
		docker compose -f compose.test.yaml up --build --abort-on-container-exit --exit-code-from check --attach check
fuzz:
	go test ./internal/bcbp -fuzz=FuzzParse -fuzztime=10s
