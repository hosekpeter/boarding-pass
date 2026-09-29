// Package httpserver runs the HTTP listener and handles graceful shutdown.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"boarding-pass/internal/api"
	"boarding-pass/internal/config"
	"boarding-pass/internal/dependencies"
)

const shutdownTimeout = 50 * time.Second

type Server struct {
	server *http.Server
	logger *slog.Logger
}

func New(scope dependencies.ServiceScope, cfg config.Config) *Server {
	return &Server{
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           api.New(scope),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      50 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    16 << 10,
		},
		logger: scope.Base().Logger(),
	}
}

func (s *Server) Start(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	stopped := make(chan error, 1)
	go func() {
		err := s.server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		stopped <- err
	}()

	select {
	case err := <-stopped:
		if err != nil {
			return fmt.Errorf("HTTP server: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.logger.Info("HTTP server shutting down")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.server.Shutdown(shutdown); err != nil {
		_ = s.server.Close()
		return fmt.Errorf("shutdown: %w", err)
	}
	return <-stopped
}
