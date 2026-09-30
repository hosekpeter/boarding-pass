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
	"sync"
	"testing"
	"time"

	"boarding-pass/internal/api"
	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/client/skypicker"
	"boarding-pass/internal/dependencies"
	"boarding-pass/internal/pdf"
	"boarding-pass/internal/service"
	"boarding-pass/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type parserStub struct {
	err   error
	calls int
	pass  *bcbp.BoardingPass
}

func (s *parserStub) Parse(context.Context, []byte) (bcbp.BoardingPass, error) {
	s.calls++
	if s.pass != nil {
		return *s.pass, s.err
	}
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
		require.NoError(t, err)
		_, err = f.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/boarding-pass/parse-from-file", &data)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestParseUploadValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		fields []string
		body   []byte
		status int
		code   string
	}{
		{"valid", []string{"file"}, []byte("%PDF-1.4\n"), 200, ""},
		{"valid larger PDF", []string{"file"}, append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 32<<10)...), 200, ""},
		{"missing", nil, nil, 400, "invalid_upload"},
		{"wrong field", []string{"document"}, []byte("%PDF-1.4"), 400, "invalid_upload"},
		{"duplicate", []string{"file", "file"}, []byte("%PDF-1.4"), 400, "invalid_upload"},
		{"extra", []string{"file", "extra"}, []byte("%PDF-1.4"), 400, "invalid_upload"},
		{"empty", []string{"file"}, nil, 400, "empty_file"},
		{"non PDF", []string{"file"}, []byte("hello"), 415, "unsupported_media_type"},
		{"oversized", []string{"file"}, bytes.Repeat([]byte("x"), int(api.MaxUploadBytes)+1), 413, "upload_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parser := &parserStub{}
			handler := newTestAPI(parser, &repoStub{}, logger())
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, upload(t, tc.fields, tc.body))
			assert.Equal(t, tc.status, resp.Code, resp.Body.String())
			if tc.code != "" {
				assertError(t, resp, tc.code)
				assert.Zero(t, parser.calls, "parsed an invalid upload")
			}
			assert.Equal(t, "no-store", resp.Header().Get("Cache-Control"))
		})
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{pdf.ErrInvalid, 422, "invalid_pdf"}, {pdf.ErrNoBarcode, 422, "barcode_not_found"},
		{pdf.ErrMultiple, 422, "multiple_boarding_passes"}, {pdf.ErrTooManyPages, 422, "too_many_pages"},
		{service.ErrInvalidBCBP, 422, "invalid_bcbp"}, {skypicker.ErrUnavailable, 502, "locations_unavailable"},
		{skypicker.ErrNotFound, 422, "airport_not_found"},
		{context.DeadlineExceeded, 504, "timeout"}, {context.Canceled, 408, "canceled"},
		{errors.New("private database failure"), 500, "internal_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			handler := newTestAPI(&parserStub{err: tc.err}, &repoStub{}, logger())
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, upload(t, []string{"file"}, []byte("%PDF-1.4")))
			assert.Equal(t, tc.status, resp.Code, resp.Body.String())
			assertError(t, resp, tc.code)
			assert.NotContains(t, resp.Body.String(), "private database")
		})
	}
}

func TestListFiltersAndErrors(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"", "?limit=2&offset=3&passenger_name=cyprian&airline_code=FR"} {
		repo := &repoStub{}
		handler := newTestAPI(&parserStub{}, repo, logger())
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/boarding-passes"+query, nil))
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		var page store.Page
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &page))
		assert.NotNil(t, page.Items, "items must be an array")
		if query == "" {
			assert.Equal(t, 20, repo.filter.Limit)
		} else {
			assert.Equal(t, store.Filter{Limit: 2, Offset: 3, PassengerName: "cyprian", AirlineCode: "FR"}, repo.filter)
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=nope", "limit=", "offset=-1", "offset=2147483648", "limit=1&limit=2", "unknown=yes", "airline_code=TOOLONG", "passenger_name=%ZZ"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			handler := newTestAPI(&parserStub{}, &repoStub{}, logger())
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/boarding-passes?"+query, nil))
			assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
			assertError(t, resp, "invalid_query")
		})
	}
}

func TestRoutesAndContentType(t *testing.T) {
	t.Parallel()

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
		assert.Equal(t, tc.status, resp.Code, "%s %s", tc.method, tc.path)
	}
	req := httptest.NewRequest("POST", "/boarding-pass/parse-from-file", strings.NewReader("bad multipart"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	assert.Equal(t, http.StatusBadRequest, resp.Code, "malformed multipart")
}

type blockingParser struct{ started, release chan struct{} }

func (s *blockingParser) Parse(context.Context, []byte) (bcbp.BoardingPass, error) {
	s.started <- struct{}{}
	<-s.release
	return bcbp.BoardingPass{}, nil
}
func TestConcurrentParseLimit(t *testing.T) {
	t.Parallel()

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
	assert.Equal(t, http.StatusServiceUnavailable, resp.Code)
	assert.Equal(t, "1", resp.Header().Get("Retry-After"))
}

type readNotifier struct {
	io.Reader
	started chan struct{}
	once    sync.Once
}

func (r *readNotifier) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.Reader.Read(p)
}

