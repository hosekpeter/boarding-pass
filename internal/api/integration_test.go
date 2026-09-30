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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercises the actual PDF renderer, PDF417 decoder, BCBP parser, HTTP airport
// client and PostgreSQL through the API. Only the external API is stubbed.
func TestSamplePDFEndToEnd(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		airports := map[string]string{
			"KSC": `{"code":"KSC","type":"airport","name":"Košice International","city":{"name":"Košice","country":{"name":"Slovakia"}}}`,
			"PRG": `{"code":"PRG","type":"airport","name":"Václav Havel Airport Prague","city":{"name":"Prague","country":{"name":"Czechia"}}}`,
		}
		result, ok := airports[r.URL.Query().Get("id")]
		if !assert.True(t, ok, "unexpected airport request: %s", r.URL) {
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
	require.NoError(t, err)
	defer scope.Close()
	handler := api.New(scope)
	data, err := os.ReadFile("../../testdata/boarding_pass.pdf")
	require.NoError(t, err)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "../../untrusted-filename.pdf")
	require.NoError(t, err)
	_, err = file.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest("POST", "/boarding-pass/parse-from-file", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result struct {
		DecodedBCBP bcbp.BoardingPass `json:"decoded_bcbp"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	pass := result.DecodedBCBP
	assert.Equal(t, "CYPRIAN/MICHAL", pass.PassengerName)
	require.Len(t, pass.Legs, 1)
	leg := pass.Legs[0]
	assert.Equal(t, bcbp.Leg{
		BookingReference: "ZKN85B", AirlineCode: "FR", FlightNumber: "7774", JulianDate: 105,
		CabinClass: "economy", Seat: "29C", PassengerStatus: "1",
		Origin:      bcbp.Airport{Code: "KSC", AirportName: "Košice International", CityName: "Košice", Country: "Slovakia"},
		Destination: bcbp.Airport{Code: "PRG", AirportName: "Václav Havel Airport Prague", CityName: "Prague", Country: "Czechia"},
	}, leg)
	require.NoError(t, scope.Close())
	_, err = scope.Repository().List(t.Context(), store.Filter{Limit: 20})
	assert.Error(t, err, "scope did not close the database")
	reopened, err := dependencies.NewServiceScope(t.Context(), cfg)
	require.NoError(t, err)
	defer reopened.Close()
	handler = api.New(reopened)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/boarding-passes?passenger_name=cyprian&airline_code=FR&limit=1", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page store.Page
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	assert.Equal(t, 1, page.Total)
	require.Len(t, page.Items, 1)
	assert.NotEmpty(t, page.Items[0].ID)
	assert.False(t, page.Items[0].ParsedAt.IsZero())
	assert.Equal(t, pass, page.Items[0].DecodedBCBP)
}

func TestMultipleLegsEndToEnd(t *testing.T) {
	t.Parallel()

	for count := 2; count <= 4; count++ {
		t.Run(fmt.Sprintf("%d legs", count), func(t *testing.T) {
			t.Parallel()

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
			require.NoError(t, err)
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
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				var page store.Page
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
				return page
			}
			if count == 2 {
				response := parse()
				assert.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
				page := list("")
				assert.Zero(t, page.Total)
				assert.Empty(t, page.Items, "saved a partially enriched pass")
			}
			response := parse()
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var result struct {
				DecodedBCBP bcbp.BoardingPass `json:"decoded_bcbp"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
			require.Len(t, result.DecodedBCBP.Legs, count)
			for i, leg := range result.DecodedBCBP.Legs {
				assert.Equal(t, codes[i], leg.Origin.Code, "leg %d", i)
				assert.Equal(t, codes[i+1], leg.Destination.Code, "leg %d", i)
				assert.Equal(t, carriers[i], leg.AirlineCode, "leg %d", i)
				assert.Equal(t, "Airport", leg.Origin.AirportName, "leg %d", i)
				assert.Equal(t, "Country", leg.Destination.Country, "leg %d", i)
			}
			page := list("?passenger_name=traveler&airline_code=BA&limit=1")
			assert.Equal(t, 1, page.Total)
			require.Len(t, page.Items, 1)
			assert.Len(t, page.Items[0].DecodedBCBP.Legs, count)
			mu.Lock()
			defer mu.Unlock()
			for _, code := range codes[:count+1] {
				want := 1
				if count == 2 && code == "PRG" {
					want = 2
				}
				assert.Equal(t, want, calls[code], "requests for airport %s", code)
			}
		})
	}
}
