package worker

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

type DriftDetails struct {
	SaleID              string `json:"sale_id"`
	RedisAvailable      int    `json:"redis_available"`
	RedisReserved       int    `json:"redis_reserved"`
	ExpectedAvailable   int    `json:"expected_available"`
	ExpectedReserved    int    `json:"expected_reserved"`
	PostgresReserved    int    `json:"postgres_reserved"`
	PostgresConfirmed   int    `json:"postgres_confirmed"`
	TotalStock          int    `json:"total_stock"`
}

func StartReconciliationWorker(ctx context.Context, pg *pgxpool.Pool, rdb *redis.Client, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Reconciliation worker started (interval: %v)", interval)

	for {
		select {
		case <-ctx.Done():
			log.Println("Reconciliation worker stopped")
			return
		case <-ticker.C:
			if err := reconcileAllSales(ctx, pg, rdb); err != nil {
				log.Printf("Reconciliation error: %v", err)
			}
		}
	}
}

func reconcileAllSales(ctx context.Context, pg *pgxpool.Pool, rdb *redis.Client) error {
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

	for rows.Next() {
		var saleID string
		var totalStock int
		if err := rows.Scan(&saleID, &totalStock); err != nil {
			log.Printf("Failed to scan sale: %v", err)
			continue
		}

		corrected, err := reconcileSingleSale(ctx, pg, rdb, saleID, totalStock)
		if err != nil {
			log.Printf("Failed to reconcile sale %s: %v", saleID, err)
			continue
		}

		if corrected {
			corrections++
		}
	}

	if corrections > 0 {
		log.Printf("Reconciliation cycle: %d corrections made", corrections)
		metrics.ReconciliationCorrections.Add(float64(corrections))
	}

	return rows.Err()
}

func reconcileSingleSale(ctx context.Context, pg *pgxpool.Pool, rdb *redis.Client, saleID string, totalStock int) (bool, error) {
	var pgReserved int
	err := pg.QueryRow(ctx, `
		SELECT COUNT(*) 
		FROM reservations 
		WHERE sale_id = $1 AND status = 'reserved' AND expires_at > NOW()
	`, saleID).Scan(&pgReserved)
	if err != nil {
		return false, fmt.Errorf("failed to count reservations: %w", err)
	}

	var pgConfirmed int
	err = pg.QueryRow(ctx, `
		SELECT COUNT(*) 
		FROM orders 
		WHERE sale_id = $1 AND status IN ('pending', 'paid')
	`, saleID).Scan(&pgConfirmed)
	if err != nil {
		return false, fmt.Errorf("failed to count orders: %w", err)
	}

	expectedAvailable := totalStock - pgReserved - pgConfirmed

	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	reservedKey := fmt.Sprintf("sale:%s:reserved", saleID)

	redisAvailable, _ := rdb.Get(ctx, availableKey).Int()
	redisReserved, _ := rdb.Get(ctx, reservedKey).Int()

	if redisAvailable != expectedAvailable || redisReserved != pgReserved {
		drift := DriftDetails{
			SaleID:            saleID,
			RedisAvailable:    redisAvailable,
			RedisReserved:     redisReserved,
			ExpectedAvailable: expectedAvailable,
			ExpectedReserved:  pgReserved,
			PostgresReserved:  pgReserved,
			PostgresConfirmed: pgConfirmed,
			TotalStock:        totalStock,
		}

		if err := rdb.Set(ctx, availableKey, expectedAvailable, 0).Err(); err != nil {
			return false, fmt.Errorf("failed to set available: %w", err)
		}
		if err := rdb.Set(ctx, reservedKey, pgReserved, 0).Err(); err != nil {
			return false, fmt.Errorf("failed to set reserved: %w", err)
		}

		payload, _ := json.Marshal(drift)
		if err := audit.Log(ctx, pg, audit.AuditEntry{
			EntityType: "sale",
			EntityID:   saleID,
			EventType:  "reconciliation_correction",
			Payload:    payload,
		}); err != nil {
			log.Printf("Warning: failed to log audit entry: %v", err)
		}

		log.Printf("Drift corrected for sale %s: available %d->%d, reserved %d->%d",
			saleID, redisAvailable, expectedAvailable, redisReserved, pgReserved)

		return true, nil
	}

	return false, nil
}
