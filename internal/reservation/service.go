package reservation

import (
	"context"
	"fmt"
	"time"

	"github.com/flashsale/backend/internal/audit"
	"github.com/flashsale/backend/internal/events"
	"github.com/flashsale/backend/internal/metrics"
	"github.com/flashsale/backend/internal/models"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("flash-sale/reservation")

type Service struct {
	redis         *redisclient.Client
	db            *pgxpool.Pool
	reservationTTL int
	publisher     *events.Publisher
}

func NewService(redis *redisclient.Client, db *pgxpool.Pool, reservationTTL int, publisher *events.Publisher) *Service {
	return &Service{
		redis:         redis,
		db:            db,
		reservationTTL: reservationTTL,
		publisher:     publisher,
	}
}

func (s *Service) Reserve(ctx context.Context, userID, saleID, itemID uuid.UUID, idempotencyKey string) (*models.Reservation, error) {
	ctx, span := tracer.Start(ctx, "reservation.Reserve")
	defer span.End()
	span.SetAttributes(
		attribute.String("sale_id", saleID.String()),
		attribute.String("user_id", userID.String()),
	)

	timer := metrics.ReservationDuration.WithLabelValues(saleID.String())
	start := time.Now()
	defer func() { timer.Observe(time.Since(start).Seconds()) }()

	if idempotencyKey != "" {
		existing, err := s.checkIdempotency(ctx, saleID, idempotencyKey)
		if err == nil && existing != nil {
			metrics.ReservationTotal.WithLabelValues(saleID.String(), "idempotent").Inc()
			return existing, nil
		}
	}

	result, err := s.redis.Reserve(ctx, saleID.String())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		metrics.ReservationTotal.WithLabelValues(saleID.String(), "error").Inc()
		return nil, fmt.Errorf("redis reserve failed: %w", err)
	}

	if result == -1 {
		metrics.ReservationTotal.WithLabelValues(saleID.String(), "sold_out").Inc()
		return nil, fmt.Errorf("sold out")
	}

	reservation := &models.Reservation{
		ID:         uuid.New(),
		UserID:     userID,
		SaleID:     saleID,
		ItemID:     itemID,
		Status:     models.ReservationStatusReserved,
		ReservedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Duration(s.reservationTTL) * time.Second),
	}

	pgCtx, pgSpan := tracer.Start(ctx, "postgres.insert_reservation")
	pgSpan.SetAttributes(
		attribute.String("db.operation", "INSERT"),
		attribute.String("db.table", "reservations"),
	)
	query := `
		INSERT INTO reservations (id, user_id, sale_id, item_id, status, reserved_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err = s.db.Exec(pgCtx, query, reservation.ID, reservation.UserID, reservation.SaleID,
		reservation.ItemID, reservation.Status, reservation.ReservedAt, reservation.ExpiresAt)
	pgSpan.End()
	if err != nil {
		pgSpan.RecordError(err)
		pgSpan.SetStatus(codes.Error, err.Error())
		s.redis.Release(ctx, saleID.String())
		metrics.ReservationTotal.WithLabelValues(saleID.String(), "error").Inc()
		return nil, fmt.Errorf("failed to create reservation: %w", err)
	}

	rdb := s.redis.GetClient()
	ttlKey := fmt.Sprintf("reservation:%s:ttl", reservation.ID)
	if err = rdb.Set(ctx, ttlKey, "1", time.Duration(s.reservationTTL)*time.Second).Err(); err != nil {
		return nil, fmt.Errorf("failed to set ttl: %w", err)
	}

	if idempotencyKey != "" {
		s.setIdempotency(ctx, saleID, idempotencyKey, reservation.ID)
	}

	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	available, _ := rdb.Get(ctx, availableKey).Int()

	metrics.StockLevel.WithLabelValues(saleID.String()).Set(float64(available))
	metrics.ReservationTotal.WithLabelValues(saleID.String(), "success").Inc()

	if s.publisher != nil {
		s.publisher.StockUpdate(ctx, saleID.String(), available)
		if available == 0 {
			s.publisher.SaleEnded(ctx, saleID.String(), "sold_out")
		}
	}

	audit.Log(ctx, s.db, audit.AuditEntry{
		EntityType: "reservation",
		EntityID:   reservation.ID,
		EventType:  "reserved",
		Payload: map[string]interface{}{
			"user_id": userID.String(),
			"sale_id": saleID.String(),
			"item_id": itemID.String(),
		},
	})

	return reservation, nil
}

func (s *Service) Confirm(ctx context.Context, reservationID uuid.UUID, idempotencyKey string) (*models.Order, error) {
	ctx, span := tracer.Start(ctx, "reservation.Confirm")
	defer span.End()
	span.SetAttributes(attribute.String("reservation_id", reservationID.String()))

	// Check idempotency first
	if idempotencyKey != "" {
		existingOrder, err := s.getOrderByIdempotencyKey(ctx, idempotencyKey)
		if err == nil && existingOrder != nil {
			return existingOrder, nil
		}
	}

	var reservation models.Reservation
	query := `SELECT id, user_id, sale_id, item_id, status FROM reservations WHERE id = $1`
	err := s.db.QueryRow(ctx, query, reservationID).Scan(
		&reservation.ID, &reservation.UserID, &reservation.SaleID, &reservation.ItemID, &reservation.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("reservation not found: %w", err)
	}

	if reservation.Status != models.ReservationStatusReserved {
		return nil, fmt.Errorf("reservation not in reserved state")
	}

	result, err := s.redis.Confirm(ctx, reservation.SaleID.String())
	if err != nil {
		return nil, fmt.Errorf("redis confirm failed: %w", err)
	}

	if result == -1 {
		return nil, fmt.Errorf("invalid reservation state")
	}

	// Begin transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now()

	// Span: UPDATE reservation
	txCtx, txSpan := tracer.Start(ctx, "postgres.update_reservation_confirmed")
	txSpan.SetAttributes(
		attribute.String("db.operation", "UPDATE"),
		attribute.String("db.table", "reservations"),
	)
	updateQuery := `UPDATE reservations SET status = $1, confirmed_at = $2 WHERE id = $3`
	_, err = tx.Exec(txCtx, updateQuery, models.ReservationStatusConfirmed, now, reservationID)
	txSpan.End()
	if err != nil {
		txSpan.RecordError(err)
		txSpan.SetStatus(codes.Error, err.Error())
		s.redis.Release(ctx, reservation.SaleID.String())
		return nil, fmt.Errorf("failed to update reservation: %w", err)
	}

	// Create order
	order := &models.Order{
		ID:             uuid.New(),
		ReservationID:  reservationID,
		UserID:         reservation.UserID,
		SaleID:         reservation.SaleID,
		Amount:         0, // TODO: fetch from sale_items
		Status:         "pending",
		IdempotencyKey: idempotencyKey,
		CreatedAt:      now,
	}

	// Span: INSERT order
	ordCtx, ordSpan := tracer.Start(ctx, "postgres.insert_order")
	ordSpan.SetAttributes(
		attribute.String("db.operation", "INSERT"),
		attribute.String("db.table", "orders"),
		attribute.String("order_id", order.ID.String()),
	)
	orderQuery := `
		INSERT INTO orders (id, reservation_id, user_id, sale_id, amount, status, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err = tx.Exec(ordCtx, orderQuery, order.ID, order.ReservationID, order.UserID, order.SaleID, order.Amount, order.Status, order.IdempotencyKey, order.CreatedAt)
	ordSpan.End()
	if err != nil {
		ordSpan.RecordError(err)
		ordSpan.SetStatus(codes.Error, err.Error())
		s.redis.Release(ctx, reservation.SaleID.String())
		return nil, fmt.Errorf("failed to create order: %w", err)
	}

	// Audit log
	err = audit.LogWithConn(ctx, tx, audit.AuditEntry{
		EntityType: "reservation",
		EntityID:   reservationID,
		EventType:  "confirmed",
		Payload: map[string]interface{}{
			"order_id": order.ID.String(),
		},
	})
	if err != nil {
		s.redis.Release(ctx, reservation.SaleID.String())
		return nil, fmt.Errorf("failed to write audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		s.redis.Release(ctx, reservation.SaleID.String())
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return order, nil
}

func (s *Service) Release(ctx context.Context, reservationID uuid.UUID) error {
	var reservation models.Reservation
	query := `SELECT id, sale_id, status FROM reservations WHERE id = $1`
	err := s.db.QueryRow(ctx, query, reservationID).Scan(&reservation.ID, &reservation.SaleID, &reservation.Status)
	if err != nil {
		return fmt.Errorf("reservation not found: %w", err)
	}

	if reservation.Status != models.ReservationStatusReserved {
		return fmt.Errorf("reservation not in reserved state")
	}

	err = s.redis.Release(ctx, reservation.SaleID.String())
	if err != nil {
		return fmt.Errorf("redis release failed: %w", err)
	}

	updateQuery := `UPDATE reservations SET status = $1 WHERE id = $2`
	_, err = s.db.Exec(ctx, updateQuery, models.ReservationStatusReleased, reservationID)
	if err != nil {
		return fmt.Errorf("failed to update reservation: %w", err)
	}

	rdb := s.redis.GetClient()
	availableKey := fmt.Sprintf("sale:%s:available", reservation.SaleID)
	available, _ := rdb.Get(ctx, availableKey).Int()
	
	if s.publisher != nil {
		s.publisher.StockUpdate(ctx, reservation.SaleID.String(), available)
	}

	audit.Log(ctx, s.db, audit.AuditEntry{
		EntityType: "reservation",
		EntityID:   reservationID,
		EventType:  "released",
		Payload: map[string]interface{}{
			"sale_id": reservation.SaleID.String(),
		},
	})

	return nil
}

func (s *Service) getOrderByIdempotencyKey(ctx context.Context, key string) (*models.Order, error) {
	var order models.Order
	query := `SELECT id, reservation_id, user_id, sale_id, amount, status, idempotency_key, created_at FROM orders WHERE idempotency_key = $1`
	err := s.db.QueryRow(ctx, query, key).Scan(
		&order.ID, &order.ReservationID, &order.UserID, &order.SaleID, &order.Amount, &order.Status, &order.IdempotencyKey, &order.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *Service) checkIdempotency(ctx context.Context, saleID uuid.UUID, key string) (*models.Reservation, error) {
	rdb := s.redis.GetClient()
	idempotencyKey := fmt.Sprintf("sale:%s:idempotency:%s", saleID, key)
	
	reservationID, err := rdb.Get(ctx, idempotencyKey).Result()
	if err != nil {
		return nil, err
	}

	resID, err := uuid.Parse(reservationID)
	if err != nil {
		return nil, err
	}

	var reservation models.Reservation
	query := `SELECT id, user_id, sale_id, item_id, status, reserved_at, expires_at FROM reservations WHERE id = $1`
	err = s.db.QueryRow(ctx, query, resID).Scan(
		&reservation.ID, &reservation.UserID, &reservation.SaleID, &reservation.ItemID,
		&reservation.Status, &reservation.ReservedAt, &reservation.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}

	return &reservation, nil
}

func (s *Service) setIdempotency(ctx context.Context, saleID uuid.UUID, key string, reservationID uuid.UUID) error {
	rdb := s.redis.GetClient()
	idempotencyKey := fmt.Sprintf("sale:%s:idempotency:%s", saleID, key)
	return rdb.Set(ctx, idempotencyKey, reservationID.String(), 24*time.Hour).Err()
}
