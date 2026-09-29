package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"boarding-pass/internal/client/skypicker"
	"boarding-pass/internal/dependencies"
	"boarding-pass/internal/pdf"
	"boarding-pass/internal/service"
	"boarding-pass/internal/store"

	swaggerFiles "github.com/swaggo/files/v2"
)

const MaxUploadBytes int64 = 10 << 20
const ParseTimeout = 30 * time.Second

type API struct {
	parser     dependencies.Parser
	repository store.Repository
	logger     *slog.Logger
	slots      chan struct{}
}

// New constructs the HTTP API.
//
// @title Boarding Pass API
// @version 1.0.0
// @description Parse a PDF417 boarding pass from PDF and retain successfully parsed results.
// @servers.url /
// @servers.description Current server
// @externalDocs.description Original assignment
// @externalDocs.url https://github.com/mcyprian/interview_task/blob/main/Boarding_Pass_Parsing_Task.md
func New(scope dependencies.ServiceScope) http.Handler {
	a := &API{
		parser:     scope.Parser(),
		repository: scope.Repository(),
		logger:     scope.Base().Logger(),
		slots:      make(chan struct{}, 2),
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/boarding-pass/parse-from-file", method(http.MethodPost, a.parse))
	mux.HandleFunc("/boarding-passes", method(http.MethodGet, a.list))
	mux.HandleFunc("/healthz", method(http.MethodGet, health))
	mux.HandleFunc("/openapi.yaml", method(http.MethodGet, openAPI))
	mux.HandleFunc("/documentation", method(http.MethodGet, documentation))
	mux.HandleFunc("/documentation/{$}", method(http.MethodGet, documentation))
	mux.HandleFunc("/documentation/swagger.json", method(http.MethodGet, specJSON))
	mux.HandleFunc("/documentation/swagger.yaml", method(http.MethodGet, specYAML))
	mux.HandleFunc("/documentation/", method(http.MethodGet,
		http.StripPrefix("/documentation/", http.FileServer(http.FS(swaggerFiles.FS))).ServeHTTP))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Endpoint not found")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recover() != nil {
				a.logger.Error("request panic")
				writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

func method(want string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != want {
			w.Header().Set("Allow", want)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use "+want+" for this endpoint")
			return
		}
		next(w, r)
	}
}

// @Summary Decode and save one boarding pass
// @Description Accepts a PDF of up to 10 pages. The complete multipart request must fit within 10 MiB. Repeated identical barcodes are accepted; distinct barcodes in the same document are rejected.
// @Description Each successful call creates a new history entry, including repeated uploads. Processing has a 30-second context deadline. Airport lookup failures do not save partial records.
// @ID parseBoardingPass
// @Tags Boarding passes
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "Exactly one PDF file; complete request limited to 10 MiB"
// @Success 200 {object} ParsedPass "Decoded and committed to history"
// @Failure 400 {object} ErrorResponse "Malformed multipart, missing or duplicate file, or empty upload"
// @Failure 405 {object} ErrorResponse "Method not allowed"
// @Failure 408 {object} ErrorResponse "Request canceled"
// @Failure 413 {object} ErrorResponse "Multipart request exceeds 10 MiB"
// @Failure 415 {object} ErrorResponse "Expected multipart/form-data containing a PDF"
// @Failure 422 {object} ErrorResponse "Invalid PDF, too many pages, missing or ambiguous PDF417 barcode, or invalid BCBP"
// @Failure 500 {object} ErrorResponse "Internal processing or persistence failure"
// @Failure 502 {object} ErrorResponse "Airport information is unavailable"
// @Failure 503 {object} ErrorResponse "Both processing slots are busy; retry after one second"
// @Header 503 {integer} Retry-After "Seconds to wait before retrying (1)"
// @Failure 504 {object} ErrorResponse "Processing timed out"
// @Router /boarding-pass/parse-from-file [post]
func (a *API) parse(w http.ResponseWriter, r *http.Request) {
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "busy", "Parser is busy; retry shortly")
		return
	}

	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "multipart/form-data" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Expected multipart/form-data with a PDF file")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "upload_too_large", "Multipart request exceeds 10 MiB")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_upload", "Invalid multipart request")
		}
		return
	}

	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
		writeError(w, http.StatusBadRequest, "invalid_upload", "Provide exactly one PDF in the file field")
		return
	}

	f, err := r.MultipartForm.File["file"][0].Open()
	if err != nil {
		a.fail(w, err)
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		a.fail(w, err)
		return
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty_file", "The uploaded file is empty")
		return
	}

	if len(data) < 5 || string(data[:5]) != "%PDF-" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "The uploaded file must be a PDF")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), ParseTimeout)
	defer cancel()

	pass, err := a.parser.Parse(ctx, data)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		a.fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ParsedPass{DecodedBCBP: pass})
}

