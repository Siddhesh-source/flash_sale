package queue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/flashsale/backend/internal/config"
	"github.com/flashsale/backend/internal/inventory"
	"github.com/flashsale/backend/internal/models"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/flashsale/backend/internal/reservation"
	"github.com/flashsale/backend/internal/waitlist"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServices(t *testing.T) (*inventory.Service, *reservation.Service, *waitlist.Service, *ExpiryHandler, *redisclient.Client, *pgxpool.Pool) {
	cfg, err := config.Load()
	require.NoError(t, err)

	redisClient, err := redisclient.NewClient(cfg.RedisURL)
	require.NoError(t, err)

	db, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	require.NoError(t, err)

	inventoryService := inventory.NewService(redisClient, db)
	reservationService := reservation.NewService(redisClient, db, cfg.ReservationTTLSeconds, nil)
	waitlistService := waitlist.NewService(redisClient)
	expiryHandler := NewExpiryHandler(redisClient, db, cfg.ReservationTTLSeconds, nil)

	return inventoryService, reservationService, waitlistService, expiryHandler, redisClient, db
}

func TestWaitlistPromotion(t *testing.T) {
	inventoryService, reservationService, waitlistService, expiryHandler, redisClient, db := setupTestServices(t)
	defer redisClient.Close()
	defer db.Close()

	ctx := context.Background()

	sale := &models.Sale{
		Name:       "Test Waitlist Sale",
		TotalStock: 1,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(1 * time.Hour),
	}

	err := inventoryService.CreateSale(ctx, sale)
	require.NoError(t, err)

	userA := uuid.New()
	userB := uuid.New()
	userC := uuid.New()
	userD := uuid.New()
	itemID := uuid.New()

	reservationA, err := reservationService.Reserve(ctx, userA, sale.ID, itemID, "")
	require.NoError(t, err)
	require.NotNil(t, reservationA)

	joinB, err := waitlistService.Join(ctx, sale.ID, userB)
	require.NoError(t, err)
	assert.Equal(t, 1, joinB.Position)

	joinC, err := waitlistService.Join(ctx, sale.ID, userC)
	require.NoError(t, err)
	assert.Equal(t, 2, joinC.Position)

	joinD, err := waitlistService.Join(ctx, sale.ID, userD)
	require.NoError(t, err)
	assert.Equal(t, 3, joinD.Position)

	if err := expiryHandler.Start(ctx); err != nil {
		t.Fatalf("Failed to start expiry handler: %v", err)
	}

	rdb := redisClient.GetClient()
	ttlKey := fmt.Sprintf("reservation:%s:ttl", reservationA.ID)
	err = rdb.Del(ctx, ttlKey).Err()
	require.NoError(t, err)

	time.Sleep(3 * time.Second)

	var expiredStatus models.ReservationStatus
	query := `SELECT status FROM reservations WHERE id = $1`
	err = db.QueryRow(ctx, query, reservationA.ID).Scan(&expiredStatus)
	require.NoError(t, err)
	assert.Equal(t, models.ReservationStatusExpired, expiredStatus)

	var promotedReservation struct {
		UserID               uuid.UUID
		PromotedFromWaitlist bool
	}
	promotedQuery := `SELECT user_id, promoted_from_waitlist FROM reservations WHERE sale_id = $1 AND status = $2 AND user_id = $3`
	err = db.QueryRow(ctx, promotedQuery, sale.ID, models.ReservationStatusReserved, userB).Scan(
		&promotedReservation.UserID, &promotedReservation.PromotedFromWaitlist,
	)
	require.NoError(t, err)
	assert.Equal(t, userB, promotedReservation.UserID)
	assert.True(t, promotedReservation.PromotedFromWaitlist)

	posC, err := waitlistService.GetPosition(ctx, sale.ID, userC)
	require.NoError(t, err)
	assert.Equal(t, 1, posC.Position)

	posD, err := waitlistService.GetPosition(ctx, sale.ID, userD)
	require.NoError(t, err)
	assert.Equal(t, 2, posD.Position)

	availableKey := fmt.Sprintf("sale:%s:available", sale.ID)
	available, err := rdb.Get(ctx, availableKey).Int()
	require.NoError(t, err)
	assert.Equal(t, 0, available)

	reservedKey := fmt.Sprintf("sale:%s:reserved", sale.ID)
	reserved, err := rdb.Get(ctx, reservedKey).Int()
	require.NoError(t, err)
	assert.Equal(t, 1, reserved)

	t.Logf("Waitlist promotion test passed: User B promoted after User A expired")
}

func TestWaitlistPosition(t *testing.T) {
	inventoryService, _, waitlistService, _, redisClient, db := setupTestServices(t)
	defer redisClient.Close()
	defer db.Close()

	ctx := context.Background()

	sale := &models.Sale{
		Name:       "Test Position Sale",
		TotalStock: 0,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(1 * time.Hour),
	}

	err := inventoryService.CreateSale(ctx, sale)
	require.NoError(t, err)

	users := make([]uuid.UUID, 100)
	for i := 0; i < 100; i++ {
		users[i] = uuid.New()
		join, err := waitlistService.Join(ctx, sale.ID, users[i])
		require.NoError(t, err)
		assert.Equal(t, i+1, join.Position, "User %d should be at position %d", i, i+1)
	}

	pos50, err := waitlistService.GetPosition(ctx, sale.ID, users[49])
	require.NoError(t, err)
	assert.Equal(t, 50, pos50.Position)
	assert.Equal(t, 100, pos50.TotalWaiting)

	pos1, err := waitlistService.GetPosition(ctx, sale.ID, users[0])
	require.NoError(t, err)
	assert.Equal(t, 1, pos1.Position)

	pos100, err := waitlistService.GetPosition(ctx, sale.ID, users[99])
	require.NoError(t, err)
	assert.Equal(t, 100, pos100.Position)

	t.Logf("Waitlist position test passed: All 100 users in correct order")
}
