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
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pool, err := Connect(ctx, config.DBConfig{})
	assert.Nil(t, pool)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestInvalidConfigDoesNotExposeCredentials(t *testing.T) {
	pool, err := Connect(t.Context(), config.DBConfig{Host: "localhost", Port: 5432, User: "user", Password: "secret", Name: "passes", SSLMode: "invalid", ConnectTimeout: time.Second})
	assert.Nil(t, pool)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
}

func TestConnectTimeoutBoundsUnresponsiveServer(t *testing.T) {
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
