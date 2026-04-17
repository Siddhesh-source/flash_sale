package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/flashsale/backend/internal/metrics"
	"github.com/flashsale/backend/internal/ratelimit"
)

type contextKey string

const userIDKey contextKey = "user_id"

func RateLimitMiddleware(limiter *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := r.Header.Get("X-User-ID")
			if userID == "" {
				userID = r.RemoteAddr
			}

			ctx := context.WithValue(r.Context(), userIDKey, userID)
			r = r.WithContext(ctx)

			allowed, remaining, err := limiter.Allow(r.Context(), userID)
			if err != nil {
				http.Error(w, "Rate limiter error", http.StatusInternalServerError)
				return
			}

			if !allowed {
				metrics.RateLimitedTotal.WithLabelValues(userID).Inc()
				w.Header().Set("Retry-After", "1")
				w.Header().Set("X-RateLimit-Remaining", "0")
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			next.ServeHTTP(w, r)
		})
	}
}
