package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"boarding-pass/internal/api"
	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/client/skypicker"
	"boarding-pass/internal/dependencies"
	"boarding-pass/internal/pdf"
	"boarding-pass/internal/service"
	"boarding-pass/internal/store"
)

type parserStub struct {
	err   error
	calls int
}

func (s *parserStub) Parse(context.Context, []byte) (bcbp.BoardingPass, error) {
	s.calls++
	return bcbp.BoardingPass{PassengerName: "TEST/USER", Legs: []bcbp.Leg{}}, s.err
}

type repoStub struct {
	calls  int
	filter store.Filter
	err    error
}

func (s *repoStub) List(_ context.Context, f store.Filter) (store.Page, error) {
	s.calls++
	s.filter = f
	return store.Page{Items: []store.Record{}, Limit: f.Limit, Offset: f.Offset}, s.err
}
func (s *repoStub) Save(context.Context, bcbp.BoardingPass) error {
	return errors.New("unexpected direct save from HTTP handler")
}

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func upload(t *testing.T, fields []string, body []byte) *http.Request {
	t.Helper()
	var data bytes.Buffer
	writer := multipart.NewWriter(&data)
	for _, field := range fields {
		f, err := writer.CreateFormFile(field, "boarding.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/boarding-pass/parse-from-file", &data)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestParseUploadValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
		body   []byte
		status int
		code   string
	}{
		{"valid", []string{"file"}, []byte("%PDF-1.4\n"), 200, ""},
		{"missing", nil, nil, 400, "invalid_upload"},
		{"wrong field", []string{"document"}, []byte("%PDF-1.4"), 400, "invalid_upload"},
		{"duplicate", []string{"file", "file"}, []byte("%PDF-1.4"), 400, "invalid_upload"},
		{"extra", []string{"file", "extra"}, []byte("%PDF-1.4"), 400, "invalid_upload"},
		{"empty", []string{"file"}, nil, 400, "empty_file"},
		{"non PDF", []string{"file"}, []byte("hello"), 415, "unsupported_media_type"},
		{"oversized", []string{"file"}, bytes.Repeat([]byte("x"), int(api.MaxUploadBytes)+1), 413, "upload_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parser := &parserStub{}
			handler := newTestAPI(parser, &repoStub{}, logger())
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, upload(t, tc.fields, tc.body))
			if resp.Code != tc.status {
				t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
			}
			if tc.code != "" {
				assertError(t, resp, tc.code)
				if parser.calls != 0 {
					t.Fatal("parsed an invalid upload")
				}
			}
			if resp.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing privacy header")
			}
		})
	}
}

func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{pdf.ErrInvalid, 422, "invalid_pdf"}, {pdf.ErrNoBarcode, 422, "barcode_not_found"},
		{pdf.ErrMultiple, 422, "multiple_boarding_passes"}, {pdf.ErrTooManyPages, 422, "too_many_pages"},
		{service.ErrInvalidBCBP, 422, "invalid_bcbp"}, {skypicker.ErrUnavailable, 502, "locations_unavailable"},
		{context.DeadlineExceeded, 504, "timeout"}, {context.Canceled, 408, "canceled"},
		{errors.New("private database failure"), 500, "internal_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			handler := newTestAPI(&parserStub{err: tc.err}, &repoStub{}, logger())
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, upload(t, []string{"file"}, []byte("%PDF-1.4")))
			if resp.Code != tc.status {
				t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
			}
			assertError(t, resp, tc.code)
			if strings.Contains(resp.Body.String(), "private database") {
				t.Fatal("exposed internal failure")
			}
		})
	}
}

