package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

type requestContextKey string

const requestIDContextKey requestContextKey = "request_id"

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

func requestContext(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()

			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
			}

			ctx := context.WithValue(r.Context(), requestIDContextKey, requestID)
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
					"request_id", requestID,
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

				logger.Error("request_panic",
					"request_id", requestIDFromContext(r.Context()),
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
