package skypicker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const DefaultBaseURL = "https://api.skypicker.com"

var ErrUnavailable = errors.New("airport lookup unavailable")

type cacheEntry struct {
	airport Airport
	expires time.Time
}

// Client resolves airports through Skypicker and caches successful lookups.
// It is safe for concurrent reuse across application services.
type Client struct {
	baseURL string
	http    *http.Client
	mu      sync.Mutex
	cache   map[string]cacheEntry
}

func New(baseURL string, client *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("invalid Locations API base URL")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: client, cache: make(map[string]cacheEntry)}, nil
}

// Lookup returns complete airport details for an IATA code.
func (c *Client) Lookup(ctx context.Context, code string) (Airport, error) {
	if err := ctx.Err(); err != nil {
		return Airport{}, err
	}

	c.mu.Lock()
	cached, ok := c.cache[code]
	c.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.airport, nil
	}
	// A per-call deadline also applies if an injected HTTP client has no timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/locations/id?"+url.Values{"id": {code}, "locale": {"en-US"}}.Encode(), nil)
	if err != nil {
		return Airport{}, err
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Airport{}, fmt.Errorf("%w: request failed", ErrUnavailable)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Airport{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}

	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return Airport{}, fmt.Errorf("%w: invalid response size", ErrUnavailable)
	}

	var result struct {
		Locations []struct {
			Code string `json:"code"`
			Name string `json:"name"`
			Type string `json:"type"`
			City struct {
				Name    string `json:"name"`
				Country struct {
					Name string `json:"name"`
				} `json:"country"`
			} `json:"city"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return Airport{}, fmt.Errorf("%w: invalid JSON", ErrUnavailable)
	}
	for _, loc := range result.Locations {
		if loc.Code != code || loc.Type != "airport" {
			continue
		}

		if loc.Name == "" || loc.City.Name == "" || loc.City.Country.Name == "" {
			continue
		}

		airport := Airport{Code: code, AirportName: loc.Name, CityName: loc.City.Name, Country: loc.City.Country.Name}

		c.mu.Lock()
		// Airport codes form a small keyspace; still cap memory even with bad input.
		if len(c.cache) >= 1024 {
			clear(c.cache)
		}
		c.cache[code] = cacheEntry{airport: airport, expires: time.Now().Add(24 * time.Hour)}
		c.mu.Unlock()
		return airport, nil
	}

	return Airport{}, fmt.Errorf("%w: airport %s not found", ErrUnavailable, code)
}
