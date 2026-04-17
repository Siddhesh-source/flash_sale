package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/flashsale/backend/internal/models"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	redis *redisclient.Client
	db    *pgxpool.Pool
}

func NewService(redis *redisclient.Client, db *pgxpool.Pool) *Service {
	return &Service{
		redis: redis,
		db:    db,
	}
}

func (s *Service) CreateSale(ctx context.Context, sale *models.Sale) error {
	sale.ID = uuid.New()
	sale.CreatedAt = time.Now()
	sale.Status = models.SaleStatusPending

	query := `
		INSERT INTO sales (id, name, total_stock, start_time, end_time, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.db.Exec(ctx, query, sale.ID, sale.Name, sale.TotalStock, sale.StartTime, sale.EndTime, sale.Status, sale.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create sale: %w", err)
	}

	rdb := s.redis.GetClient()
	availableKey := fmt.Sprintf("sale:%s:available", sale.ID)
	reservedKey := fmt.Sprintf("sale:%s:reserved", sale.ID)
	metaKey := fmt.Sprintf("sale:%s:meta", sale.ID)

	pipe := rdb.Pipeline()
	pipe.Set(ctx, availableKey, sale.TotalStock, 0)
	pipe.Set(ctx, reservedKey, 0, 0)
	pipe.HSet(ctx, metaKey, map[string]interface{}{
		"total":      sale.TotalStock,
		"start_time": sale.StartTime.Unix(),
		"end_time":   sale.EndTime.Unix(),
		"status":     string(sale.Status),
	})
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize redis data: %w", err)
	}

	return nil
}

func (s *Service) GetSale(ctx context.Context, saleID uuid.UUID) (*models.Sale, *models.SaleStats, error) {
	query := `SELECT id, name, total_stock, start_time, end_time, status, created_at FROM sales WHERE id = $1`
	
	var sale models.Sale
	err := s.db.QueryRow(ctx, query, saleID).Scan(
		&sale.ID, &sale.Name, &sale.TotalStock, &sale.StartTime, &sale.EndTime, &sale.Status, &sale.CreatedAt,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get sale: %w", err)
	}

	rdb := s.redis.GetClient()
	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	reservedKey := fmt.Sprintf("sale:%s:reserved", saleID)

	available, err := rdb.Get(ctx, availableKey).Int()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get available count: %w", err)
	}

	reserved, err := rdb.Get(ctx, reservedKey).Int()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get reserved count: %w", err)
	}

	stats := &models.SaleStats{
		Available: available,
		Reserved:  reserved,
		Total:     sale.TotalStock,
	}

	return &sale, stats, nil
}

func (s *Service) UpdateSaleStatus(ctx context.Context, saleID uuid.UUID, status models.SaleStatus) error {
	query := `UPDATE sales SET status = $1 WHERE id = $2`
	_, err := s.db.Exec(ctx, query, status, saleID)
	if err != nil {
		return fmt.Errorf("failed to update sale status: %w", err)
	}

	rdb := s.redis.GetClient()
	metaKey := fmt.Sprintf("sale:%s:meta", saleID)
	err = rdb.HSet(ctx, metaKey, "status", string(status)).Err()
	if err != nil {
		return fmt.Errorf("failed to update redis status: %w", err)
	}

	return nil
}

// SaleInput is used by CreateSaleAndReturn to pass sale parameters.
type SaleInput struct {
	Name       string
	TotalStock int
	StartTime  time.Time
	EndTime    time.Time
}

// CreateSaleAndReturn creates a sale and returns the populated model (with generated ID).
// This is a convenience method for tests and internal use.
func (s *Service) CreateSaleAndReturn(ctx context.Context, input *SaleInput) (*models.Sale, error) {
	sale := &models.Sale{
		Name:       input.Name,
		TotalStock: input.TotalStock,
		StartTime:  input.StartTime,
		EndTime:    input.EndTime,
	}
	if err := s.CreateSale(ctx, sale); err != nil {
		return nil, err
	}
	return sale, nil
}
