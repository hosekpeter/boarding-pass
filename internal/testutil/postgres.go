//go:build integration

// Package testutil provides isolated PostgreSQL schemas for integration tests.
package testutil

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"boarding-pass/internal/config"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// DatabaseURL creates one schema per test without modifying existing tables.
// Integration tests fail, rather than skip, if PostgreSQL is not configured.
func DatabaseURL(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("set TEST_DATABASE_URL to run PostgreSQL integration tests (see README)")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || strings.Trim(u.Path, "/") == "" {
		t.Fatal("TEST_DATABASE_URL must be a PostgreSQL URL with a host and database name")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid TEST_DATABASE_URL connection settings")
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("remove isolated test schema: %v", err)
		}
	})
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	return u.String()
}

// DatabaseConfig provides structured settings for the isolated schema.
func DatabaseConfig(t *testing.T) config.DBConfig {
	t.Helper()
	dsn := DatabaseURL(t)
	u, _ := url.Parse(dsn)
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database connection settings")
	}
	sslMode := u.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "prefer"
	}
	return config.DBConfig{
		Host: parsed.Host, Port: int(parsed.Port), User: parsed.User, Password: parsed.Password,
		Name: parsed.Database, SSLMode: sslMode, Schema: u.Query().Get("search_path"),
		MaxConns: 20, MinConns: 2, StatementTimeout: 5 * time.Second, ConnectTimeout: 10 * time.Second,
	}
}
