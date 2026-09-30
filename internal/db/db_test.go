package db

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"boarding-pass/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanceledStartup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pool, err := Connect(ctx, config.DBConfig{})
	assert.Nil(t, pool)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestInvalidConfigDoesNotExposeCredentials(t *testing.T) {
	t.Parallel()

	pool, err := Connect(t.Context(), config.DBConfig{Host: "localhost", Port: 5432, User: "user", Password: "secret", Name: "passes", SSLMode: "invalid", ConnectTimeout: time.Second})
	assert.Nil(t, pool)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
}

func TestConnectTimeoutBoundsUnresponsiveServer(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	cfg := config.DBConfig{Host: host, Port: n, User: "user", Password: "password", Name: "passes", SSLMode: "disable", MaxConns: 1, ConnectTimeout: 50 * time.Millisecond, StatementTimeout: time.Second}
	started := time.Now()
	pool, err := Connect(t.Context(), cfg)
	assert.Nil(t, pool)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.LessOrEqual(t, time.Since(started), time.Second, "database startup exceeded its connection timeout")
}

func TestConnectRetainsLastFailureWithoutPassword(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	pool, err := Connect(t.Context(), config.DBConfig{
		Host: host, Port: n, User: "user", Password: "do-not-log-this-password", Name: "passes",
		SSLMode: "disable", MaxConns: 1, ConnectTimeout: 50 * time.Millisecond, StatementTimeout: time.Second,
	})
	assert.Nil(t, pool)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var networkErr *net.OpError
	assert.ErrorAs(t, err, &networkErr, "retain the connection failure as well as the retry deadline")
	assert.NotContains(t, err.Error(), "do-not-log-this-password")
}
