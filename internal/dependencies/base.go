package dependencies

import "log/slog"

type base struct {
	logger *slog.Logger
}

func (b *base) Logger() *slog.Logger { return b.logger }

func NewBase(logger *slog.Logger) Base {
	return &base{logger: logger}
}
