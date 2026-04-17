package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/flashsale/backend/internal/metrics"
	"github.com/go-chi/chi/v5"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	capture    bool
}

func newResponseWriter(w http.ResponseWriter, capture bool) *responseWriter {
	return &responseWriter{ResponseWriter: w, statusCode: http.StatusOK, capture: capture}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.capture {
		rw.body.Write(b)
	}
	return rw.ResponseWriter.Write(b)
}

// MetricsMiddleware records HTTP request counts and latency for Prometheus.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := newResponseWriter(w, false)

		next.ServeHTTP(wrapped, r)

		// Use the chi route pattern, not the raw path (avoids high-cardinality labels)
		routePattern := chi.RouteContext(r.Context()).RoutePattern()
		if routePattern == "" {
			routePattern = r.URL.Path
		}

		status := strconv.Itoa(wrapped.statusCode)
		metrics.HTTPRequestsTotal.WithLabelValues(routePattern, r.Method, status).Inc()
	})
}

// IdempotencyMiddleware intercepts requests with Idempotency-Key headers, returns
// cached responses on replay, and caches fresh responses after the handler runs.
// It operates on POST /api/sales/:id/reserve and POST /api/reservations/:id/confirm.
func IdempotencyMiddleware(rdb interface {
	Get(ctx context.Context, key string) interface{ Result() (string, error) }
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) interface{ Err() error }
}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}

			iKey := r.Header.Get("Idempotency-Key")
			if iKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			cacheKey := "idempotency:" + iKey

			// Fast path: check Redis cache
			cached, err := rdb.Get(r.Context(), cacheKey).Result()
			if err == nil && cached != "" {
				var entry idempotencyEntry
				if json.Unmarshal([]byte(cached), &entry) == nil {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Idempotency-Replayed", "true")
					w.WriteHeader(entry.Status)
					w.Write([]byte(entry.Body))
					return
				}
			}

			// Slow path: run handler and cache result
			captured := newResponseWriter(w, true)
			next.ServeHTTP(captured, r)

			entry := idempotencyEntry{
				Status: captured.statusCode,
				Body:   captured.body.String(),
			}
			if data, err := json.Marshal(entry); err == nil {
				rdb.Set(r.Context(), cacheKey, string(data), 24*time.Hour)
			}
		})
	}
}

type idempotencyEntry struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}
