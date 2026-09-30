package dependencies_test

import (
	"context"
	"testing"
	"time"

	"boarding-pass/internal/config"
	"boarding-pass/internal/dependencies"

	"github.com/stretchr/testify/assert"
)

func TestCanceledStartupDoesNotConnect(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := config.Config{
		Database:        unreachableDatabase(),
		LocationBaseURL: config.DefaultLocationBaseURL,
		HTTPAddr:        config.DefaultHTTPAddr,
	}
	scope, err := dependencies.NewServiceScope(ctx, cfg)
	assert.Nil(t, scope)
	assert.ErrorIs(t, err, context.Canceled)
}

// PATH changes affect the whole process, so these tests must stay sequential.
func TestMissingPDFToolsFailsBeforeDatabaseConnection(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := config.Config{
		Database:        unreachableDatabase(),
		LocationBaseURL: config.DefaultLocationBaseURL,
		HTTPAddr:        config.DefaultHTTPAddr,
	}
	scope, err := dependencies.NewServiceScope(t.Context(), cfg)
	assert.Nil(t, scope)
	assert.ErrorContains(t, err, "initialize PDF extractor")
}

func TestInvalidConfigurationFailsBeforeResourceInitialization(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Configuration must be checked before PDF tools.
	cfg := config.Config{
		Database:        unreachableDatabase(),
		LocationBaseURL: config.DefaultLocationBaseURL,
		HTTPAddr:        "not-an-address",
	}
	scope, err := dependencies.NewServiceScope(t.Context(), cfg)
	assert.Nil(t, scope)
	assert.ErrorContains(t, err, "HTTP_ADDR")
}

func unreachableDatabase() config.DBConfig {
	return config.DBConfig{Host: "127.0.0.1", Port: 1, User: "user", Password: "password", Name: "unreachable", SSLMode: "disable", Schema: "public", MaxConns: 2, MinConns: 0, StatementTimeout: time.Second, ConnectTimeout: time.Second}
}