func TestListFiltersAndErrors(t *testing.T) {
	for _, query := range []string{"", "?limit=2&offset=3&passenger_name=cyprian&airline_code=FR"} {
		repo := &repoStub{}
		handler := newTestAPI(&parserStub{}, repo, logger())
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/boarding-passes"+query, nil))
		if resp.Code != 200 {
			t.Fatalf("status %d", resp.Code)
		}
		var page store.Page
		if err := json.Unmarshal(resp.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if page.Items == nil {
			t.Fatal("items must be an array")
		}
		if query == "" && repo.filter.Limit != 20 {
			t.Fatal("wrong default limit")
		}
		if query != "" && repo.filter != (store.Filter{Limit: 2, Offset: 3, PassengerName: "cyprian", AirlineCode: "FR"}) {
			t.Fatalf("wrong filter: %+v", repo.filter)
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=nope", "limit=", "offset=-1", "offset=2147483648", "limit=1&limit=2", "unknown=yes", "airline_code=TOOLONG", "passenger_name=%ZZ"} {
		t.Run(query, func(t *testing.T) {
			handler := newTestAPI(&parserStub{}, &repoStub{}, logger())
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/boarding-passes?"+query, nil))
			if resp.Code != 400 {
				t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
			}
			assertError(t, resp, "invalid_query")
		})
	}
}

func TestRoutesAndContentType(t *testing.T) {
	handler := newTestAPI(&parserStub{}, &repoStub{}, logger())
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/healthz", 200}, {"GET", "/openapi.yaml", 200}, {"GET", "/unknown", 404},
		{"POST", "/boarding-passes", 405}, {"GET", "/boarding-pass/parse-from-file", 405},
		{"POST", "/boarding-pass/parse-from-file", 415},
	} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(tc.method, tc.path, nil))
		if resp.Code != tc.status {
			t.Fatalf("%s %s returned %d", tc.method, tc.path, resp.Code)
		}
	}
	req := httptest.NewRequest("POST", "/boarding-pass/parse-from-file", strings.NewReader("bad multipart"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != 400 {
		t.Fatalf("malformed multipart returned %d", resp.Code)
	}
}

type blockingParser struct{ started, release chan struct{} }

func (s *blockingParser) Parse(context.Context, []byte) (bcbp.BoardingPass, error) {
	s.started <- struct{}{}
	<-s.release
	return bcbp.BoardingPass{}, nil
}
func TestConcurrentParseLimit(t *testing.T) {
	parser := &blockingParser{started: make(chan struct{}, 2), release: make(chan struct{})}
	handler := newTestAPI(parser, &repoStub{}, logger())
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		req := upload(t, []string{"file"}, []byte("%PDF-1.4"))
		go func() { handler.ServeHTTP(httptest.NewRecorder(), req); done <- struct{}{} }()
	}
	<-parser.started
	<-parser.started
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, upload(t, []string{"file"}, []byte("%PDF-1.4")))
	close(parser.release)
	<-done
	<-done
	if resp.Code != 503 || resp.Header().Get("Retry-After") != "1" {
		t.Fatalf("unexpected overload response: %d", resp.Code)
	}
}

func assertError(t *testing.T, resp *httptest.ResponseRecorder, code string) {
	t.Helper()
	var envelope struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != code || envelope.Error.Message == "" {
		t.Fatalf("unexpected error: %s", resp.Body.String())
	}
}

type apiScopeStub struct {
	base       dependencies.Base
	parser     dependencies.Parser
	repository store.Repository
}

func (s *apiScopeStub) Base() dependencies.Base      { return s.base }
func (s *apiScopeStub) Parser() dependencies.Parser  { return s.parser }
func (s *apiScopeStub) Repository() store.Repository { return s.repository }

func (s *apiScopeStub) Close() error { return nil }

var _ dependencies.ServiceScope = (*apiScopeStub)(nil)

func newTestAPI(parser dependencies.Parser, repository store.Repository, logger *slog.Logger) http.Handler {
	return api.New(&apiScopeStub{base: dependencies.NewBase(logger), parser: parser, repository: repository})
}

func TestInvalidTextFiltersDoNotReachRepository(t *testing.T) {
	for _, query := range []string{
		"passenger_name=%00", "passenger_name=CYPRIAN%00MICHAL", "passenger_name=%FF",
		"airline_code=%00", "airline_code=%FF", "passenger_name=%C0%AF",
	} {
		t.Run(query, func(t *testing.T) {
			repo := &repoStub{}
			handler := newTestAPI(&parserStub{}, repo, logger())
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/boarding-passes?"+query, nil))
			if resp.Code != 400 || repo.calls != 0 {
				t.Fatalf("status %d, repository calls %d: %s", resp.Code, repo.calls, resp.Body.String())
			}
			assertError(t, resp, "invalid_query")
		})
	}
}
