package db

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"boarding-pass/internal/config"
)

func TestCanceledStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pool, err := Connect(ctx, config.DBConfig{})
	if pool != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected canceled startup: %v", err)
	}
}

func TestInvalidConfigDoesNotExposeCredentials(t *testing.T) {
	pool, err := Connect(t.Context(), config.DBConfig{Host: "localhost", Port: 5432, User: "user", Password: "secret", Name: "passes", SSLMode: "invalid", ConnectTimeout: time.Second})
	if pool != nil || err == nil {
		t.Fatal("accepted invalid connection configuration")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("database error exposed credentials")
	}
}

func TestConnectTimeoutBoundsUnresponsiveServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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
	if pool != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded timeout: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("database startup exceeded its connection timeout")
	}
}
