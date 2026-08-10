// Package httpx builds the chi router every service uses, with the
// standard middleware stack: request ID, panic recovery, otelhttp tracing,
// and structured request logging.
package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	htrotel "github.com/dhiazfathra/how-to-replicate/services/internal/otel"
)

// NewRouter builds a chi.Router for serviceName with the standard
// middleware stack applied, in order: request ID, panic recovery, otelhttp
// tracing, and structured request logging. logger defaults to
// slog.Default() when nil.
func NewRouter(serviceName string, logger *slog.Logger) chi.Router {
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return htrotel.Middleware(serviceName, next)
	})
	r.Use(requestLogger(logger))

	return r
}

// requestLogger logs each request's method, path, status, and duration at
// info level once the handler completes.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.Info("http_request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}
