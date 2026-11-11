package base_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"boarding-pass/internal/base"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestIDContext(t *testing.T) {
	t.Parallel()

	assert.Empty(t, base.RequestID(t.Context()))
	assert.Equal(t, "req-1", base.RequestID(base.WithRequestID(t.Context(), "req-1")))
}

func TestLogHandlerAddsRequestID(t *testing.T) {
	t.Parallel()

	withID := func(t *testing.T) context.Context { return base.WithRequestID(t.Context(), "req-1") }
	for _, tc := range []struct {
		name string
		ctx  func(*testing.T) context.Context
		log  func(context.Context, *slog.Logger)
		want string
	}{
		{
			name: "present",
			ctx:  withID,
			log:  func(ctx context.Context, l *slog.Logger) { l.InfoContext(ctx, "msg", "k", "v") },
			want: `{"msg":"msg","k":"v","request_id":"req-1"}`,
		},
		{
			name: "absent",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			log:  func(ctx context.Context, l *slog.Logger) { l.InfoContext(ctx, "msg", "k", "v") },
			want: `{"msg":"msg","k":"v"}`,
		},
		{
			name: "without context",
			ctx:  withID,
			log:  func(_ context.Context, l *slog.Logger) { l.Info("msg") },
			want: `{"msg":"msg"}`,
		},
		{
			name: "with attrs",
			ctx:  withID,
			log:  func(ctx context.Context, l *slog.Logger) { l.With("component", "api").InfoContext(ctx, "msg") },
			want: `{"msg":"msg","component":"api","request_id":"req-1"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			tc.log(tc.ctx(t), newLogger(&out))
			assert.JSONEq(t, tc.want, out.String())
			assert.Equal(t, 1, strings.Count(out.String(), "\n"), "exactly one record")
			assert.Equal(t, strings.Count(tc.want, `"request_id"`), strings.Count(out.String(), `"request_id"`))
		})
	}
}

func TestLogHandlerRecordIsNotMutated(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logger := newLogger(&out)
	logger.InfoContext(base.WithRequestID(t.Context(), "req-1"), "first")
	logger.InfoContext(t.Context(), "second")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2)
	assert.JSONEq(t, `{"msg":"first","request_id":"req-1"}`, lines[0])
	assert.JSONEq(t, `{"msg":"second"}`, lines[1])
}

func TestLogHandlerRespectsLevel(t *testing.T) {
	t.Parallel()

	h := base.NewLogHandler(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	assert.False(t, h.Enabled(t.Context(), slog.LevelInfo))
	assert.True(t, h.Enabled(t.Context(), slog.LevelWarn))
}

func TestNewJSONLogger(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	base.NewJSONLogger(&out).InfoContext(base.WithRequestID(t.Context(), "req-1"), "msg")
	var record map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &record))
	assert.Equal(t, "req-1", record["request_id"])
	assert.Equal(t, "INFO", record["level"])
}

// jsonHandler omits time and level so records can be compared verbatim.
func jsonHandler(out *bytes.Buffer) slog.Handler {
	return slog.NewJSONHandler(out, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
				return slog.Attr{}
			}
			return a
		},
	})
}

func newLogger(out *bytes.Buffer) *slog.Logger { return slog.New(base.NewLogHandler(jsonHandler(out))) }
