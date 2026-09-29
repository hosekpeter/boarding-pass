# Boarding Pass API (Go)

A Go implementation of the [boarding pass parsing assignment](https://github.com/mcyprian/interview_task/blob/main/Boarding_Pass_Parsing_Task.md).
Upload a PDF containing a PDF417 barcode, decode the IATA BCBP payload, resolve
its airports through the Skypicker Locations API, and query the saved history.
The assignment originally asks for Python; this implementation deliberately uses Go.

## Quick start: Docker only

Install **Docker with Compose v2** (Docker Desktop includes both). Run all
commands from this repository's root. You do not need Go, Python, PostgreSQL,
Poppler, or Make installed on your computer for the Docker commands below.
The first build downloads dependencies and may take a few minutes.

Start the API and PostgreSQL:

```bash
docker compose up --build -d --wait
```

Check that the API is ready:

```bash
curl --fail --retry 10 --retry-connrefused --retry-delay 1 http://localhost:8080/healthz
```

Expected response: `{"status":"ok"}`. Then try the included boarding pass:

```bash
curl --fail-with-body \
  -F 'file=@testdata/boarding_pass.pdf;type=application/pdf' \
  http://localhost:8080/boarding-pass/parse-from-file

curl --fail-with-body 'http://localhost:8080/boarding-passes?limit=20&offset=0'
```

The first request returns `decoded_bcbp` for `CYPRIAN/MICHAL`, route `KSC` → `PRG`;
the second returns the saved record. Airport enrichment requires access to
`api.skypicker.com`. No API key or manual database setup is needed; migrations
run automatically. On Windows PowerShell, use `curl.exe` and put each command
on one line (the backslashes above are POSIX shell line continuations).

View logs (Ctrl+C stops following them) or stop the application:

```bash
docker compose logs -f api
docker compose down
```

History survives restarts in the `postgres-data` volume. To deliberately delete
that history and start with an empty database, use `docker compose down --volumes`.

## Run all checks: Docker only

This command builds a test image containing Go and Poppler, starts a separate
PostgreSQL database, and runs static checks, all unit/integration tests, the Go
race detector, and coverage:

```bash
docker compose -f compose.test.yaml up --build --abort-on-container-exit --exit-code-from check --attach check
```

Exit code **0** means all checks passed; a nonzero exit code means a failure.
The command shows the test output; database logs remain available through
`docker compose -f compose.test.yaml logs db`. Both containers stop when the checks finish. This uses a separate Compose project
with **no published ports**, so it can run alongside the application or another
local PostgreSQL. Tests use a local airport API stub, not the live Skypicker API.
Building images and downloading dependencies still require internet access.
Allow a few minutes for the first run; PDF tests with the race detector are slower
than unit tests. No prior `docker compose up` is needed.

Remove the stopped test containers and their disposable database:

```bash
docker compose -f compose.test.yaml down --volumes
```

If Make is installed, `make check-docker` runs the same checks and cleans up the
test containers automatically. The local command `make check` is different: it
requires Go, Poppler, and a running PostgreSQL, as explained below.

Docker coverage is stored in the stopped `check` container at `/src/coverage.out`.
To keep it, run this **before** the cleanup command (or use local `make check`):

```bash
docker compose -f compose.test.yaml cp check:/src/coverage.out ./coverage.out
```

## Troubleshooting

| Symptom | What to do |
| --- | --- |
| Docker cannot connect to its daemon | Start Docker Desktop or the Docker service. |
| Port 8080 or 5432 is already in use | Set `API_PORT=8081` and/or `POSTGRES_PORT=55432` in a `.env` file in the repository root, then rerun Compose. Use the chosen API port in curl commands. |
| `make check` cannot connect to PostgreSQL | Use the Docker-only check command above, or start `docker compose up -d --wait db` before local checks. |
| `pdfinfo` or `pdftoppm` is missing | Use Docker, or install Poppler for local development. |
| Upload returns `502 locations_unavailable` | Check access to `api.skypicker.com`; `/healthz` checks process liveness, not upstream services. |
| Build fails while downloading an image or Go module | Check Docker's network/proxy settings and retry. |

A `.env` file supplies Compose settings; it is not automatically loaded by a
local Go process or Make. For local commands, pass matching ports explicitly,
e.g. `make check POSTGRES_PORT=55432` or `DB_PORT=55432 go run .`.

## Local Go development (optional)

Requires **Go 1.27+**, **Poppler** (`pdfinfo`, `pdftoppm`) on `PATH`, and
**PostgreSQL 18**. The Make commands use GNU Make; race tests also need a C
compiler (for example GCC on Linux or the Xcode command-line tools on macOS).
There is no Python runtime or virtual environment to prepare.

```bash
# Debian/Ubuntu; on macOS install Poppler with: brew install poppler
sudo apt-get install poppler-utils build-essential

docker compose up -d --wait db
go mod download
go run .
```

The API listens on `127.0.0.1:8080`; the Compose database is available on
`127.0.0.1:5432`. If you already started the full Docker application, stop its
API with `docker compose stop api` before using that port for `go run .`.
Stop the local Go process with Ctrl+C; active requests get up to 50 seconds to finish.

## Configuration

The defaults use database/user/password `boarding_pass`. These are local
development credentials; set the `DB_*` variables below for your own PostgreSQL
instance. The target database and selected schema must already exist. Startup
applies embedded Goose migrations, so the database user needs permission to
create tables and indexes in that schema.

The API container runs as a non-root user with a read-only root filesystem,
512 MiB of memory, and a 128 MiB temporary filesystem. PostgreSQL has its own
writable data volume mounted at `/var/lib/postgresql` for PostgreSQL 18.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `HTTP_ADDR` | `127.0.0.1:8080` | HTTP bind address; the container uses `:8080` |
| `DB_HOST` | `127.0.0.1` | PostgreSQL host; Compose uses `db` |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_USER` | `boarding_pass` | Database user |
| `DB_PASSWORD` | `boarding_pass` | Database password |
| `DB_NAME` | `boarding_pass` | Database name |
| `DB_SSLMODE` | `disable` | PostgreSQL TLS mode |
| `DB_SCHEMA` | `public` | Existing schema for tables and migration history |
| `DB_MAX_CONNS` | `20` | Maximum pool connections |
| `DB_MIN_CONNS` | `2` | Minimum pool connections |
| `DB_STATEMENT_TIMEOUT` | `5s` | Server statement timeout (at least 1 ms) |
| `DB_CONNECT_TIMEOUT` | `30s` | Startup connection retry deadline; also bounds migrations |
| `LOCATIONS_BASE_URL` | `https://api.skypicker.com` | Airport API base URL; override for a local stub |

Configuration uses `caarlos0/env` and `go-playground/validator`.
Unset or empty environment variables use the defaults. Invalid settings fail
before PDF tools or the database are initialized, with errors naming the affected
variables without printing connection credentials. `HTTP_ADDR` accepts `host:port`,
`:port`, and bracketed IPv6 addresses; port 0 lets the OS choose an available port.
`config.DBConfig.ConnectionString()` builds the PostgreSQL URL from these fields
and escapes credentials and database names. Configure `DB_SSLMODE` for your
server; the local Compose database uses `disable`. `LOCATIONS_BASE_URL` must be an absolute HTTP(S) URL
without credentials, query parameters or a fragment.

## Use the API

```bash
curl --fail-with-body \
  -F 'file=@testdata/boarding_pass.pdf;type=application/pdf' \
  http://localhost:8080/boarding-pass/parse-from-file

curl --fail-with-body \
  'http://localhost:8080/boarding-passes?limit=20&offset=0&passenger_name=cyprian&airline_code=FR'
```

The parse endpoint responds with `{"decoded_bcbp": {...}}`. The included example
contains passenger `CYPRIAN/MICHAL`, flight `FR 7774`, route `KSC` to `PRG`, Julian
day `105`, economy seat `29C`, and booking reference `ZKN85B`.
Airport names and country names come from the live API; for example it currently
uses `Czechia` rather than the assignment example's `Czech Republic`.

History responds with `items`, `total`, `limit`, and `offset`. Each item has a UUID,
a UTC `parsed_at` timestamp, and `decoded_bcbp`. Results are newest first.
Text filters reject invalid UTF-8 and NUL characters with HTTP 400.
Passenger names use a case-insensitive literal substring match, while airline
codes use an exact, case-sensitive match on any leg. Both filters must match when
combined. `total` is counted before pagination. No matches return `items: []`.
`limit` defaults to 20 and accepts 1–100; `offset` defaults to 0.
Each successful POST creates a new history record, even for a repeated upload.

The [OpenAPI 3.0 specification](docs/openapi.yaml) is also served at
`GET /openapi.yaml`. `GET /healthz` reports process liveness only.

Errors use a consistent JSON envelope:

```json
{"error":{"code":"barcode_not_found","message":"No readable PDF417 barcode found"}}
```

| Status | Meaning |
| --- | --- |
| 400 | Malformed multipart, missing/duplicate file, empty upload, or invalid query |
| 413 | Multipart request exceeds 10 MiB, including its framing |
| 415 | Wrong request type or uploaded bytes do not begin with a PDF header |
| 422 | Corrupt/encrypted PDF, over 10 pages, unreadable/ambiguous barcode, or invalid BCBP |
| 502 | Airport API failed or did not return complete airport information |
| 503 | Both parser slots are occupied; `Retry-After: 1` |
| 504 | Processing deadline exceeded |
| 500 | Internal processing or persistence failure |

## Local tests

All tests use `testify/assert` for result checks and `testify/require` for
prerequisites that must pass before the test can continue.

These commands run Go on your computer. For Docker-only checks, use the
[command above](#run-all-checks-docker-only). `make check` and
`make test-integration` **do not start PostgreSQL automatically**.

```bash
make test                 # Unit tests and the Go race detector; no database or Poppler needed

docker compose up -d --wait db
make test-integration     # PostgreSQL and real PDF tests; requires Poppler
make check                # Vet, all tests, race detector, coverage.out
make fuzz                 # Exercise the BCBP parser with generated malformed inputs
```

The Make targets default `TEST_DATABASE_URL` to the local Compose database URL,
using `POSTGRES_PORT` if set.
To use a separate database, supply it explicitly:

```bash
TEST_DATABASE_URL='postgres://user:password@localhost:5432/boarding_pass_test?sslmode=disable' make check
```

Direct `go test -tags=integration ./...` also requires `TEST_DATABASE_URL`.
Integration tests fail if the database is missing or unavailable. Each database
test creates a unique schema, selects it through `search_path`, and removes only
that schema at cleanup; existing application tables are untouched. The test user
needs permission to create schemas in the test database.

Tests do not depend on the live Locations API: HTTP test servers provide airport
responses, errors, and timeouts. The integration test goes through the real PDF
renderer and barcode decoder, HTTP API, airport client, and PostgreSQL, then
reopens the application scope and verifies the saved result. Additional PDF
integration tests generate vector barcodes with an independent encoder
(`boombuler/barcode`, test-only) to verify scanning later pages, duplicate
barcodes, distinct boarding passes in different orientations on the same page,
corrupt documents, and the page limit. Multi-leg API tests parse 2–4 flights from
generated PDFs, enrich their airports, save them, and filter by a later leg.
They also verify that a failed second airport lookup saves nothing and a retry
succeeds. Test PDF generation is shared in `internal/testutil/pdf.go`.
Tests cover malformed BCBP fields, multiple legs and optional sections, filter
combinations, persistence, concurrent writes and schema initialization, upload
limits, overload, and upstream failures. CI runs the same checks with Poppler
installed and a PostgreSQL service container.

## Design choices

The application uses `net/http` for routing and a shared `pgx/v5/pgxpool`
PostgreSQL pool, with a configurable maximum of 20 and minimum of 2 connections
by default. Records use UUID identifiers, `TIMESTAMPTZ` timestamps, and `JSONB`
for the decoded boarding pass. Queries use positional SQL parameters; the
passenger filter matches literal substrings and the airline filter checks every
leg with PostgreSQL JSONB functions.

History count and rows are read in one read-only `REPEATABLE READ` transaction,
so they share a consistent snapshot during concurrent writes. An index supports
newest-first ordering, with insertion sequence breaking timestamp ties.
Embedded [Goose migrations](internal/db/migrations) run transactionally at
startup under a PostgreSQL advisory lock, allowing concurrent application starts.
Goose tracks applied versions; the first migration creates the table and index.
Only migrations use a `database/sql` adapter over the same pool. Existing SQLite files from earlier versions are not read
or deleted by this implementation.

At startup, `main` loads configuration,
creates `dependencies.NewServiceScope(ctx, cfg)`, defers `scope.Close()`, and runs
`httpserver.New(scope, cfg).Start(ctx)`. The scope creates the logger, PDF
extractor, airport client, PostgreSQL connection pool, and parser. It exposes the parser and
history repository through interfaces and owns database cleanup. Configuration
loading does not connect to PostgreSQL; resource initialization belongs to the
scope. The HTTP server finishes in-flight requests before the scope is closed.

The parser receives its extractor, airport lookup, and `store.Repository` directly
through its constructor. It has no dependency on the application scope. API
unit tests supply a fake scope, while the PDF integration test initializes the
real scope, closes it, and reopens it to check persistence. A single
`store.Repository` interface groups `Save` and `List`; PostgreSQL queries and
driver types stay inside its implementation.

The request flows through small packages:

1. `internal/api` validates multipart input, bounds request size and concurrency,
   handles status codes, and serves documentation.
2. `internal/pdf` renders every page using Poppler and decodes PDF417 with
   [`zxinggo`](https://github.com/ericlevine/zxinggo). Rendering handles both image
   and vector barcodes. Pages are limited to 2400 pixels on their longest side.
3. `internal/bcbp` parses fixed-width format M fields for 1–4 legs and respects
   the hexadecimal lengths of optional sections. Field positions and widths are
   named in `internal/bcbp/layout.go`; `Parse` delegates header, flight-leg, and
   trailing security-block handling to separate functions.
4. `internal/client/skypicker` is a reusable HTTP client with its own `Airport`
   type, a five-second timeout, and a bounded 24-hour cache of successful lookups.
   It has no dependency on the boarding-pass model; `internal/service` maps the
   client response into that model. The scope constructs one shared client.
5. `internal/service` enriches every leg before `internal/store` commits the result.

Only decoded, enriched data is stored. Uploaded PDFs and rendered pages are
removed after each request. Filenames supplied by the client are never used as
filesystem paths. Logs do not include passenger names, booking references, raw
barcodes, request bodies, or query strings. Responses use `Cache-Control: no-store`.

## Scope and tradeoffs

- A document may contain multiple flight legs in one BCBP, or repeated copies of
  the same barcode. Distinct barcodes are rejected because the response represents
  one boarding pass; batch uploads would need a separate response contract.
- Conditional/airline fields and security data are consumed by their lengths but
  are not exposed or cryptographically verified. This service parses data; it
  does not establish that a boarding pass is genuine or authorize boarding.
- Dates retain the day of year. The mandatory BCBP fields do not specify a flight
  year, so the service does not guess one.
- Cabin codes F/P, C/J, W, and Y/M map to first, business, premium economy, and
  economy. Other airline-specific codes return `unknown`. Leading zero padding is
  removed from flight and seat numbers; blank seats stay blank.
- A 30-second context deadline kills Poppler subprocesses and bounds downstream
  calls. The in-process Go barcode decoder checks the deadline between bounded
  pages and barcode orientations; a scan already in progress is not preempted. Container limits add
  an OS-level resource bound. Tiny/damaged barcodes may require a higher rendering
  resolution or a different decoder in a future version.
- Missing airport details fail the parse with 502 instead of inventing data. Warm
  airport cache entries can serve requests during short upstream outages. The
  cache is per process and resets on restart.
- The API intentionally has no accounts. Localhost is the default. Before shared
  deployment, add authentication and per-user history authorization at the service
  boundary, TLS, and a retention policy for stored passenger information. These
  are deployment concerns outside the assignment's two-endpoint contract.

The BCBP field layout follows the
[IATA implementation guide](https://www.iata.org/contentassets/1dccc9ed041b4f3bbdcf8ee8682e75c4/2021_03_02-bcbp-implementation-guide-version-7-.pdf).
The original example PDF and its source are documented in [testdata](testdata/README.md).
