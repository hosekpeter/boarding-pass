POSTGRES_PORT ?= 5432
export TEST_DATABASE_URL ?= postgres://boarding_pass:boarding_pass@127.0.0.1:$(POSTGRES_PORT)/boarding_pass?sslmode=disable

.PHONY: run build test test-integration check check-docker fuzz bench bench-compare
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

# Run without -race or coverage; those flags distort performance measurements.
bench:
	go test ./benchmarks -run '^$$' -bench . -benchmem -benchtime=500ms -count=5 -cpu=1

PYTHON ?= python3
PYTHON_PROJECT ?= ../boarding-pass-python
bench-compare:
	"$(PYTHON)" benchmarks/compare.py --python-project "$(PYTHON_PROJECT)"
