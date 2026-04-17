package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/flashsale/backend/internal/config"
	"github.com/flashsale/backend/internal/events"
	"github.com/flashsale/backend/internal/inventory"
	"github.com/flashsale/backend/internal/models"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/flashsale/backend/internal/reservation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func setupInMemoryTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		tp.Shutdown(ctx)
	})
	return recorder
}

func TestSpanCreatedOnReserve(t *testing.T) {
	recorder := setupInMemoryTracer(t)

	cfg, err := config.Load()
	require.NoError(t, err)

	rc, err := redisclient.NewClient(cfg.RedisURL)
	require.NoError(t, err)
	defer rc.Close()

	db, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	require.NoError(t, err)
	defer db.Close()

	publisher := events.NewPublisher(rc.GetClient())
	invSvc := inventory.NewService(rc, db)
	resSvc := reservation.NewService(rc, db, cfg.ReservationTTLSeconds, publisher)

	ctx := context.Background()
	sale := &models.Sale{
		Name:       "Tracer Test Sale",
		TotalStock: 5,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(time.Hour),
	}
	require.NoError(t, invSvc.CreateSale(ctx, sale))

	userID := uuid.New()
	itemID := uuid.New()

	_, err = resSvc.Reserve(ctx, userID, sale.ID, itemID, "")
	require.NoError(t, err)

	// Give the span processor a moment to flush
	time.Sleep(20 * time.Millisecond)

	spans := recorder.Ended()
	require.NotEmpty(t, spans, "expected at least one span to be recorded")

	spanNames := make(map[string]bool)
	for _, s := range spans {
		spanNames[s.Name()] = true
	}

	assert.True(t, spanNames["redis.reserve_lua"], "expected span 'redis.reserve_lua', got: %v", spanNames)
	assert.True(t, spanNames["postgres.insert_reservation"], "expected span 'postgres.insert_reservation', got: %v", spanNames)
	assert.True(t, spanNames["reservation.Reserve"], "expected span 'reservation.Reserve', got: %v", spanNames)

	// Verify attributes on the reservation.Reserve span
	for _, s := range spans {
		if s.Name() != "reservation.Reserve" {
			continue
		}
		attrMap := make(map[string]string)
		for _, a := range s.Attributes() {
			attrMap[string(a.Key)] = a.Value.AsString()
		}
		assert.Equal(t, sale.ID.String(), attrMap["sale_id"], "sale_id attribute mismatch")
		assert.Equal(t, userID.String(), attrMap["user_id"], "user_id attribute mismatch")
	}

	// Verify attributes on redis.reserve_lua span
	for _, s := range spans {
		if s.Name() != "redis.reserve_lua" {
			continue
		}
		attrMap := make(map[string]string)
		for _, a := range s.Attributes() {
			attrMap[string(a.Key)] = a.Value.AsString()
		}
		assert.Equal(t, sale.ID.String(), attrMap["sale_id"])
		assert.NotEmpty(t, attrMap["redis.key"])
	}
}