// @Summary List previously parsed boarding passes, newest first
// @Description Filters are combined with AND. Total counts all matching records before pagination. Equal timestamps are ordered by insertion sequence, newest first.
// @Description Unknown or repeated query parameters, invalid UTF-8, and NUL characters are rejected. String filter limits are measured in UTF-8 bytes.
// @ID listBoardingPasses
// @Tags Boarding passes
// @Produce json
// @Param limit query int false "Maximum records to return" minimum(1) maximum(100) default(20)
// @Param offset query int false "Matching records to skip" minimum(0) maximum(2147483647) default(0)
// @Param passenger_name query string false "Case-insensitive literal substring of the passenger name; at most 100 UTF-8 bytes" maxLength(100)
// @Param airline_code query string false "Exact, case-sensitive match on any flight leg; empty means no filter; at most 3 UTF-8 bytes" maxLength(3)
// @Success 200 {object} store.Page "Matching records"
// @Failure 400 {object} ErrorResponse "Invalid query parameters"
// @Failure 405 {object} ErrorResponse "Method not allowed"
// @Failure 408 {object} ErrorResponse "Request canceled"
// @Failure 500 {object} ErrorResponse "Internal persistence failure"
// @Failure 504 {object} ErrorResponse "Query timed out"
// @Router /boarding-passes [get]
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	query, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := a.repository.List(ctx, query)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func parseFilter(r *http.Request) (store.Filter, error) {
	f := store.Filter{Limit: 20}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return f, errors.New("malformed query parameters")
	}
	for key, values := range q {
		switch key {
		case "limit", "offset", "passenger_name", "airline_code":
		default:
			return f, errors.New("unsupported query parameter")
		}
		if len(values) != 1 {
			return f, errors.New("query parameters must not be repeated")
		}
	}
	for _, field := range []struct {
		key     string
		target  *int
		minimum int
	}{
		{"limit", &f.Limit, 1}, {"offset", &f.Offset, 0},
	} {
		if q.Has(field.key) {
			n, err := strconv.ParseInt(q.Get(field.key), 10, 32)
			if err != nil || n < int64(field.minimum) || (field.key == "limit" && n > 100) {
				return f, errors.New("limit must be 1..100 and offset must be 0..2147483647")
			}
			*field.target = int(n)
		}
	}
	f.PassengerName, f.AirlineCode = q.Get("passenger_name"), q.Get("airline_code")
	for _, value := range []string{f.PassengerName, f.AirlineCode} {
		if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
			return f, errors.New("filters must be valid UTF-8 without NUL characters")
		}
	}
	if len(f.PassengerName) > 100 || len(f.AirlineCode) > 3 {
		return f, errors.New("filter value is too long")
	}
	return f, nil
}

func (a *API) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "Processing timed out")
	case errors.Is(err, context.Canceled):
		writeError(w, http.StatusRequestTimeout, "canceled", "Request was canceled")
	case errors.Is(err, pdf.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid_pdf", "The PDF is invalid or encrypted")
	case errors.Is(err, pdf.ErrNoBarcode):
		writeError(w, http.StatusUnprocessableEntity, "barcode_not_found", "No readable PDF417 barcode found")
	case errors.Is(err, pdf.ErrMultiple):
		writeError(w, http.StatusUnprocessableEntity, "multiple_boarding_passes", "Upload one distinct boarding pass per request")
	case errors.Is(err, pdf.ErrTooManyPages):
		writeError(w, http.StatusUnprocessableEntity, "too_many_pages", "PDF exceeds the 10-page limit")
	case errors.Is(err, service.ErrInvalidBCBP):
		writeError(w, http.StatusUnprocessableEntity, "invalid_bcbp", "Barcode does not contain a valid supported BCBP payload")
	case errors.Is(err, skypicker.ErrUnavailable):
		writeError(w, http.StatusBadGateway, "locations_unavailable", "Airport information is temporarily unavailable")
	default:
		a.logger.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Error: APIError{Code: code, Message: message}})
}
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
