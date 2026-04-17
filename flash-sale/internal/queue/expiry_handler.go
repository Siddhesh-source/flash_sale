package queue

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/flashsale/backend/internal/audit"
	"github.com/flashsale/backend/internal/events"
	"github.com/flashsale/backend/internal/models"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ExpiryHandler struct {
	redis         *redisclient.Client
	db            *pgxpool.Pool
	reservationTTL int
	publisher     *events.Publisher
}

func NewExpiryHandler(redis *redisclient.Client, db *pgxpool.Pool, reservationTTL int, publisher *events.Publisher) *ExpiryHandler {
	return &ExpiryHandler{
		redis:         redis,
		db:            db,
		reservationTTL: reservationTTL,
		publisher:     publisher,
	}
}

func (h *ExpiryHandler) Start(ctx context.Context) error {
	rdb := h.redis.GetClient()

	if err := rdb.ConfigSet(ctx, "notify-keyspace-events", "Ex").Err(); err != nil {
		return fmt.Errorf("failed to enable keyspace notifications: %w", err)
	}

	pubsub := rdb.PSubscribe(ctx, "__keyevent@0__:expired")

	log.Println("Expiry handler started, listening for TTL expirations...")

	// Goroutine: listen for TTL expiry events
	go func() {
		defer pubsub.Close()
		for {
			msg, err := pubsub.ReceiveMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("Error receiving message: %v", err)
				continue
			}

			if strings.HasPrefix(msg.Payload, "reservation:") && strings.HasSuffix(msg.Payload, ":ttl") {
				go h.handleExpiredReservation(ctx, msg.Payload)
			}
		}
	}()

	// Goroutine: 60-second expiry warning ticker — poll every 30s for reservations
	// expiring within the next 60–90 second window and notify their users.
	go h.runExpiryWarningTicker(ctx)

	return nil
}

// runExpiryWarningTicker polls Postgres every 30 seconds and fires
// reservation_expiring to any user whose reservation TTL is 60 ± 30 seconds away.
func (h *ExpiryHandler) runExpiryWarningTicker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.notifyExpiringReservations(ctx)
		}
	}
}

func (h *ExpiryHandler) notifyExpiringReservations(ctx context.Context) {
	if h.publisher == nil {
		return
	}
	// Find reservations expiring in the next 60–90 second window
	query := `
		SELECT id, user_id, expires_at
		FROM reservations
		WHERE status = 'reserved'
		  AND expires_at BETWEEN NOW() + INTERVAL '55 seconds' AND NOW() + INTERVAL '90 seconds'
	`
	rows, err := h.db.Query(ctx, query)
	if err != nil {
		log.Printf("Failed to query expiring reservations: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var resID, userID uuid.UUID
		var expiresAt time.Time
		if err := rows.Scan(&resID, &userID, &expiresAt); err != nil {
			continue
		}
		secondsLeft := int(time.Until(expiresAt).Seconds())
		if secondsLeft < 0 {
			secondsLeft = 0
		}
		if err := h.publisher.ReservationExpiring(ctx, userID.String(), secondsLeft); err != nil {
			log.Printf("Failed to send reservation_expiring to user %s: %v", userID, err)
		}
	}
}

func (h *ExpiryHandler) handleExpiredReservation(ctx context.Context, key string) {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		log.Printf("Invalid reservation TTL key format: %s", key)
		return
	}

	reservationID, err := uuid.Parse(parts[1])
	if err != nil {
		log.Printf("Invalid reservation ID in key %s: %v", key, err)
		return
	}

	log.Printf("Processing expired reservation: %s", reservationID)

	var reservation struct {
		ID     uuid.UUID
		SaleID uuid.UUID
		Status models.ReservationStatus
	}

	query := `SELECT id, sale_id, status FROM reservations WHERE id = $1`
	err = h.db.QueryRow(ctx, query, reservationID).Scan(&reservation.ID, &reservation.SaleID, &reservation.Status)
	if err != nil {
		log.Printf("Failed to load reservation %s: %v", reservationID, err)
		return
	}

	if reservation.Status != models.ReservationStatusReserved {
		log.Printf("Reservation %s is not in reserved state, skipping", reservationID)
		return
	}

	if err := h.redis.Release(ctx, reservation.SaleID.String()); err != nil {
		log.Printf("Failed to release reservation %s: %v", reservationID, err)
		return
	}

	updateQuery := `UPDATE reservations SET status = $1 WHERE id = $2`
	if _, err := h.db.Exec(ctx, updateQuery, models.ReservationStatusExpired, reservationID); err != nil {
		log.Printf("Failed to update reservation %s to expired: %v", reservationID, err)
		return
	}

	rdb := h.redis.GetClient()
	availableKey := fmt.Sprintf("sale:%s:available", reservation.SaleID)
	available, _ := rdb.Get(ctx, availableKey).Int()
	
	if h.publisher != nil {
		h.publisher.StockUpdate(ctx, reservation.SaleID.String(), available)
	}

	audit.Log(ctx, h.db, audit.AuditEntry{
		EntityType: "reservation",
		EntityID:   reservationID,
		EventType:  "expired",
		Payload: map[string]interface{}{
			"sale_id": reservation.SaleID.String(),
		},
	})

	log.Printf("Reservation %s expired and released", reservationID)

	h.promoteNextUser(ctx, reservation.SaleID)
}

