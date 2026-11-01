package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"boarding-pass/internal/base"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// requestContext stores the request ID in the request context (see
// base.WithRequestID), echoes it in X-Request-ID, and logs every response with
// its status. Loggers built with base.NewLogHandler add the request ID to every
// record logged with the request context.
func requestContext(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()

			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
			}

			ctx := base.WithRequestID(r.Context(), requestID)
			r = r.WithContext(ctx)
			w.Header().Set("X-Request-ID", requestID)
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := wrapped.Status()
				if status == 0 {
					status = http.StatusOK
				}
				level := slog.LevelInfo
				if status >= 500 {
					level = slog.LevelError
				} else if status >= 400 {
					level = slog.LevelWarn
				}
				logger.Log(ctx, level, "request_finished",
					"duration", time.Since(startedAt).Nanoseconds(),
					slog.Group("http",
						"method", r.Method,
						"url", r.URL.Path,
						"status_code", status,
						"response_size", wrapped.BytesWritten(),
					),
				)
			}()
			next.ServeHTTP(wrapped, r)
		})
	}
}

func recoverPanic(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}

				logger.ErrorContext(r.Context(), "request_panic",
					slog.Group("http", "method", r.Method, "url", r.URL.Path),
					slog.Group("error",
						"kind", fmt.Sprintf("%T", recovered),
						"message", fmt.Sprint(recovered),
						"stack", string(debug.Stack()),
					),
				)

				writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
			}()
			next.ServeHTTP(w, r)
		})
	}
}
