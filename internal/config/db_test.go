package config

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestConnectionString(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "db.internal", "::1", "fe80::1%eth0"} {
		t.Run(host, func(t *testing.T) {
			cfg := validConfig()
			cfg.Database.Host = host
			cfg.Database.User = "user +:@/%"
			cfg.Database.Password = "p@ss +:/?#%word"
			cfg.Database.Name = "passes /?#%+"
			cfg.Database.Schema = "schema with spaces"
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			parsed, err := pgx.ParseConfig(cfg.Database.ConnectionString())
			if err != nil {
				t.Fatal("generated connection string is invalid")
			}
			if parsed.Host != host || int(parsed.Port) != cfg.Database.Port || parsed.User != cfg.Database.User || parsed.Password != cfg.Database.Password || parsed.Database != cfg.Database.Name {
				t.Fatal("connection string changed a connection parameter")
			}
			if parsed.RuntimeParams["statement_timeout"] != "5000" || parsed.RuntimeParams["search_path"] != `"schema with spaces"` {
				t.Fatalf("connection string lost query settings: %q", parsed.RuntimeParams)
			}
		})
	}
}

func TestDatabaseFieldsCannotContainNUL(t *testing.T) {
	for _, field := range []string{"DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SCHEMA"} {
		t.Run(field, func(t *testing.T) {
			cfg := validConfig()
			fields := map[string]*string{"DB_USER": &cfg.Database.User, "DB_PASSWORD": &cfg.Database.Password, "DB_NAME": &cfg.Database.Name, "DB_SCHEMA": &cfg.Database.Schema}
			*fields[field] = "secret\x00value"
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), field) || strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid database field was accepted or exposed")
			}
		})
	}
}