func (h *ExpiryHandler) promoteNextUser(ctx context.Context, saleID uuid.UUID) {
	userIDStr, err := h.redis.WaitlistPromote(ctx, saleID.String())
	if err != nil {
		log.Printf("Failed to promote from waitlist for sale %s: %v", saleID, err)
		return
	}

	if userIDStr == "-1" {
		log.Printf("No users to promote for sale %s", saleID)
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Printf("Invalid user ID from waitlist: %s", userIDStr)
		return
	}

	newReservation := &models.Reservation{
		ID:         uuid.New(),
		UserID:     userID,
		SaleID:     saleID,
		ItemID:     uuid.New(),
		Status:     models.ReservationStatusReserved,
		ReservedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Duration(h.reservationTTL) * time.Second),
	}

	query := `
		INSERT INTO reservations (id, user_id, sale_id, item_id, status, reserved_at, expires_at, promoted_from_waitlist)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true)
	`
	_, err = h.db.Exec(ctx, query, newReservation.ID, newReservation.UserID, newReservation.SaleID,
		newReservation.ItemID, newReservation.Status, newReservation.ReservedAt, newReservation.ExpiresAt)
	if err != nil {
		log.Printf("Failed to create promoted reservation: %v", err)
		h.redis.Release(ctx, saleID.String())
		return
	}

	rdb := h.redis.GetClient()
	ttlKey := fmt.Sprintf("reservation:%s:ttl", newReservation.ID)
	if err := rdb.Set(ctx, ttlKey, "1", time.Duration(h.reservationTTL)*time.Second).Err(); err != nil {
		log.Printf("Failed to set TTL for promoted reservation: %v", err)
	}

	if h.publisher != nil {
		h.publisher.WaitlistPromoted(ctx, userID.String(), newReservation.ID.String())
		
		availableKey := fmt.Sprintf("sale:%s:available", saleID)
		available, _ := rdb.Get(ctx, availableKey).Int()
		h.publisher.StockUpdate(ctx, saleID.String(), available)
	}

	audit.Log(ctx, h.db, audit.AuditEntry{
		EntityType: "reservation",
		EntityID:   newReservation.ID,
		EventType:  "promoted",
		Payload: map[string]interface{}{
			"user_id": userID.String(),
			"sale_id": saleID.String(),
		},
	})

	log.Printf("NOTIFY user %s promoted to reservation %s", userID, newReservation.ID)
}
