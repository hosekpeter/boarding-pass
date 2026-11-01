package base

import (
	"context"
	"io"
	"log/slog"
	"slices"
)

// RequestIDKey is the top-level log attribute that carries the request ID.
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
// request ID (see [WithRequestID]) gets a top-level request_id attribute. Use
// the *Context logging methods to pass the context through.
//
// The attribute is added at most once and stays at the top level even after
// [slog.Handler.WithGroup].
func NewLogHandler(h slog.Handler) slog.Handler {
	if ch, ok := h.(*contextHandler); ok {
		return ch
	}
	return &contextHandler{root: h, handler: h}
}

type contextHandler struct {
	// root is the wrapped handler without any WithAttrs/WithGroup applied.
	root slog.Handler
	// handler is root with ops applied.
	handler slog.Handler
	// ops replays WithAttrs/WithGroup calls on a handler that already carries
	// request_id; it is only needed once a group was opened.
	ops      []func(slog.Handler) slog.Handler
	grouped  bool
	hasReqID bool
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	id := RequestID(ctx)
	if id == "" || h.hasReqID {
		return h.handler.Handle(ctx, r)
	}
	if h.grouped {
		// Record attributes would be qualified by the open groups, so rebuild
		// the handler chain with request_id attached before the first group.
		handler := h.root.WithAttrs([]slog.Attr{slog.String(RequestIDKey, id)})
		for _, op := range h.ops {
			handler = op(handler)
		}
		return handler.Handle(ctx, r)
	}

	// Detecting an explicit request_id requires resolving LogValuers. The
	// resolved values are passed on, so the wrapped handler does not call
	// LogValue a second time.
	attrs, resolved := resolveRecordAttrs(r)
	found := slices.ContainsFunc(attrs, isRequestIDAttr)
	switch {
	case resolved:
		out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
		out.AddAttrs(attrs...)
		if !found {
			out.AddAttrs(slog.String(RequestIDKey, id))
		}
		return h.handler.Handle(ctx, out)
	case found:
		return h.handler.Handle(ctx, r)
	default:
		r = r.Clone()
		r.AddAttrs(slog.String(RequestIDKey, id))
		return h.handler.Handle(ctx, r)
	}
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	// The op may run again for every record once a group is open, possibly
	// concurrently, and the receiving handler owns the slice it is given.
	next := h.with(func(handler slog.Handler) slog.Handler { return handler.WithAttrs(slices.Clone(attrs)) })
	if !h.grouped && slices.ContainsFunc(attrs, isRequestIDAttr) {
		next.hasReqID = true
	}
	return next
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := h.with(func(handler slog.Handler) slog.Handler { return handler.WithGroup(name) })
	next.grouped = true
	return next
}

func (h *contextHandler) with(op func(slog.Handler) slog.Handler) *contextHandler {
	return &contextHandler{
		root:     h.root,
		handler:  op(h.handler),
		ops:      append(slices.Clip(h.ops), op),
		grouped:  h.grouped,
		hasReqID: h.hasReqID,
	}
}

// resolveRecordAttrs returns the attributes of r with LogValuers resolved at
// the top level and inside inline groups, the levels [isRequestIDAttr]
// inspects. resolved reports whether any LogValuer was found; if not, attrs
// equal the record's own attributes.
func resolveRecordAttrs(r slog.Record) (attrs []slog.Attr, resolved bool) {
	attrs = make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		a, changed := resolveAttr(a)
		attrs = append(attrs, a)
		resolved = resolved || changed
		return true
	})
	return attrs, resolved
}

func resolveAttr(a slog.Attr) (slog.Attr, bool) {
	changed := a.Value.Kind() == slog.KindLogValuer
	if changed {
		a.Value = a.Value.Resolve()
	}
	if a.Key != "" || a.Value.Kind() != slog.KindGroup {
		return a, changed
	}
	// The group's slice belongs to the caller's value, so copy it before
	// replacing members.
	var members []slog.Attr
	for i, m := range a.Value.Group() {
		if m, ok := resolveAttr(m); ok {
			if members == nil {
				members = slices.Clone(a.Value.Group())
			}
			members[i] = m
		}
	}
	if members != nil {
		a.Value = slog.GroupValue(members...)
		changed = true
	}
	return a, changed
}

// isRequestIDAttr reports whether a yields a top-level request_id, looking
// into inline groups (empty key), whose members handlers put at the current
// level. An empty group is omitted by handlers, so it does not count. Resolve
// is a no-op for attributes already passed through [resolveAttr].
func isRequestIDAttr(a slog.Attr) bool {
	v := a.Value.Resolve()
	isGroup := v.Kind() == slog.KindGroup
	switch {
	case a.Key == "" && isGroup:
		return slices.ContainsFunc(v.Group(), isRequestIDAttr)
	case a.Key == RequestIDKey:
		return !isGroup || len(v.Group()) > 0
	default:
		return false
	}
}
