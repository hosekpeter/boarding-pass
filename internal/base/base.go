// Package base holds process-wide, cross-cutting dependencies such as logging.
// Telemetry providers belong here too, so every component that receives Base
// gains them without changing its constructor.
package base

import "log/slog"

// Base exposes process-wide dependencies.
type Base interface {
	Logger() *slog.Logger
}

type base struct {
	logger *slog.Logger
}

func (b *base) Logger() *slog.Logger { return b.logger }

// New returns a Base backed by the given logger. Production code passes
// [NewJSONLogger], whose handler adds the request ID from the context.
func New(logger *slog.Logger) Base {
	return &base{logger: logger}
}
