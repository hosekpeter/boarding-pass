package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"boarding-pass/internal/config"
	"boarding-pass/internal/dependencies"
	"boarding-pass/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeScope struct{}

func (fakeScope) Base() dependencies.Base {
	return dependencies.NewBase(slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func (fakeScope) Parser() dependencies.Parser  { return nil }
func (fakeScope) Repository() store.Repository { return nil }
func (fakeScope) Close() error                 { return nil }

func TestStartReportsBindFailure(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	server := New(fakeScope{}, config.Config{HTTPAddr: listener.Addr().String()})
	assert.Error(t, server.Start(t.Context()), "expected an error for an occupied port")
}

func TestStartWithCanceledContextDoesNotBind(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	server := New(fakeScope{}, config.Config{HTTPAddr: "invalid address"})
	assert.NoError(t, server.Start(ctx), "canceled startup tried to bind")
}

func TestShutdownWaitsForActiveRequest(t *testing.T) {
	t.Parallel()

	server := New(fakeScope{}, config.Config{HTTPAddr: "127.0.0.1:0"})
	address := make(chan string, 1)
	server.server.BaseContext = func(listener net.Listener) context.Context {
		address <- listener.Addr().String()
		return context.Background()
	}
	entered := make(chan struct{})
	released := make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	t.Cleanup(release)
	server.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-released
		_, _ = io.WriteString(w, "finished")
	})
	shutdown := make(chan struct{})
	server.server.RegisterOnShutdown(func() { close(shutdown) })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- server.Start(ctx) }()
	var addr string
	select {
	case addr = <-address:
	case err := <-done:
		require.FailNow(t, "server stopped during startup", "error: %v", err)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "server did not start")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	requestDone := make(chan error, 1)
	go func() {
		response, err := client.Get("http://" + addr)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		require.FailNow(t, "request did not reach the server")
	}
	cancel()
	select {
	case <-shutdown:
	case <-time.After(3 * time.Second):
		require.FailNow(t, "server did not initiate shutdown")
	}
	select {
	case err := <-done:
		require.FailNow(t, "server stopped before the active request finished", "error: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	assert.NoError(t, <-requestDone, "active request was interrupted")
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "server did not finish shutdown")
	}
}
