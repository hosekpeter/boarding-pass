package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"

	"boarding-pass/internal/base"
	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/client/skypicker"
	"boarding-pass/internal/pdf"
	"boarding-pass/internal/service"
	"boarding-pass/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type extractorStub struct {
	value string
	err   error
}

func (s extractorStub) Extract(context.Context, []byte) (string, error) { return s.value, s.err }

type lookupStub struct {
	calls int
	err   error
}

func (s *lookupStub) Lookup(_ context.Context, code string) (skypicker.Airport, error) {
	s.calls++
	return skypicker.Airport{Code: code, AirportName: "Airport", CityName: "City", Country: "Country"}, s.err
}

type repositoryStub struct {
	saved []bcbp.BoardingPass
	err   error
}

func (s *repositoryStub) Save(_ context.Context, p bcbp.BoardingPass) error {
	s.saved = append(s.saved, p)
	return s.err
}

func (s *repositoryStub) List(context.Context, store.Filter) (store.Page, error) {
	return store.Page{}, errors.New("unexpected history lookup in parser service")
}

const validPayload = "M1CYPRIAN/MICHAL       ZKN85B KSCPRGFR 7774 105Y029C0126 100"

func testBase() base.Base { return base.New(base.NewJSONLogger(io.Discard)) }

func TestParseEnrichesBeforeSave(t *testing.T) {
	t.Parallel()

	airports := &lookupStub{}
	repo := &repositoryStub{}
	parser := service.New(testBase(), extractorStub{value: validPayload}, airports, repo)
	pass, err := parser.Parse(t.Context(), []byte("PDF"))
	require.NoError(t, err)
	wantOrigin := bcbp.Airport{Code: "KSC", AirportName: "Airport", CityName: "City", Country: "Country"}
	wantDestination := bcbp.Airport{Code: "PRG", AirportName: "Airport", CityName: "City", Country: "Country"}
	require.Len(t, pass.Legs, 1)
	assert.Equal(t, wantOrigin, pass.Legs[0].Origin)
	assert.Equal(t, wantDestination, pass.Legs[0].Destination)
	assert.Equal(t, 2, airports.calls)
	assert.Equal(t, []bcbp.BoardingPass{pass}, repo.saved)
}

func TestFailuresAreWrappedAndNotSaved(t *testing.T) {
	t.Parallel()

	offline := errors.New("offline")
	for _, tc := range []struct {
		name      string
		extract   extractorStub
		lookupErr error
		want      error
		message   string
	}{
		{"extract", extractorStub{err: pdf.ErrInvalid}, nil, pdf.ErrInvalid, "extract barcode: "},
		{"no barcode", extractorStub{err: pdf.ErrNoBarcode}, nil, pdf.ErrNoBarcode, "extract barcode: "},
		{"multiple", extractorStub{err: pdf.ErrMultiple}, nil, pdf.ErrMultiple, "extract barcode: "},
		{"too many pages", extractorStub{err: pdf.ErrTooManyPages}, nil, pdf.ErrTooManyPages, "extract barcode: "},
		{"extract timeout", extractorStub{err: context.DeadlineExceeded}, nil, context.DeadlineExceeded, "extract barcode: "},
		{"parse", extractorStub{value: "bad"}, nil, service.ErrInvalidBCBP, "invalid BCBP payload: "},
		{"enrich", extractorStub{value: validPayload}, offline, offline, "lookup airport KSC: "},
		{"unknown airport", extractorStub{value: validPayload}, skypicker.ErrNotFound, skypicker.ErrNotFound, "lookup airport KSC: "},
		{"airports unavailable", extractorStub{value: validPayload}, skypicker.ErrUnavailable, skypicker.ErrUnavailable, "lookup airport KSC: "},
		{"lookup canceled", extractorStub{value: validPayload}, context.Canceled, context.Canceled, "lookup airport KSC: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			repo := &repositoryStub{}
			_, err := service.New(base.New(base.NewJSONLogger(&logs)), tc.extract, &lookupStub{err: tc.lookupErr}, repo).Parse(t.Context(), nil)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.want)
			assert.True(t, strings.HasPrefix(err.Error(), tc.message), err.Error())
			assert.Empty(t, repo.saved, "saved partial result")
			assert.Empty(t, logs.String(), "errors are returned, not logged")
		})
	}
}

func TestInvalidBCBPKeepsParserCause(t *testing.T) {
	t.Parallel()

	_, cause := bcbp.Parse("bad")
	require.Error(t, cause)

	_, err := service.New(testBase(), extractorStub{value: "bad"}, &lookupStub{}, &repositoryStub{}).Parse(t.Context(), nil)
	require.ErrorIs(t, err, service.ErrInvalidBCBP)
	var wrapped []string
	for e := range errorTree(err) {
		if e != err && e != service.ErrInvalidBCBP {
			wrapped = append(wrapped, e.Error())
		}
	}
	assert.Contains(t, wrapped, cause.Error(), "parser error is not unwrappable")
}

// errorTree yields err and every error reachable through Unwrap.
func errorTree(err error) iter.Seq[error] {
	return func(yield func(error) bool) {
		var walk func(error) bool
		walk = func(e error) bool {
			if e == nil {
				return true
			}
			if !yield(e) {
				return false
			}
			switch u := e.(type) {
			case interface{ Unwrap() error }:
				return walk(u.Unwrap())
			case interface{ Unwrap() []error }:
				for _, inner := range u.Unwrap() {
					if !walk(inner) {
						return false
					}
				}
			}
			return true
		}
		walk(err)
	}
}

func TestSuccessLogHasRequestID(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	parser := service.New(base.New(base.NewJSONLogger(&logs)), extractorStub{value: validPayload}, &lookupStub{}, &repositoryStub{})
	_, err := parser.Parse(base.WithRequestID(t.Context(), "req-1"), nil)
	require.NoError(t, err)

	var record map[string]any
	require.NoError(t, json.Unmarshal(logs.Bytes(), &record))
	assert.Equal(t, "boarding pass parsed successfully", record["msg"])
	assert.Equal(t, "req-1", record["request_id"])
}

func TestSaveFailureIsReturned(t *testing.T) {
	t.Parallel()

	failure := errors.New("disk full")
	_, err := service.New(testBase(), extractorStub{value: validPayload}, &lookupStub{}, &repositoryStub{err: failure}).Parse(t.Context(), nil)
	require.ErrorIs(t, err, failure)
}

func TestRepeatedAirportsResolvedOnce(t *testing.T) {
	t.Parallel()

	payload := "M2" + validPayload[2:] + validPayload[23:]
	airports := &lookupStub{}
	pass, err := service.New(testBase(), extractorStub{value: payload}, airports, &repositoryStub{}).Parse(t.Context(), nil)
	require.NoError(t, err)
	assert.Len(t, pass.Legs, 2)
	assert.Equal(t, 2, airports.calls)
}
