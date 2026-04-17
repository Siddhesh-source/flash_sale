package waitlist

import (
	"context"
	"testing"
	"time"

	"github.com/flashsale/backend/internal/config"
	"github.com/flashsale/backend/internal/inventory"
	"github.com/flashsale/backend/internal/models"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServices(t *testing.T) (*inventory.Service, *Service, *redisclient.Client, *pgxpool.Pool) {
	cfg, err := config.Load()
	require.NoError(t, err)

	redisClient, err := redisclient.NewClient(cfg.RedisURL)
	require.NoError(t, err)

	db, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	require.NoError(t, err)

	inventoryService := inventory.NewService(redisClient, db)
	waitlistService := NewService(redisClient)

	return inventoryService, waitlistService, redisClient, db
}

func TestJoinWaitlistWhenStockAvailable(t *testing.T) {
	inventoryService, waitlistService, redisClient, db := setupTestServices(t)
	defer redisClient.Close()
	defer db.Close()

	ctx := context.Background()

	sale := &models.Sale{
		Name:       "Test Stock Available",
		TotalStock: 5,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(1 * time.Hour),
	}

	err := inventoryService.CreateSale(ctx, sale)
	require.NoError(t, err)

	userID := uuid.New()
	_, err = waitlistService.Join(ctx, sale.ID, userID)
	assert.Error(t, err)
	assert.Equal(t, "stock available", err.Error())
}

func TestJoinWaitlistTwice(t *testing.T) {
	inventoryService, waitlistService, redisClient, db := setupTestServices(t)
	defer redisClient.Close()
	defer db.Close()

	ctx := context.Background()

	sale := &models.Sale{
		Name:       "Test Duplicate Join",
		TotalStock: 0,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(1 * time.Hour),
	}

	err := inventoryService.CreateSale(ctx, sale)
	require.NoError(t, err)

	userID := uuid.New()
	
	join1, err := waitlistService.Join(ctx, sale.ID, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, join1.Position)

	_, err = waitlistService.Join(ctx, sale.ID, userID)
	assert.Error(t, err)
	assert.Equal(t, "already in waitlist", err.Error())
}
