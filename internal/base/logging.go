package base

import (
	"context"
	"io"
	"log/slog"
)

// RequestIDKey is the log attribute that carries the request ID.
const RequestIDKey = "request_id"

type requestIDKey struct{}

// WithRequestID returns a copy of ctx carrying the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID stored in ctx, or "" if there is none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// NewJSONLogger returns the application logger: JSON records written to w,
// enriched with context values by [NewLogHandler].
func NewJSONLogger(w io.Writer) *slog.Logger {
	return slog.New(NewLogHandler(slog.NewJSONHandler(w, nil)))
}

// NewLogHandler wraps h so that every record logged with a context carrying a
// request ID (see [WithRequestID]) gets a request_id attribute. Use the
// *Context logging methods to pass the context through.
func NewLogHandler(h slog.Handler) slog.Handler {
	if ch, ok := h.(contextHandler); ok {
		return ch
	}
	return contextHandler{h}
}

// shortcut: request_id is added inside any open WithGroup group and is not de-duplicated; add group replay when code uses WithGroup or logs request_id itself.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r = r.Clone()
		r.AddAttrs(slog.String(RequestIDKey, id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
