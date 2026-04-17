package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type Publisher struct {
	rdb *redis.Client
}

func NewPublisher(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

type Event struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

type StockUpdateData struct {
	Remaining int `json:"remaining"`
}

type SaleEndedData struct {
	Reason string `json:"reason"`
}

type WaitlistPromotedData struct {
	ReservationID string `json:"your_reservation_id"`
}

type ReservationExpiringData struct {
	ExpiresInSeconds int `json:"expires_in_seconds"`
}

type QueuePositionData struct {
	Position int `json:"position"`
}

type SaleStartedData struct {
	SaleID     string `json:"sale_id"`
	TotalStock int    `json:"total_stock"`
}

func (p *Publisher) Publish(ctx context.Context, saleID string, event Event) error {
	channel := fmt.Sprintf("sale:%s:pubsub", saleID)
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	return p.rdb.Publish(ctx, channel, payload).Err()
}

func (p *Publisher) PublishToUser(ctx context.Context, userID string, event Event) error {
	channel := fmt.Sprintf("user:%s:events", userID)
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	return p.rdb.Publish(ctx, channel, payload).Err()
}

func (p *Publisher) StockUpdate(ctx context.Context, saleID string, remaining int) error {
	return p.Publish(ctx, saleID, Event{
		Event: "stock_update",
		Data:  StockUpdateData{Remaining: remaining},
	})
}

func (p *Publisher) SaleEnded(ctx context.Context, saleID string, reason string) error {
	return p.Publish(ctx, saleID, Event{
		Event: "sale_ended",
		Data:  SaleEndedData{Reason: reason},
	})
}

func (p *Publisher) WaitlistPromoted(ctx context.Context, userID, reservationID string) error {
	return p.PublishToUser(ctx, userID, Event{
		Event: "waitlist_promoted",
		Data:  WaitlistPromotedData{ReservationID: reservationID},
	})
}

func (p *Publisher) ReservationExpiring(ctx context.Context, userID string, expiresInSeconds int) error {
	return p.PublishToUser(ctx, userID, Event{
		Event: "reservation_expiring",
		Data:  ReservationExpiringData{ExpiresInSeconds: expiresInSeconds},
	})
}

func (p *Publisher) QueuePosition(ctx context.Context, saleID string, position int) error {
	return p.Publish(ctx, saleID, Event{
		Event: "queue_position",
		Data:  QueuePositionData{Position: position},
	})
}

func (p *Publisher) SaleStarted(ctx context.Context, saleID string, totalStock int) error {
	return p.Publish(ctx, saleID, Event{
		Event: "sale_started",
		Data:  SaleStartedData{SaleID: saleID, TotalStock: totalStock},
	})
}
