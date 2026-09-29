package service_test

import (
	"context"
	"errors"
	"testing"

	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/client/skypicker"
	"boarding-pass/internal/pdf"
	"boarding-pass/internal/service"
	"boarding-pass/internal/store"
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

func TestParseEnrichesBeforeSave(t *testing.T) {
	airports := &lookupStub{}
	repo := &repositoryStub{}
	parser := service.New(extractorStub{value: validPayload}, airports, repo)
	pass, err := parser.Parse(context.Background(), []byte("PDF"))
	if err != nil {
		t.Fatal(err)
	}
	wantOrigin := bcbp.Airport{Code: "KSC", AirportName: "Airport", CityName: "City", Country: "Country"}
	wantDestination := bcbp.Airport{Code: "PRG", AirportName: "Airport", CityName: "City", Country: "Country"}
	if pass.Legs[0].Origin != wantOrigin || pass.Legs[0].Destination != wantDestination {
		t.Fatalf("airport client response was not mapped to the boarding pass: %+v", pass)
	}
	if airports.calls != 2 || len(repo.saved) != 1 || repo.saved[0].Legs[0].Origin.Country != "Country" || pass.Legs[0].Destination.Code != "PRG" {
		t.Fatalf("incomplete enrichment: %+v", pass)
	}
}

func TestFailuresAreNotSaved(t *testing.T) {
	for _, tc := range []struct {
		name      string
		extract   extractorStub
		lookupErr error
		want      error
	}{
		{"extract", extractorStub{err: pdf.ErrInvalid}, nil, pdf.ErrInvalid},
		{"parse", extractorStub{value: "bad"}, nil, service.ErrInvalidBCBP},
		{"enrich", extractorStub{value: validPayload}, errors.New("offline"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repositoryStub{}
			_, err := service.New(tc.extract, &lookupStub{err: tc.lookupErr}, repo).Parse(context.Background(), nil)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(repo.saved) != 0 {
				t.Fatal("saved partial result")
			}
		})
	}
}

func TestSaveFailureIsReturned(t *testing.T) {
	failure := errors.New("disk full")
	_, err := service.New(extractorStub{value: validPayload}, &lookupStub{}, &repositoryStub{err: failure}).Parse(context.Background(), nil)
	if !errors.Is(err, failure) {
		t.Fatalf("expected save error, got %v", err)
	}
}

func TestRepeatedAirportsResolvedOnce(t *testing.T) {
	payload := "M2" + validPayload[2:] + validPayload[23:]
	airports := &lookupStub{}
	pass, err := service.New(extractorStub{value: payload}, airports, &repositoryStub{}).Parse(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pass.Legs) != 2 || airports.calls != 2 {
		t.Fatalf("expected two lookups: %d", airports.calls)
	}
}
