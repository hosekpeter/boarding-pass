package base_test

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
			name: "empty request ID",
			ctx:  func(t *testing.T) context.Context { return base.WithRequestID(t.Context(), "") },
			log:  func(ctx context.Context, l *slog.Logger) { l.InfoContext(ctx, "msg") },
			want: `{"msg":"msg"}`,
		},
		{
			name: "with attrs",
			ctx:  withID,
			log:  func(ctx context.Context, l *slog.Logger) { l.With("component", "api").InfoContext(ctx, "msg") },
			want: `{"msg":"msg","component":"api","request_id":"req-1"}`,
		},
		{
			name: "with group stays top level",
			ctx:  withID,
			log: func(ctx context.Context, l *slog.Logger) {
				l.With("component", "api").WithGroup("http").With("method", "GET").InfoContext(ctx, "msg", "status", 200)
			},
			want: `{"msg":"msg","component":"api","request_id":"req-1","http":{"method":"GET","status":200}}`,
		},
		{
			name: "explicit attr is not duplicated",
			ctx:  withID,
			log:  func(ctx context.Context, l *slog.Logger) { l.InfoContext(ctx, "msg", "request_id", "explicit") },
			want: `{"msg":"msg","request_id":"explicit"}`,
		},
		{
			name: "attr from With is not duplicated",
			ctx:  withID,
			log: func(ctx context.Context, l *slog.Logger) {
				l.With("request_id", "explicit").WithGroup("g").InfoContext(ctx, "msg", "k", "v")
			},
			want: `{"msg":"msg","request_id":"explicit","g":{"k":"v"}}`,
		},
		{
			name: "attr in inline group is not duplicated",
			ctx:  withID,
			log: func(ctx context.Context, l *slog.Logger) {
				l.InfoContext(ctx, "msg", slog.Group("", "request_id", "explicit"))
			},
			want: `{"msg":"msg","request_id":"explicit"}`,
		},
		{
			name: "attr in nested inline group is not duplicated",
			ctx:  withID,
			log: func(ctx context.Context, l *slog.Logger) {
				l.InfoContext(ctx, "msg", slog.Group("", slog.Group("", "request_id", "explicit")))
			},
			want: `{"msg":"msg","request_id":"explicit"}`,
		},
		{
			name: "inline group attr from With is not duplicated",
			ctx:  withID,
			log: func(ctx context.Context, l *slog.Logger) {
				l.With(slog.Group("", "request_id", "explicit")).InfoContext(ctx, "msg")
			},
			want: `{"msg":"msg","request_id":"explicit"}`,
		},
		{
			name: "inline group attr from With before group is not duplicated",
			ctx:  withID,
			log: func(ctx context.Context, l *slog.Logger) {
				l.With(slog.Group("", "request_id", "explicit")).WithGroup("g").InfoContext(ctx, "msg", "k", "v")
			},
			want: `{"msg":"msg","request_id":"explicit","g":{"k":"v"}}`,
		},
		{
			name: "empty group does not count",
			ctx:  withID,
			log:  func(ctx context.Context, l *slog.Logger) { l.InfoContext(ctx, "msg", slog.Group("request_id")) },
			want: `{"msg":"msg","request_id":"req-1"}`,
		},
		{
			name: "named group does not count",
			ctx:  withID,
			log: func(ctx context.Context, l *slog.Logger) {
				l.InfoContext(ctx, "msg", slog.Group("g", "request_id", "nested"))
			},
			want: `{"msg":"msg","request_id":"req-1","g":{"request_id":"nested"}}`,
		},
		{
			name: "attr in group does not count",
			ctx:  withID,
			log: func(ctx context.Context, l *slog.Logger) {
				l.WithGroup("g").InfoContext(ctx, "msg", "request_id", "nested")
			},
			want: `{"msg":"msg","request_id":"req-1","g":{"request_id":"nested"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			tc.log(tc.ctx(t), newLogger(&out))
			assert.JSONEq(t, tc.want, out.String())
			assert.Equal(t, 1, strings.Count(out.String(), "\n"), "exactly one record")
			var record map[string]any
			assert.NoError(t, jsonv2.Unmarshal(out.Bytes(), &record), "duplicate keys")
		})
	}
}

func TestLogHandlerRecordIsNotMutated(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logger := newLogger(&out)
	ctx := base.WithRequestID(t.Context(), "req-1")
	logger.InfoContext(ctx, "first")
	logger.InfoContext(t.Context(), "second")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2)
	assert.JSONEq(t, `{"msg":"first","request_id":"req-1"}`, lines[0])
	assert.JSONEq(t, `{"msg":"second"}`, lines[1])
}

// countingValuer counts LogValue calls; the counter is shared by copies.
type countingValuer struct {
	calls *atomic.Int32
	value slog.Value
}

func (v countingValuer) LogValue() slog.Value {
	v.calls.Add(1)
	return v.value
}

func TestLogHandlerResolvesLogValuerOnce(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value slog.Value
		attr  func(slog.LogValuer) slog.Attr
		want  string
	}{
		{
			name:  "top level",
			value: slog.StringValue("v"),
			attr:  func(v slog.LogValuer) slog.Attr { return slog.Any("k", v) },
			want:  `{"msg":"msg","k":"v","request_id":"req-1"}`,
		},
		{
			name:  "inside inline group",
			value: slog.StringValue("v"),
			attr:  func(v slog.LogValuer) slog.Attr { return slog.Group("", slog.Any("k", v)) },
			want:  `{"msg":"msg","k":"v","request_id":"req-1"}`,
		},
		{
			name:  "resolves to request ID",
			value: slog.StringValue("explicit"),
			attr:  func(v slog.LogValuer) slog.Attr { return slog.Any("request_id", v) },
			want:  `{"msg":"msg","request_id":"explicit"}`,
		},
		{
			name:  "resolves to inline group with request ID",
			value: slog.GroupValue(slog.String("request_id", "explicit")),
			attr:  func(v slog.LogValuer) slog.Attr { return slog.Any("", v) },
			want:  `{"msg":"msg","request_id":"explicit"}`,
		},
		{
			name:  "resolves to empty group",
			value: slog.GroupValue(),
			attr:  func(v slog.LogValuer) slog.Attr { return slog.Any("request_id", v) },
			want:  `{"msg":"msg","request_id":"req-1"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			logger := newLogger(&out)
			ctx := base.WithRequestID(t.Context(), "req-1")
			valuer := countingValuer{calls: new(atomic.Int32), value: tc.value}

			const records = 3
			for i := range records {
				logger.LogAttrs(ctx, slog.LevelInfo, "msg", tc.attr(valuer))
				assert.Equal(t, int32(i+1), valuer.calls.Load(), "LogValue calls after record %d", i+1)
			}

			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			require.Len(t, lines, records)
			for _, line := range lines {
				assert.JSONEq(t, tc.want, line)
				var record map[string]any
				assert.NoError(t, jsonv2.Unmarshal([]byte(line), &record), "duplicate keys")
			}
		})
	}
}

