package inventory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/flashsale/backend/internal/config"
	"github.com/flashsale/backend/internal/models"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/flashsale/backend/internal/reservation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServices(t *testing.T) (*Service, *reservation.Service, *redisclient.Client, *pgxpool.Pool) {
	cfg, err := config.Load()
	require.NoError(t, err)

	redisClient, err := redisclient.NewClient(cfg.RedisURL)
	require.NoError(t, err)

	db, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	require.NoError(t, err)

	inventoryService := NewService(redisClient, db)
	reservationService := reservation.NewService(redisClient, db, cfg.ReservationTTLSeconds, nil)

	return inventoryService, reservationService, redisClient, db
}

func TestNoOversell(t *testing.T) {
	inventoryService, reservationService, redisClient, db := setupTestServices(t)
	defer redisClient.Close()
	defer db.Close()

	ctx := context.Background()

	sale := &models.Sale{
		Name:       "Test Flash Sale",
		TotalStock: 10,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(1 * time.Hour),
	}

	err := inventoryService.CreateSale(ctx, sale)
	require.NoError(t, err)

	userID := uuid.New()
	itemID := uuid.New()

	numGoroutines := 10000
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	successCount := int32(0)
	failCount := int32(0)
	var mu sync.Mutex
	successfulReservations := make([]uuid.UUID, 0)

	for i := 0; i < numGoroutines; i++ {
		go func(index int) {
			defer wg.Done()

			idempotencyKey := fmt.Sprintf("test-key-%d", index)
			_, err := reservationService.Reserve(ctx, userID, sale.ID, itemID, idempotencyKey)
			
			mu.Lock()
			if err == nil {
				successCount++
			} else {
				failCount++
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int32(10), successCount, "Expected exactly 10 successful reservations")
	assert.Equal(t, int32(9990), failCount, "Expected 9990 failed reservations")

	rdb := redisClient.GetClient()
	availableKey := fmt.Sprintf("sale:%s:available", sale.ID)
	available, err := rdb.Get(ctx, availableKey).Int()
	require.NoError(t, err)
	assert.Equal(t, 0, available, "Available stock should be 0")

	reservedKey := fmt.Sprintf("sale:%s:reserved", sale.ID)
	reserved, err := rdb.Get(ctx, reservedKey).Int()
	require.NoError(t, err)
	assert.Equal(t, 10, reserved, "Reserved count should be 10")

	query := `SELECT COUNT(*) FROM reservations WHERE sale_id = $1 AND status = $2`
	var dbReservedCount int
	err = db.QueryRow(ctx, query, sale.ID, models.ReservationStatusReserved).Scan(&dbReservedCount)
	require.NoError(t, err)
	assert.Equal(t, 10, dbReservedCount, "Database should have exactly 10 reserved records")

	t.Logf("Test completed: %d successful, %d failed out of %d attempts", successCount, failCount, numGoroutines)
}
