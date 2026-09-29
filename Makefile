POSTGRES_PORT ?= 5432
export TEST_DATABASE_URL ?= postgres://boarding_pass:boarding_pass@127.0.0.1:$(POSTGRES_PORT)/boarding_pass?sslmode=disable
SWAG_VERSION ?= v2.0.0-rc6
DOC_DIR ?= docs
SWAG_DIRS ?= internal/api,internal/bcbp,internal/store
SWAG_MAIN ?= api.go
SWAG = go run github.com/swaggo/swag/v2/cmd/swag@$(SWAG_VERSION) init \
	--v3.1 --parseInternal --parseDependency=false --parseDepth=1 --requiredByDefault \
	--outputTypes json,yaml -d $(SWAG_DIRS) -g $(SWAG_MAIN)

.PHONY: run build docs docs-check test test-integration check check-docker fuzz
run:
	go run .
build:
	go build -trimpath -o bin/boarding-pass .
docs:
	@echo "Generating API documentation into $(DOC_DIR) using Swag $(SWAG_VERSION)"
	@$(SWAG) -o $(DOC_DIR)
	@echo "Docs generated to $(DOC_DIR)/swagger.json and $(DOC_DIR)/swagger.yaml"
docs-check:
	@set -eu; output=$$(mktemp -d); trap 'rm -rf "$$output"' EXIT; \
		$(SWAG) --quiet --output "$$output"; \
		for file in swagger.json swagger.yaml; do \
			diff -u "$(DOC_DIR)/$$file" "$$output/$$file" || { echo 'API documentation is outdated; run make docs.'; exit 1; }; \
		done
test:
	go test -race ./...
test-integration:
	go test -race -tags=integration ./...
check: docs-check
	go vet -tags=integration ./...
	go test -race -tags=integration -coverpkg=./... -coverprofile=coverage.out ./...
# Tests use their own Compose project and never publish a database port.
check-docker:
	@trap 'docker compose -f compose.test.yaml down --volumes' EXIT; \
		docker compose -f compose.test.yaml up --build --abort-on-container-exit --exit-code-from check --attach check
fuzz:
	go test ./internal/bcbp -fuzz=FuzzParse -fuzztime=10s