// ownedAttrsHandler exercises the slog contract that a handler owns the slice
// passed to WithAttrs: it keeps a copy and clears the original.
type ownedAttrsHandler struct{ slog.Handler }

func (h ownedAttrsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := ownedAttrsHandler{h.Handler.WithAttrs(slices.Clone(attrs))}
	clear(attrs)
	return next
}

func (h ownedAttrsHandler) WithGroup(name string) slog.Handler {
	return ownedAttrsHandler{h.Handler.WithGroup(name)}
}

func TestLogHandlerPassesOwnedAttrs(t *testing.T) {
	t.Parallel()

	// slog.JSONHandler serializes writes to out across its derived handlers.
	var out bytes.Buffer
	logger := slog.New(base.NewLogHandler(ownedAttrsHandler{jsonHandler(&out)})).
		With("component", "api").WithGroup("g")
	ctx := base.WithRequestID(t.Context(), "req-1")

	const records = 8
	var wg sync.WaitGroup
	for range records {
		wg.Go(func() { logger.InfoContext(ctx, "msg", "k", "v") })
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, records)
	for _, line := range lines {
		assert.JSONEq(t, `{"msg":"msg","request_id":"req-1","component":"api","g":{"k":"v"}}`, line)
	}
}

func TestNewLogHandlerDoesNotWrapTwice(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	h := base.NewLogHandler(base.NewLogHandler(jsonHandler(&out)))
	slog.New(h).InfoContext(base.WithRequestID(t.Context(), "req-1"), "msg")
	assert.JSONEq(t, `{"msg":"msg","request_id":"req-1"}`, out.String())
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
