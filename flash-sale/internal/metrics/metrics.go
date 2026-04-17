package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ReservationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "reservation_total",
			Help: "Total reservation attempts",
		},
		[]string{"sale_id", "result"},
	)

	ReservationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "reservation_duration_seconds",
			Help:    "Reservation request duration",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1},
		},
		[]string{"sale_id"},
	)

	StockLevel = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stock_available",
			Help: "Current available stock",
		},
		[]string{"sale_id"},
	)

	WaitlistDepth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "waitlist_depth",
			Help: "Current waitlist size",
		},
		[]string{"sale_id"},
	)

	RateLimitedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limited_total",
			Help: "Requests rejected by rate limiter",
		},
		[]string{"user_id"},
	)

	ReconciliationCorrections = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "reconciliation_corrections_total",
			Help: "Redis/Postgres drift corrections",
		},
	)

	WebSocketConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "websocket_connections_active",
			Help: "Active WebSocket connections",
		},
	)

	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests",
		},
		[]string{"route", "method", "status"},
	)
)
