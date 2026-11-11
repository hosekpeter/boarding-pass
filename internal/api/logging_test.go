package api_test

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"boarding-pass/internal/base"
	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/client/skypicker"
	"boarding-pass/internal/pdf"
	"boarding-pass/internal/service"
	"boarding-pass/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeLogs parses JSON log lines. encoding/json/v2 rejects duplicate keys,
// so a record with request_id twice fails the test.
func decodeLogs(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.Lines(logs.String()) {
		var record map[string]any
		require.NoError(t, jsonv2.Unmarshal([]byte(line), &record), line)
		records = append(records, record)
	}
	return records
}

func TestEveryLogRecordHasRequestID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		parser    func() *parserStub
		repo      *repoStub
		request   func(*testing.T) *http.Request
		requestID string
		records   int
	}{
		{name: "parse internal error", parser: failingParser(errors.New("renderer failed")), records: 2},
		{name: "parse locations unavailable", parser: failingParser(skypicker.ErrUnavailable), records: 2},
		{name: "parse timeout", parser: failingParser(context.DeadlineExceeded), records: 2},
		{name: "client request ID", parser: failingParser(errors.New("renderer failed")), requestID: "client-id", records: 2},
		{name: "parse success", records: 1},
		{name: "empty file", request: func(t *testing.T) *http.Request { return upload(t, []string{"file"}, nil) }, records: 2},
		{
			name: "list repository error", repo: &repoStub{err: errors.New("database down")}, records: 2,
			request: func(*testing.T) *http.Request { return httptest.NewRequest(http.MethodGet, "/boarding-passes", nil) },
		},
		{
			name: "invalid query", records: 2,
			request: func(*testing.T) *http.Request {
				return httptest.NewRequest(http.MethodGet, "/boarding-passes?limit=bad", nil)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parser, repo := &parserStub{}, &repoStub{}
			if tc.parser != nil {
				parser = tc.parser()
			}
			if tc.repo != nil {
				repo = tc.repo
			}
			req := upload(t, []string{"file"}, []byte("%PDF-1.4"))
			if tc.request != nil {
				req = tc.request(t)
			}
			if tc.requestID != "" {
				req.Header.Set("X-Request-ID", tc.requestID)
			}
			var logs bytes.Buffer
			resp := httptest.NewRecorder()
			newTestAPI(parser, repo, base.NewJSONLogger(&logs)).ServeHTTP(resp, req)

			requestID := resp.Header().Get("X-Request-ID")
			require.NotEmpty(t, requestID)
			if tc.requestID != "" {
				assert.Equal(t, tc.requestID, requestID)
			}
			records := decodeLogs(t, &logs)
			assert.Len(t, records, tc.records, logs.String())
			for _, record := range records {
				assert.Equal(t, requestID, record["request_id"], record)
			}
			assert.NotContains(t, logs.String(), `"error":null`)
		})
	}
}

func TestPanicLogHasRequestID(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	resp := httptest.NewRecorder()
	newTestAPI(panicParser{}, &repoStub{}, base.NewJSONLogger(&logs)).
		ServeHTTP(resp, upload(t, []string{"file"}, []byte("%PDF-1.4")))

	records := decodeLogs(t, &logs)
	require.Len(t, records, 2)
	assert.Equal(t, "request_panic", records[0]["msg"])
	assert.Equal(t, "request_finished", records[1]["msg"])
	for _, record := range records {
		assert.Equal(t, resp.Header().Get("X-Request-ID"), record["request_id"])
	}
}

