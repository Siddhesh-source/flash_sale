package api

import (
	"github.com/flashsale/backend/internal/api/handlers"
	"github.com/flashsale/backend/internal/inventory"
	"github.com/flashsale/backend/internal/ratelimit"
	"github.com/flashsale/backend/internal/reservation"
	"github.com/flashsale/backend/internal/waitlist"
	ws "github.com/flashsale/backend/internal/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/go-chi/chi/v5/otelchi"
)

func NewRouter(
	inventoryService *inventory.Service,
	reservationService *reservation.Service,
	waitlistService *waitlist.Service,
	wsHandler *ws.Handler,
	db *pgxpool.Pool,
	rateLimiter *ratelimit.Limiter,
) *chi.Mux {
	r := chi.NewRouter()

	// Core middleware
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware("flash-sale", otelchi.WithChiRoutes(r)))
	r.Use(MetricsMiddleware)
	r.Use(LoggingMiddleware)
	r.Use(RecoveryMiddleware)

	salesHandler := handlers.NewSalesHandler(inventoryService)
	reservationsHandler := handlers.NewReservationsHandler(reservationService)
	waitlistHandler := handlers.NewWaitlistHandler(waitlistService)
	ordersHandler := handlers.NewOrdersHandler(db)

	// Prometheus scrape endpoint
	r.Handle("/metrics", promhttp.Handler())
	r.Get("/healthz", HealthzHandler)

	r.Route("/api", func(r chi.Router) {
		r.Post("/sales", salesHandler.CreateSale)
		r.Get("/sales/{id}", salesHandler.GetSale)
		r.Patch("/sales/{id}/status", salesHandler.UpdateSaleStatus)
		
		r.With(RateLimitMiddleware(rateLimiter)).Post("/sales/{id}/reserve", reservationsHandler.Reserve)
		r.With(RateLimitMiddleware(rateLimiter)).Post("/sales/{id}/waitlist", waitlistHandler.Join)
		
		r.Get("/sales/{id}/waitlist/position", waitlistHandler.GetPosition)
		r.Delete("/sales/{id}/waitlist", waitlistHandler.Leave)

		r.Post("/reservations/{id}/confirm", reservationsHandler.Confirm)
		r.Delete("/reservations/{id}", reservationsHandler.Release)

		r.Get("/orders/{id}", ordersHandler.GetOrder)
	})

	r.Get("/ws/sales/{id}", wsHandler.HandleWebSocket)

	return r
}
