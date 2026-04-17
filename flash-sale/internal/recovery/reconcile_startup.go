package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/flashsale/backend/internal/audit"
	"github.com/flashsale/backend/internal/metrics"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type ReconciliationResult struct {
	SaleID              string
	TotalStock          int
	ReservedCount       int
	ConfirmedCount      int
	ExpectedAvailable   int
	RedisAvailableBefore int
	RedisReservedBefore  int
	Corrected           bool
}

func ReconcileOnStartup(ctx context.Context, pg *pgxpool.Pool, rdb *redis.Client) error {
	log.Println("Starting cold restart reconciliation...")
	startTime := time.Now()

	rows, err := pg.Query(ctx, `
		SELECT id, total_stock 
		FROM sales 
		WHERE status = 'active'
	`)
	if err != nil {
		return fmt.Errorf("failed to query active sales: %w", err)
	}
	defer rows.Close()

	corrections := 0
	salesProcessed := 0

	for rows.Next() {
		var saleID string
		var totalStock int
		if err := rows.Scan(&saleID, &totalStock); err != nil {
			return fmt.Errorf("failed to scan sale row: %w", err)
		}

		result, err := reconcileSale(ctx, pg, rdb, saleID, totalStock)
		if err != nil {
			log.Printf("Warning: failed to reconcile sale %s: %v", saleID, err)
			continue
		}

		salesProcessed++
		if result.Corrected {
			corrections++
			log.Printf("Corrected sale %s: available %d->%d, reserved %d->%d",
				saleID, result.RedisAvailableBefore, result.ExpectedAvailable,
				result.RedisReservedBefore, result.ReservedCount)

			payload, _ := json.Marshal(result)
			if err := audit.Log(ctx, pg, audit.AuditEntry{
				EntityType: "sale",
				EntityID:   saleID,
				EventType:  "startup_reconciliation",
				Payload:    payload,
			}); err != nil {
				log.Printf("Warning: failed to log audit entry: %v", err)
			}
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating sales: %w", err)
	}

	metrics.ReconciliationCorrections.Add(float64(corrections))
	elapsed := time.Since(startTime)
	log.Printf("Reconciliation complete: %d sales processed, %d corrections made in %v",
		salesProcessed, corrections, elapsed)

	return nil
}

func reconcileSale(ctx context.Context, pg *pgxpool.Pool, rdb *redis.Client, saleID string, totalStock int) (*ReconciliationResult, error) {
	var reservedCount int
	err := pg.QueryRow(ctx, `
		SELECT COUNT(*) 
		FROM reservations 
		WHERE sale_id = $1 AND status = 'reserved' AND expires_at > NOW()
	`, saleID).Scan(&reservedCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count reservations: %w", err)
	}

	var confirmedCount int
	err = pg.QueryRow(ctx, `
		SELECT COUNT(*) 
		FROM orders 
		WHERE sale_id = $1 AND status IN ('pending', 'paid')
	`, saleID).Scan(&confirmedCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count orders: %w", err)
	}

	expectedAvailable := totalStock - reservedCount - confirmedCount

	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	reservedKey := fmt.Sprintf("sale:%s:reserved", saleID)

	redisAvailable, _ := rdb.Get(ctx, availableKey).Int()
	redisReserved, _ := rdb.Get(ctx, reservedKey).Int()

	result := &ReconciliationResult{
		SaleID:               saleID,
		TotalStock:           totalStock,
		ReservedCount:        reservedCount,
		ConfirmedCount:       confirmedCount,
		ExpectedAvailable:    expectedAvailable,
		RedisAvailableBefore: redisAvailable,
		RedisReservedBefore:  redisReserved,
		Corrected:            false,
	}

	if redisAvailable != expectedAvailable || redisReserved != reservedCount {
		if err := rdb.Set(ctx, availableKey, expectedAvailable, 0).Err(); err != nil {
			return nil, fmt.Errorf("failed to set available: %w", err)
		}
		if err := rdb.Set(ctx, reservedKey, reservedCount, 0).Err(); err != nil {
			return nil, fmt.Errorf("failed to set reserved: %w", err)
		}
		result.Corrected = true
	}

	return result, nil
}
