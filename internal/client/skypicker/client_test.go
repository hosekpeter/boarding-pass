package skypicker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const airportJSON = `{"locations":[{"code":"PRG","type":"airport","name":"Václav Havel Airport Prague","city":{"name":"Prague","country":{"name":"Czechia"}}}]}`

func TestLookupAndCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/locations/id", r.URL.Path)
		assert.Equal(t, "PRG", r.URL.Query().Get("id"))
		fmt.Fprint(w, airportJSON)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		airport, err := client.Lookup(context.Background(), "PRG")
		require.NoError(t, err)
		assert.Equal(t, Airport{Code: "PRG", CityName: "Prague", Country: "Czechia", AirportName: "Václav Havel Airport Prague"}, airport)
	}
	assert.Equal(t, int32(1), calls.Load(), "cache missed")
	client.mu.Lock()
	entry := client.cache["PRG"]
	entry.expires = time.Now().Add(-time.Second)
	client.cache["PRG"] = entry
	client.mu.Unlock()
	_, err = client.Lookup(context.Background(), "PRG")
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "expired entry was reused")
}

func TestLookupFailuresAreNotCached(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"upstream", 503, `{}`}, {"invalid JSON", 200, `{`}, {"missing locations", 200, `{}`},
		{"wrong airport", 200, strings.ReplaceAll(airportJSON, "PRG", "KSC")},
		{"city instead of airport", 200, strings.ReplaceAll(airportJSON, `"type":"airport"`, `"type":"city"`)},
		{"incomplete", 200, `{"locations":[{"code":"PRG","type":"airport"}]}`},
		{"oversized", 200, strings.Repeat(" ", (1<<20)+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client, err := New(server.URL, server.Client())
			require.NoError(t, err)
			for i := 0; i < 2; i++ {
				_, err = client.Lookup(context.Background(), "PRG")
				require.ErrorIs(t, err, ErrUnavailable)
			}
			assert.Equal(t, 2, calls, "cached a failure")
		})
	}
}

func TestUnknownAirportIsNotAnOutage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"locations":[]}`)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	require.NoError(t, err)
	_, err = client.Lookup(t.Context(), "ZZZ")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.NotErrorIs(t, err, ErrUnavailable)
}

func TestLookupCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.Lookup(ctx, "PRG")
	assert.Error(t, err, "expected timeout")
	canceled, stop := context.WithCancel(context.Background())
	stop()
	_, err = client.Lookup(canceled, "PRG")
	require.ErrorIs(t, err, context.Canceled)
}

func TestInvalidBaseURL(t *testing.T) {
	for _, url := range []string{"", "://bad", "file:///tmp/data", "https://example.com?secret=yes", "https://example.com?", "https://user:password@example.com", "https://example.com#fragment"} {
		_, err := New(url, nil)
		assert.Error(t, err, "accepted URL %q", url)
	}
}

func TestConcurrentReuse(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, airportJSON)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			airport, err := client.Lookup(t.Context(), "PRG")
			if assert.NoError(t, err, "concurrent lookup failed") {
				assert.Equal(t, "PRG", airport.Code)
			}
		})
	}
	wg.Wait()
	before := calls.Load()
	_, err = client.Lookup(t.Context(), "PRG")
	require.NoError(t, err)
	assert.Equal(t, before, calls.Load(), "shared client did not reuse cached result")
}
