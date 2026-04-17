package models

import (
	"time"
	"github.com/google/uuid"
)

type SaleStatus string

const (
	SaleStatusPending SaleStatus = "pending"
	SaleStatusActive  SaleStatus = "active"
	SaleStatusPaused  SaleStatus = "paused"
	SaleStatusEnded   SaleStatus = "ended"
)

type Sale struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	TotalStock int        `json:"total_stock"`
	StartTime  time.Time  `json:"start_time"`
	EndTime    time.Time  `json:"end_time"`
	Status     SaleStatus `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
}

type SaleItem struct {
	ID        uuid.UUID `json:"id"`
	SaleID    uuid.UUID `json:"sale_id"`
	SKU       string    `json:"sku"`
	Price     float64   `json:"price"`
	CreatedAt time.Time `json:"created_at"`
}

type SaleStats struct {
	Available int `json:"available"`
	Reserved  int `json:"reserved"`
	Total     int `json:"total"`
}
