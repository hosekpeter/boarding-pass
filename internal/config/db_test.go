package config

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.NoError(t, cfg.Validate())
			parsed, err := pgx.ParseConfig(cfg.Database.ConnectionString())
			require.NoError(t, err)
			assert.Equal(t, host, parsed.Host)
			assert.Equal(t, cfg.Database.Port, int(parsed.Port))
			assert.Equal(t, cfg.Database.User, parsed.User)
			assert.Equal(t, cfg.Database.Password, parsed.Password)
			assert.Equal(t, cfg.Database.Name, parsed.Database)
			assert.Equal(t, "5000", parsed.RuntimeParams["statement_timeout"])
			assert.Equal(t, `"schema with spaces"`, parsed.RuntimeParams["search_path"])
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
			require.Error(t, err)
			assert.ErrorContains(t, err, field)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}
