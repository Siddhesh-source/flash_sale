package reservation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/flashsale/backend/internal/config"
	"github.com/flashsale/backend/internal/events"
	"github.com/flashsale/backend/internal/inventory"
	"github.com/flashsale/backend/internal/models"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupConfirmTestServices(t *testing.T) (*inventory.Service, *Service, *redisclient.Client, *pgxpool.Pool) {
	cfg, err := config.Load()
	require.NoError(t, err)

	redisClient, err := redisclient.NewClient(cfg.RedisURL)
	require.NoError(t, err)

	db, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	require.NoError(t, err)

	publisher := events.NewPublisher(redisClient.GetClient())
	inventoryService := inventory.NewService(redisClient, db)
	reservationService := NewService(redisClient, db, cfg.ReservationTTLSeconds, publisher)

	return inventoryService, reservationService, redisClient, db
}

func TestConfirmWritesOrder(t *testing.T) {
	inventoryService, reservationService, redisClient, db := setupConfirmTestServices(t)
	defer redisClient.Close()
	defer db.Close()

	ctx := context.Background()

	sale := &models.Sale{
		Name:       "Test Confirm Order",
		TotalStock: 10,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(1 * time.Hour),
	}

	err := inventoryService.CreateSale(ctx, sale)
	require.NoError(t, err)

	userID := uuid.New()
	itemID := uuid.New()

	reservation, err := reservationService.Reserve(ctx, userID, sale.ID, itemID, "")
	require.NoError(t, err)
	require.NotNil(t, reservation)

	order, err := reservationService.Confirm(ctx, reservation.ID, "test-idempotency-key")
	require.NoError(t, err)
	require.NotNil(t, order)

	var orderCount int
	err = db.QueryRow(ctx, `SELECT COUNT(*) FROM orders WHERE reservation_id = $1`, reservation.ID).Scan(&orderCount)
	require.NoError(t, err)
	assert.Equal(t, 1, orderCount, "Should have exactly 1 order")

	var resStatus models.ReservationStatus
	err = db.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, reservation.ID).Scan(&resStatus)
	require.NoError(t, err)
	assert.Equal(t, models.ReservationStatusConfirmed, resStatus)

	var auditCount int
	err = db.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity_id = $1 AND entity_type = 'reservation'`, reservation.ID).Scan(&auditCount)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, auditCount, 2, "Should have at least 2 audit entries (reserved + confirmed)")

	var reservedCount, confirmedCount int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity_id = $1 AND event_type = 'reserved'`, reservation.ID).Scan(&reservedCount)
	db.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity_id = $1 AND event_type = 'confirmed'`, reservation.ID).Scan(&confirmedCount)
	assert.Equal(t, 1, reservedCount)
	assert.Equal(t, 1, confirmedCount)
}

func TestConfirmIdempotency(t *testing.T) {
	inventoryService, reservationService, redisClient, db := setupConfirmTestServices(t)
	defer redisClient.Close()
	defer db.Close()

	ctx := context.Background()

	sale := &models.Sale{
		Name:       "Test Idempotency",
		TotalStock: 10,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(1 * time.Hour),
	}

	err := inventoryService.CreateSale(ctx, sale)
	require.NoError(t, err)

	userID := uuid.New()
	itemID := uuid.New()

	reservation, err := reservationService.Reserve(ctx, userID, sale.ID, itemID, "")
	require.NoError(t, err)

	idempotencyKey := "test-confirm-idempotency-" + uuid.New().String()

	order1, err := reservationService.Confirm(ctx, reservation.ID, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, order1)

	order2, err := reservationService.Confirm(ctx, reservation.ID, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, order2)

	assert.Equal(t, order1.ID, order2.ID, "Both responses should return the same order ID")
	assert.Equal(t, order1.Status, order2.Status)

	var orderCount int
	err = db.QueryRow(ctx, `SELECT COUNT(*) FROM orders WHERE reservation_id = $1`, reservation.ID).Scan(&orderCount)
	require.NoError(t, err)
	assert.Equal(t, 1, orderCount, "Should have exactly 1 order despite 2 confirm calls")

	rdb := redisClient.GetClient()
	reservedKey := fmt.Sprintf("sale:%s:reserved", sale.ID)
	reserved, err := rdb.Get(ctx, reservedKey).Int()
	require.NoError(t, err)
	assert.Equal(t, 0, reserved, "Reserved count should be 0 (decremented only once)")
}

func TestConfirmRollbackOnFailure(t *testing.T) {
	t.Skip("Skipping rollback test - requires mocking transaction failure")
}