// Handler validation failures log their own record; client errors mapped by
// fail are covered only by request_finished. No record carries a nil error.
func TestClientErrorLogs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		request    func(*testing.T) *http.Request
		parser     func() *parserStub
		status     int
		handlerLog string
		withError  bool
	}{
		{name: "missing content type", status: 415, handlerLog: "invalid content type", withError: true, request: func(*testing.T) *http.Request {
			return httptest.NewRequest(http.MethodPost, "/boarding-pass/parse-from-file", nil)
		}},
		{name: "wrong content type", status: 415, handlerLog: "invalid content type", request: func(*testing.T) *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/boarding-pass/parse-from-file", nil)
			req.Header.Set("Content-Type", "application/json")
			return req
		}},
		{name: "malformed content type", status: 415, handlerLog: "invalid content type", withError: true, request: func(*testing.T) *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/boarding-pass/parse-from-file", nil)
			req.Header.Set("Content-Type", "multipart/form-data; =")
			return req
		}},
		{name: "malformed multipart", status: 400, handlerLog: "invalid upload", withError: true, request: func(*testing.T) *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/boarding-pass/parse-from-file", strings.NewReader("bad multipart"))
			req.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
			return req
		}},
		{name: "wrong file count", status: 400, handlerLog: "invalid upload", request: func(t *testing.T) *http.Request {
			return upload(t, []string{"file", "file"}, []byte("%PDF-1.4"))
		}},
		{name: "empty file", status: 400, handlerLog: "empty file", request: func(t *testing.T) *http.Request {
			return upload(t, []string{"file"}, nil)
		}},
		{name: "not a PDF", status: 415, handlerLog: "invalid file type", request: func(t *testing.T) *http.Request {
			return upload(t, []string{"file"}, []byte("hello"))
		}},
		{name: "invalid query", status: 400, handlerLog: "invalid query parameters", withError: true, request: func(*testing.T) *http.Request {
			return httptest.NewRequest(http.MethodGet, "/boarding-passes?unknown=1", nil)
		}},
		{name: "invalid PDF", status: 422, parser: failingParser(pdf.ErrInvalid)},
		{name: "invalid BCBP", status: 422, parser: failingParser(service.ErrInvalidBCBP)},
		{name: "airport not found", status: 422, parser: failingParser(skypicker.ErrNotFound)},
		{name: "canceled", status: 408, parser: failingParser(context.Canceled)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parser := &parserStub{}
			if tc.parser != nil {
				parser = tc.parser()
			}
			req := upload(t, []string{"file"}, []byte("%PDF-1.4"))
			if tc.request != nil {
				req = tc.request(t)
			}
			var logs bytes.Buffer
			resp := httptest.NewRecorder()
			newTestAPI(parser, &repoStub{}, base.NewJSONLogger(&logs)).ServeHTTP(resp, req)

			require.Equal(t, tc.status, resp.Code, resp.Body.String())
			assert.NotContains(t, logs.String(), `"error":null`)
			records := decodeLogs(t, &logs)
			if tc.handlerLog != "" {
				require.Len(t, records, 2, logs.String())
				assert.Equal(t, tc.handlerLog, records[0]["msg"])
				assert.Equal(t, "ERROR", records[0]["level"])
				if tc.withError {
					assert.NotEmpty(t, records[0]["error"])
				} else {
					assert.NotContains(t, records[0], "error")
				}
				records = records[1:]
			}
			require.Len(t, records, 1, logs.String())
			assert.Equal(t, "request_finished", records[0]["msg"])
			assert.Equal(t, "WARN", records[0]["level"])
			assert.NotContains(t, records[0], "error")
		})
	}
}

func TestServerErrorsLogCause(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		parser func() *parserStub
		repo   *repoStub
		path   string
		status int
		cause  string
	}{
		{name: "internal", parser: failingParser(errors.New("renderer failed")), status: 500, cause: "renderer failed"},
		{name: "locations unavailable", parser: failingParser(skypicker.ErrUnavailable), status: 502, cause: skypicker.ErrUnavailable.Error()},
		{name: "timeout", parser: failingParser(context.DeadlineExceeded), status: 504, cause: context.DeadlineExceeded.Error()},
		{name: "list", repo: &repoStub{err: errors.New("database down")}, path: "/boarding-passes", status: 500, cause: "database down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parser, repo := &parserStub{}, &repoStub{}
			if tc.parser != nil {
				parser = tc.parser()
			}
			if tc.repo != nil {
				repo = tc.repo
			}
			req := upload(t, []string{"file"}, []byte("%PDF-1.4"))
			if tc.path != "" {
				req = httptest.NewRequest(http.MethodGet, tc.path, nil)
			}
			var logs bytes.Buffer
			resp := httptest.NewRecorder()
			newTestAPI(parser, repo, base.NewJSONLogger(&logs)).ServeHTTP(resp, req)

			require.Equal(t, tc.status, resp.Code, resp.Body.String())
			records := decodeLogs(t, &logs)
			require.Len(t, records, 2, logs.String())
			assert.Equal(t, "request failed", records[0]["msg"])
			assert.Equal(t, "ERROR", records[0]["level"])
			assert.Contains(t, records[0]["error"], tc.cause)
			assert.Equal(t, "request_finished", records[1]["msg"])
			assert.Equal(t, "ERROR", records[1]["level"])
		})
	}
}

