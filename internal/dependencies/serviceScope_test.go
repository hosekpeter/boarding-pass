package dependencies_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"boarding-pass/internal/config"
	"boarding-pass/internal/dependencies"
)

func TestCanceledStartupDoesNotConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := config.Config{
		Database:        unreachableDatabase(),
		LocationBaseURL: config.DefaultLocationBaseURL,
		HTTPAddr:        config.DefaultHTTPAddr,
	}
	scope, err := dependencies.NewServiceScope(ctx, cfg)
	if !errors.Is(err, context.Canceled) || scope != nil {
		t.Fatalf("scope = %v, error = %v", scope, err)
	}
}

func TestMissingPDFToolsFailsBeforeDatabaseConnection(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := config.Config{
		Database:        unreachableDatabase(),
		LocationBaseURL: config.DefaultLocationBaseURL,
		HTTPAddr:        config.DefaultHTTPAddr,
	}
	scope, err := dependencies.NewServiceScope(t.Context(), cfg)
	if err == nil || scope != nil || !strings.Contains(err.Error(), "initialize PDF extractor") {
		t.Fatalf("scope = %v, error = %v", scope, err)
	}
}

func TestInvalidConfigurationFailsBeforeResourceInitialization(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Configuration must be checked before PDF tools.
	cfg := config.Config{
		Database:        unreachableDatabase(),
		LocationBaseURL: config.DefaultLocationBaseURL,
		HTTPAddr:        "not-an-address",
	}
	scope, err := dependencies.NewServiceScope(t.Context(), cfg)
	if scope != nil || err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
		t.Fatalf("scope = %v, error = %v", scope, err)
	}
}

func unreachableDatabase() config.DBConfig {
	return config.DBConfig{Host: "127.0.0.1", Port: 1, User: "user", Password: "password", Name: "unreachable", SSLMode: "disable", Schema: "public", MaxConns: 2, MinConns: 0, StatementTimeout: time.Second, ConnectTimeout: time.Second}
}
