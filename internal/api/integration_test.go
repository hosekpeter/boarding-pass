//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"boarding-pass/internal/api"
	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/config"
	"boarding-pass/internal/dependencies"
	"boarding-pass/internal/store"
	"boarding-pass/internal/testutil"
)

// Exercises the actual PDF renderer, PDF417 decoder, BCBP parser, HTTP airport
// client and PostgreSQL through the API. Only the external API is stubbed.
func TestSamplePDFEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		airports := map[string]string{
			"KSC": `{"code":"KSC","type":"airport","name":"Košice International","city":{"name":"Košice","country":{"name":"Slovakia"}}}`,
			"PRG": `{"code":"PRG","type":"airport","name":"Václav Havel Airport Prague","city":{"name":"Prague","country":{"name":"Czechia"}}}`,
		}
		result, ok := airports[r.URL.Query().Get("id")]
		if !ok {
			t.Errorf("unexpected airport request: %s", r.URL)
			http.Error(w, "unknown", 404)
			return
		}
		fmt.Fprintf(w, `{"locations":[%s]}`, result)
	}))
	defer upstream.Close()
	cfg := config.Config{
		Database:        testutil.DatabaseConfig(t),
		LocationBaseURL: upstream.URL,
		HTTPAddr:        "127.0.0.1:0",
	}
	scope, err := dependencies.NewServiceScope(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	handler := api.New(scope)
	data, err := os.ReadFile("../../testdata/boarding_pass.pdf")
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "../../untrusted-filename.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	req := httptest.NewRequest("POST", "/boarding-pass/parse-from-file", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("parse status %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		DecodedBCBP bcbp.BoardingPass `json:"decoded_bcbp"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	pass := result.DecodedBCBP
	if pass.PassengerName != "CYPRIAN/MICHAL" || len(pass.Legs) != 1 {
		t.Fatalf("unexpected pass: %+v", pass)
	}
	leg := pass.Legs[0]
	if leg.BookingReference != "ZKN85B" || leg.AirlineCode != "FR" || leg.FlightNumber != "7774" || leg.JulianDate != 105 || leg.CabinClass != "economy" || leg.Seat != "29C" || leg.PassengerStatus != "1" || leg.Origin.Code != "KSC" || leg.Origin.Country != "Slovakia" || leg.Destination.Code != "PRG" || leg.Destination.CityName != "Prague" {
		t.Fatalf("unexpected flight: %+v", leg)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Repository().List(t.Context(), store.Filter{Limit: 20}); err == nil {
		t.Fatal("scope did not close the database")
	}
	reopened, err := dependencies.NewServiceScope(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	handler = api.New(reopened)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/boarding-passes?passenger_name=cyprian&airline_code=FR&limit=1", nil))
	if response.Code != 200 {
		t.Fatalf("list status %d: %s", response.Code, response.Body.String())
	}
	var page store.Page
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID == "" || page.Items[0].ParsedAt.IsZero() || page.Items[0].DecodedBCBP.PassengerName != pass.PassengerName {
		t.Fatalf("unexpected saved page: %+v", page)
	}
}

func TestMultipleLegsEndToEnd(t *testing.T) {
	for count := 2; count <= 4; count++ {
		t.Run(fmt.Sprintf("%d legs", count), func(t *testing.T) {
			codes := []string{"KSC", "PRG", "LHR", "JFK", "LAX"}
			carriers := []string{"FR", "BA", "AA", "UA"}
			var payload strings.Builder
			fmt.Fprintf(&payload, "M%d%-20sE", count, "TEST/TRAVELER")
			for i := 0; i < count; i++ {
				fmt.Fprintf(&payload, "%-7s%s%s%-3s%-5s%03d%s%s%-5s%s%s", "REF123", codes[i], codes[i+1], carriers[i], "0012", 105, "Y", "001A", "00001", "1", "00")
			}
			data := testutil.PDF(t, payload.String())
			var mu sync.Mutex
			calls := make(map[string]int)
			var failSecond atomic.Bool
			failSecond.Store(count == 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				code := r.URL.Query().Get("id")
				mu.Lock()
				calls[code]++
				mu.Unlock()
				if code == "PRG" && failSecond.Swap(false) {
					http.Error(w, "temporary outage", http.StatusServiceUnavailable)
					return
				}
				fmt.Fprintf(w, `{"locations":[{"code":%q,"type":"airport","name":"Airport","city":{"name":"City","country":{"name":"Country"}}}]}`, code)
			}))
			defer upstream.Close()
			cfg := config.Config{Database: testutil.DatabaseConfig(t), LocationBaseURL: upstream.URL, HTTPAddr: "127.0.0.1:0"}
			scope, err := dependencies.NewServiceScope(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Close()
			handler := api.New(scope)
			parse := func() *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, upload(t, []string{"file"}, data))
				return response
			}
			list := func(query string) store.Page {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/boarding-passes"+query, nil))
				if response.Code != 200 {
					t.Fatalf("history: %d %s", response.Code, response.Body.String())
				}
				var page store.Page
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				return page
			}
			if count == 2 {
				response := parse()
				if response.Code != 502 {
					t.Fatalf("second airport outage: %d %s", response.Code, response.Body.String())
				}
				if page := list(""); page.Total != 0 || len(page.Items) != 0 {
					t.Fatal("saved a partially enriched pass")
				}
			}
			response := parse()
			if response.Code != 200 {
				t.Fatalf("parse: %d %s", response.Code, response.Body.String())
			}
			var result struct {
				DecodedBCBP bcbp.BoardingPass `json:"decoded_bcbp"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.DecodedBCBP.Legs) != count {
				t.Fatalf("missing legs: %+v", result)
			}
			for i, leg := range result.DecodedBCBP.Legs {
				if leg.Origin.Code != codes[i] || leg.Destination.Code != codes[i+1] || leg.AirlineCode != carriers[i] || leg.Origin.AirportName != "Airport" || leg.Destination.Country != "Country" {
					t.Fatalf("incorrectly enriched leg %d: %+v", i, leg)
				}
			}
			page := list("?passenger_name=traveler&airline_code=BA&limit=1")
			if page.Total != 1 || len(page.Items) != 1 || len(page.Items[0].DecodedBCBP.Legs) != count {
				t.Fatalf("filter on second leg failed: %+v", page)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, code := range codes[:count+1] {
				want := 1
				if count == 2 && code == "PRG" {
					want = 2
				}
				if calls[code] != want {
					t.Errorf("airport %s requested %d times, expected %d", code, calls[code], want)
				}
			}
		})
	}
}
