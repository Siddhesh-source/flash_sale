package slo

// SLO 1 — Availability
// Target: 99.9% of POST /reserve requests must return 2xx or 4xx (not 5xx)
// Error budget: 0.1% → at 100 req/s, you can have 8.64 failed requests per day
// Prometheus query: 1 - (rate(http_requests_total{route="/reserve",status=~"5.."}[5m]) / rate(http_requests_total{route="/reserve"}[5m]))
const (
	AvailabilitySLO = 0.999 // 99.9%
)

// SLO 2 — Latency
// Target: p99 of POST /reserve < 100ms under load
// Error budget: 1% of requests may exceed 100ms
// Prometheus query: histogram_quantile(0.99, rate(reservation_duration_seconds_bucket[5m]))
const (
	LatencyP99TargetSeconds = 0.1   // 100ms
	LatencyErrorBudget      = 0.01  // 1%
)

// SLO 3 — Correctness
// Target: zero oversell events (final orders <= total_stock, always)
// Prometheus query: track reconciliation_corrections_total; any nonzero value is a correctness event
const (
	CorrectnessTarget = 1.0 // 100% - zero oversells allowed
)
