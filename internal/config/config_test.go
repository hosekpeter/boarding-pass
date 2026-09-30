package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validConfig() Config {
	return Config{
		HTTPAddr: DefaultHTTPAddr, LocationBaseURL: DefaultLocationBaseURL,
		Database: DBConfig{
			Host: "127.0.0.1", Port: 5432, User: "boarding_pass", Password: "boarding_pass",
			Name: "boarding_pass", SSLMode: "disable", Schema: "public",
			MaxConns: 20, MinConns: 2, StatementTimeout: 5 * time.Second, ConnectTimeout: 30 * time.Second,
		},
	}
}

func defaultEnvironment(t *testing.T) {
	t.Helper()
	// Environment variables are process-wide, so callers must stay sequential.
	for _, key := range []string{"HTTP_ADDR", "LOCATIONS_BASE_URL", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE", "DB_SCHEMA", "DB_MAX_CONNS", "DB_MIN_CONNS", "DB_STATEMENT_TIMEOUT", "DB_CONNECT_TIMEOUT"} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	defaultEnvironment(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, validConfig(), cfg)
}

func TestLoadOverrides(t *testing.T) {
	defaultEnvironment(t)
	for key, value := range map[string]string{
		"DB_HOST": "db.internal", "DB_PORT": "55432", "DB_USER": "custom", "DB_PASSWORD": "p@ss + word",
		"DB_NAME": "passes", "DB_SSLMODE": "require", "DB_SCHEMA": "custom",
		"DB_MAX_CONNS": "8", "DB_MIN_CONNS": "0", "DB_STATEMENT_TIMEOUT": "2s", "DB_CONNECT_TIMEOUT": "3s",
		"LOCATIONS_BASE_URL": "http://localhost:9000", "HTTP_ADDR": ":8081",
	} {
		t.Setenv(key, value)
	}
	t.Setenv("DB_DB_HOST", "must-not-be-used")
	cfg, err := Load()
	want := Config{HTTPAddr: ":8081", LocationBaseURL: "http://localhost:9000", Database: DBConfig{
		Host: "db.internal", Port: 55432, User: "custom", Password: "p@ss + word", Name: "passes", SSLMode: "require", Schema: "custom",
		MaxConns: 8, MinConns: 0, StatementTimeout: 2 * time.Second, ConnectTimeout: 3 * time.Second,
	}}
	require.NoError(t, err)
	assert.Equal(t, want, cfg)
}

func TestInvalidEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"address missing port", "HTTP_ADDR", "localhost"},
		{"address missing host separator", "HTTP_ADDR", "8080"},
		{"address negative port", "HTTP_ADDR", "localhost:-1"},
		{"address port too large", "HTTP_ADDR", "localhost:65536"},
		{"address named port", "HTTP_ADDR", "localhost:http"},
		{"address invalid host", "HTTP_ADDR", "not a host:8080"},
		{"address unbracketed IPv6", "HTTP_ADDR", "::1:8080"},
		{"relative URL", "LOCATIONS_BASE_URL", "locations"},
		{"unsupported URL scheme", "LOCATIONS_BASE_URL", "file:///tmp/airports"},
		{"URL credentials", "LOCATIONS_BASE_URL", "https://user:secret@example.com"},
		{"URL query", "LOCATIONS_BASE_URL", "https://example.com?key=secret"},
		{"URL empty query", "LOCATIONS_BASE_URL", "https://example.com?"},
		{"URL fragment", "LOCATIONS_BASE_URL", "https://example.com#fragment"},
		{"URL invalid port", "LOCATIONS_BASE_URL", "https://example.com:65536"},
		{"URL port zero", "LOCATIONS_BASE_URL", "http://localhost:0"},
		{"DB invalid host", "DB_HOST", "not a host"},
		{"DB invalid port", "DB_PORT", "65536"},
		{"DB zero port", "DB_PORT", "0"},
		{"DB nonnumeric port", "DB_PORT", "abc"},
		{"DB invalid TLS mode", "DB_SSLMODE", "invalid"},
		{"DB pool too small", "DB_MAX_CONNS", "1"},
		{"DB invalid minimum", "DB_MIN_CONNS", "-1"},
		{"DB zero statement timeout", "DB_STATEMENT_TIMEOUT", "0s"},
		{"DB submillisecond statement timeout", "DB_STATEMENT_TIMEOUT", "500us"},
		{"DB invalid connection timeout", "DB_CONNECT_TIMEOUT", "-1s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defaultEnvironment(t)
			t.Setenv("DB_PASSWORD", "secret")
			t.Setenv(tc.key, tc.value)
			cfg, err := Load()
			require.Error(t, err)
			assert.Zero(t, cfg)
			if tc.name == "DB nonnumeric port" {
				assert.ErrorContains(t, err, `field "Port"`)
			} else {
				assert.ErrorContains(t, err, tc.key)
			}
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestValidListenAddresses(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"127.0.0.1:8080", ":8080", "localhost:8080", "[::1]:8080", "[fe80::1%eth0]:8080", "127.0.0.1:0"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()

			cfg := validConfig()
			cfg.HTTPAddr = address
			require.NoError(t, cfg.Validate())
		})
	}
}

func TestValidationCollectsMissingFields(t *testing.T) {
	t.Parallel()

	err := (Config{}).Validate()
	require.Error(t, err)
	for _, key := range []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_NAME", "DB_SSLMODE", "LOCATIONS_BASE_URL", "HTTP_ADDR"} {
		assert.ErrorContains(t, err, key)
	}
}
