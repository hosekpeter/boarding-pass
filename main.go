package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"boarding-pass/internal/config"
	"boarding-pass/internal/dependencies"
	"boarding-pass/internal/httpserver"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scope, err := dependencies.NewServiceScope(ctx, cfg)
	if err != nil {
		return err
	}
	defer scope.Close()

	logger := scope.Base().Logger()

	logger.Info("server starting", "address", cfg.HTTPAddr)

	err = httpserver.New(scope, cfg).Start(ctx)

	logger.Info("server stopped")
	return err
}
