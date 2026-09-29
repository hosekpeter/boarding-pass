package dependencies

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"boarding-pass/internal/config"
	"boarding-pass/internal/db"

	"boarding-pass/internal/client/skypicker"
	"boarding-pass/internal/pdf"
	"boarding-pass/internal/service"
	"boarding-pass/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

type serviceScope struct {
	base       Base
	db         *pgxpool.Pool
	repository store.Repository
	parser     Parser
}

func (s *serviceScope) Base() Base                   { return s.base }
func (s *serviceScope) Repository() store.Repository { return s.repository }
func (s *serviceScope) Parser() Parser               { return s.parser }

func (s *serviceScope) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.db.Close()
	return nil
}

// NewServiceScope initializes the logger, PDF tools, airport client, database and
// parser. No partially initialized scope is returned on failure.
func NewServiceScope(ctx context.Context, cfg config.Config) (ServiceScope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate configuration: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	extractor, err := pdf.New()
	if err != nil {
		return nil, fmt.Errorf("initialize PDF extractor: %w", err)
	}

	airports, err := skypicker.New(cfg.LocationBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize airport client: %w", err)
	}

	database, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	migrationCtx, cancel := context.WithTimeout(ctx, cfg.Database.ConnectTimeout)
	defer cancel()
	if err := db.RunMigrations(migrationCtx, database); err != nil {
		database.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		database.Close()
		return nil, err
	}
	repository := store.New(database)
	return &serviceScope{
		base:       NewBase(logger),
		db:         database,
		repository: repository,
		parser:     service.New(extractor, airports, repository),
	}, nil
}
