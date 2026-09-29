// Package dependencies constructs and owns the application services.
package dependencies

import (
	"context"
	"log/slog"

	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/store"
)

// Base holds process-wide dependencies.
type Base interface {
	Logger() *slog.Logger
}

// ServiceScope is the wired application. Close it after the HTTP server stops.
type ServiceScope interface {
	Base() Base
	Parser() Parser
	Repository() store.Repository
	Close() error
}

// Parser is the parsing use case consumed by the HTTP layer.
type Parser interface {
	Parse(context.Context, []byte) (bcbp.BoardingPass, error)
}
