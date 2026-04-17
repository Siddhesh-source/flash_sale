package models

import (
	"time"
	"github.com/google/uuid"
)

type ReservationStatus string

const (
	ReservationStatusReserved  ReservationStatus = "reserved"
	ReservationStatusConfirmed ReservationStatus = "confirmed"
	ReservationStatusReleased  ReservationStatus = "released"
	ReservationStatusExpired   ReservationStatus = "expired"
)

type Reservation struct {
	ID          uuid.UUID         `json:"id"`
	UserID      uuid.UUID         `json:"user_id"`
	SaleID      uuid.UUID         `json:"sale_id"`
	ItemID      uuid.UUID         `json:"item_id"`
	Status      ReservationStatus `json:"status"`
	ReservedAt  time.Time         `json:"reserved_at"`
	ConfirmedAt *time.Time        `json:"confirmed_at,omitempty"`
	ExpiresAt   time.Time         `json:"expires_at"`
}

type Order struct {
	ID              uuid.UUID  `json:"id"`
	ReservationID   uuid.UUID  `json:"reservation_id"`
	UserID          uuid.UUID  `json:"user_id"`
	SaleID          uuid.UUID  `json:"sale_id"`
	Amount          float64    `json:"amount"`
	Status          string     `json:"status"`
	IdempotencyKey  string     `json:"idempotency_key"`
	CreatedAt       time.Time  `json:"created_at"`
}