// interruptedParser waits for the context to end and returns its own error,
// which does not wrap the context error.
type interruptedParser struct{ err error }

func (p interruptedParser) Parse(ctx context.Context, _ []byte) (bcbp.BoardingPass, error) {
	<-ctx.Done()
	return bcbp.BoardingPass{}, p.err
}

// When the parse context ends, the response is mapped from the context error,
// and the parser's own error is logged as the cause.
func TestInterruptedParseLogsCause(t *testing.T) {
	t.Parallel()

	expired := func(t *testing.T) context.Context {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now())
		t.Cleanup(cancel)
		return ctx
	}
	canceled := func(t *testing.T) context.Context {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		return ctx
	}
	for _, tc := range []struct {
		name     string
		ctx      func(*testing.T) context.Context
		parseErr error
		baseline error
		status   int
		cause    string
	}{
		{name: "timeout with cause", ctx: expired, parseErr: errors.New("renderer interrupted"), baseline: context.DeadlineExceeded, status: 504, cause: "renderer interrupted"},
		{name: "canceled with inner deadline", ctx: canceled, parseErr: context.DeadlineExceeded, baseline: context.Canceled, status: 408},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			want := httptest.NewRecorder()
			newTestAPI(failingParser(tc.baseline)(), &repoStub{}, logger()).
				ServeHTTP(want, upload(t, []string{"file"}, []byte("%PDF-1.4")))

			var logs bytes.Buffer
			resp := httptest.NewRecorder()
			req := upload(t, []string{"file"}, []byte("%PDF-1.4")).WithContext(tc.ctx(t))
			newTestAPI(interruptedParser{err: tc.parseErr}, &repoStub{}, base.NewJSONLogger(&logs)).ServeHTTP(resp, req)

			require.Equal(t, tc.status, resp.Code, resp.Body.String())
			assert.Equal(t, want.Code, resp.Code)
			assert.Equal(t, want.Body.String(), resp.Body.String())
			assert.Equal(t, want.Header().Get("Content-Type"), resp.Header().Get("Content-Type"))
			assert.NotContains(t, logs.String(), `"cause":null`)

			records := decodeLogs(t, &logs)
			if tc.status < 500 {
				require.Len(t, records, 1, logs.String())
				assert.Equal(t, "request_finished", records[0]["msg"])
				return
			}
			require.Len(t, records, 2, logs.String())
			assert.Equal(t, "request failed", records[0]["msg"])
			assert.Equal(t, "parse boarding pass: "+context.DeadlineExceeded.Error(), records[0]["error"])
			assert.Equal(t, tc.cause, records[0]["cause"])
		})
	}
}

// A parse that succeeds has already saved the record, so an expired request
// context must not turn the response into an error.
func TestSuccessfulParseIgnoresExpiredContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(t.Context(), time.Now())
	t.Cleanup(cancel)

	var logs bytes.Buffer
	resp := httptest.NewRecorder()
	req := upload(t, []string{"file"}, []byte("%PDF-1.4")).WithContext(ctx)
	newTestAPI(&parserStub{}, &repoStub{}, base.NewJSONLogger(&logs)).ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.JSONEq(t, `{"decoded_bcbp":{"passenger_name":"TEST/USER","legs":[]}}`, resp.Body.String())

	records := decodeLogs(t, &logs)
	require.Len(t, records, 1, logs.String())
	assert.Equal(t, "request_finished", records[0]["msg"])
	assert.Equal(t, "INFO", records[0]["level"])
}

func failingParser(err error) func() *parserStub {
	return func() *parserStub { return &parserStub{err: err} }
}

var _ store.Repository = (*repoStub)(nil)