func TestSlowUploadsDoNotReserveParserSlots(t *testing.T) {
	t.Parallel()

	parser := &parserStub{}
	handler := newTestAPI(parser, &repoStub{}, logger())
	for range 2 {
		reader, writer := io.Pipe()
		body := &readNotifier{Reader: reader, started: make(chan struct{})}
		done := make(chan struct{})
		req := httptest.NewRequest(http.MethodPost, "/boarding-pass/parse-from-file", body)
		req.Header.Set("Content-Type", "multipart/form-data; boundary=slow")
		t.Cleanup(func() {
			_ = writer.Close()
			_ = reader.Close()
			<-done
		})
		go func() {
			defer close(done)
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}()
		require.Eventually(t, func() bool {
			select {
			case <-body.started:
				return true
			default:
				return false
			}
		}, time.Second, time.Millisecond)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, upload(t, []string{"file"}, []byte("%PDF-1.4")))
	assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.Equal(t, 1, parser.calls)
}

func TestRequestLogging(t *testing.T) {
	t.Parallel()

	for _, status := range []int{200, 400, 422, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			parser := &parserStub{}
			if status == 422 {
				parser.err = pdf.ErrInvalid
			} else if status == 500 {
				parser.err = errors.New("renderer failed")
			}
			handler := newTestAPI(parser, &repoStub{}, slog.New(slog.NewJSONHandler(&logs, nil)))
			req := upload(t, []string{"file"}, []byte("%PDF-1.4"))
			if status == 400 {
				req = httptest.NewRequest(http.MethodGet, "/boarding-passes?passenger_name=SECRET&limit=bad", nil)
			}
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			require.Equal(t, status, resp.Code)
			var entry struct {
				Message   string `json:"msg"`
				RequestID string `json:"request_id"`
				HTTP      struct {
					Status int    `json:"status_code"`
					URL    string `json:"url"`
				} `json:"http"`
			}
			lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
			require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &entry))
			assert.Equal(t, "request_finished", entry.Message)
			assert.Equal(t, status, entry.HTTP.Status)
			assert.NotEmpty(t, entry.HTTP.URL)
			assert.Equal(t, resp.Header().Get("X-Request-ID"), entry.RequestID)
			assert.NotEmpty(t, entry.RequestID)
			assert.NotContains(t, logs.String(), "SECRET")
			assert.NotContains(t, logs.String(), "%PDF")
		})
	}
}

func TestJSONWireContract(t *testing.T) {
	t.Parallel()

	pass := bcbp.BoardingPass{PassengerName: "TEST/USER", Legs: []bcbp.Leg{{
		BookingReference: "ABC123", AirlineCode: "FR", FlightNumber: "7774", JulianDate: 105,
		CabinClass: "economy", Seat: "29C", PassengerStatus: "1",
		Origin:      bcbp.Airport{Code: "KSC", AirportName: "Kosice", CityName: "Kosice", Country: "Slovakia"},
		Destination: bcbp.Airport{Code: "PRG", AirportName: "Prague", CityName: "Prague", Country: "Czechia"},
	}}}
	handler := newTestAPI(&parserStub{pass: &pass}, &repoStub{}, logger())
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, upload(t, []string{"file"}, []byte("%PDF-1.4")))
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "application/json", resp.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"decoded_bcbp":{"passenger_name":"TEST/USER","legs":[{
		"booking_reference":"ABC123","airline_code":"FR","flight_number":"7774","julian_date":105,
		"cabin_class":"economy","seat":"29C","passenger_status":"1",
		"origin":{"code":"KSC","airport_name":"Kosice","city_name":"Kosice","country":"Slovakia"},
		"destination":{"code":"PRG","airport_name":"Prague","city_name":"Prague","country":"Czechia"}
	}]}}`, resp.Body.String())
	resp = httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/boarding-passes", nil))
	assert.Equal(t, "application/json", resp.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"items":[],"total":0,"limit":20,"offset":0}`, resp.Body.String())
}

type panicParser struct{}

func (panicParser) Parse(context.Context, []byte) (bcbp.BoardingPass, error) {
	panic("private diagnostic")
}

func TestPanicUsesErrorEnvelopeAndIsLogged(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	handler := newTestAPI(panicParser{}, &repoStub{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, upload(t, []string{"file"}, []byte("%PDF-1.4")))
	assert.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Equal(t, "application/json", resp.Header().Get("Content-Type"))
	assertError(t, resp, "internal_error")
	assert.NotContains(t, resp.Body.String(), "private diagnostic")
	assert.NotEmpty(t, resp.Header().Get("X-Request-ID"))
	assert.Contains(t, logs.String(), `"msg":"request_panic"`)
	assert.Contains(t, logs.String(), `"msg":"request_finished"`)
	assert.Contains(t, logs.String(), `"status_code":500`)
}

func assertError(t *testing.T, resp *httptest.ResponseRecorder, code string) {
	t.Helper()
	var envelope struct {
		Error struct{ Code, Message string }
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &envelope))
	assert.Equal(t, code, envelope.Error.Code)
	assert.NotEmpty(t, envelope.Error.Message)
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
	t.Parallel()

	for _, query := range []string{
		"passenger_name=%00", "passenger_name=CYPRIAN%00MICHAL", "passenger_name=%FF",
		"airline_code=%00", "airline_code=%FF", "passenger_name=%C0%AF",
	} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			repo := &repoStub{}
			handler := newTestAPI(&parserStub{}, repo, logger())
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/boarding-passes?"+query, nil))
			assert.Equal(t, http.StatusBadRequest, resp.Code, resp.Body.String())
			assert.Zero(t, repo.calls)
			assertError(t, resp, "invalid_query")
		})
	}
}
