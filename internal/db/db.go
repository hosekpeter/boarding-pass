package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"boarding-pass/internal/config"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const retryInterval = 2 * time.Second

// Connect opens a pool from validated configuration and bounds startup retries.
func Connect(ctx context.Context, cfg config.DBConfig) (*pgxpool.Pool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(cfg.ConnectionString())
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 30 * time.Minute
	poolConfig.HealthCheckPeriod = time.Minute
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	var lastErr error
	for {
		lastErr = pool.Ping(ctx)
		if lastErr == nil {
			return pool, nil
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("connect to database: %w; last attempt: %w", ctx.Err(), lastErr)
		case <-time.After(retryInterval):
		}
	}
}

// RunMigrations serializes embedded migrations across concurrent application starts.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("database pool is nil")
	}
	// Migrations have the startup context deadline, not the shorter query timeout.
	// Use a dedicated connection so changed session settings cannot leak into the pool.
	migrationConfig := pool.Config().ConnConfig.Copy()
	migrationConfig.RuntimeParams["statement_timeout"] = "0"
	sqlDB := stdlib.OpenDB(*migrationConfig)
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	migrations, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return err
	}
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(726384901), lock.WithLockTimeout(1, 30))
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations,
		goose.WithSessionLocker(locker), goose.WithLogger(goose.NopLogger()))
	if err != nil {
		return fmt.Errorf("configure migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
