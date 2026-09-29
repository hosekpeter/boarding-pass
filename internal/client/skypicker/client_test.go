package skypicker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const airportJSON = `{"locations":[{"code":"PRG","type":"airport","name":"Václav Havel Airport Prague","city":{"name":"Prague","country":{"name":"Czechia"}}}]}`

func TestLookupAndCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/locations/id" || r.URL.Query().Get("id") != "PRG" {
			t.Errorf("unexpected URL: %s", r.URL)
		}
		fmt.Fprint(w, airportJSON)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		airport, err := client.Lookup(context.Background(), "PRG")
		if err != nil {
			t.Fatal(err)
		}
		if airport.Code != "PRG" || airport.CityName != "Prague" || airport.Country != "Czechia" || airport.AirportName != "Václav Havel Airport Prague" {
			t.Fatalf("unexpected airport: %+v", airport)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cache missed: %d calls", calls.Load())
	}
	client.mu.Lock()
	entry := client.cache["PRG"]
	entry.expires = time.Now().Add(-time.Second)
	client.cache["PRG"] = entry
	client.mu.Unlock()
	if _, err := client.Lookup(context.Background(), "PRG"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("expired entry was reused")
	}
}

func TestLookupFailuresAreNotCached(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"upstream", 503, `{}`}, {"invalid JSON", 200, `{`}, {"no airport", 200, `{"locations":[]}`},
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
			client, _ := New(server.URL, server.Client())
			for i := 0; i < 2; i++ {
				if _, err := client.Lookup(context.Background(), "PRG"); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("expected lookup error: %v", err)
				}
			}
			if calls != 2 {
				t.Fatal("cached a failure")
			}
		})
	}
}

func TestLookupCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.Lookup(ctx, "PRG"); err == nil {
		t.Fatal("expected timeout")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := client.Lookup(canceled, "PRG"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}

func TestInvalidBaseURL(t *testing.T) {
	for _, url := range []string{"", "://bad", "file:///tmp/data", "https://example.com?secret=yes", "https://example.com?", "https://user:password@example.com", "https://example.com#fragment"} {
		if _, err := New(url, nil); err == nil {
			t.Errorf("accepted URL %q", url)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			airport, err := client.Lookup(t.Context(), "PRG")
			if err != nil || airport.Code != "PRG" {
				t.Errorf("concurrent lookup failed: %v", err)
			}
		})
	}
	wg.Wait()
	before := calls.Load()
	if _, err := client.Lookup(t.Context(), "PRG"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("shared client did not reuse cached result")
	}
}
