package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type EventLogger struct {
	rdb *redis.Client
}

func NewEventLogger(rdb *redis.Client) *EventLogger {
	return &EventLogger{rdb: rdb}
}

type StreamEvent struct {
	Type          string `json:"type"`
	UserID        string `json:"user_id,omitempty"`
	ReservationID string `json:"reservation_id,omitempty"`
	SaleID        string `json:"sale_id,omitempty"`
	Timestamp     int64  `json:"timestamp"`
}

func (e *EventLogger) LogEvent(ctx context.Context, saleID uuid.UUID, event StreamEvent) error {
	streamKey := fmt.Sprintf("sale:%s:events", saleID)
	
	values := map[string]interface{}{
		"type":      event.Type,
		"timestamp": event.Timestamp,
	}
	
	if event.UserID != "" {
		values["user_id"] = event.UserID
	}
	if event.ReservationID != "" {
		values["reservation_id"] = event.ReservationID
	}
	if event.SaleID != "" {
		values["sale_id"] = event.SaleID
	}

	_, err := e.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: values,
	}).Result()

	if err != nil {
		return fmt.Errorf("failed to add event to stream: %w", err)
	}

	return nil
}

func (e *EventLogger) StartConsumer(ctx context.Context, groupName, consumerName string, handler func(StreamEvent) error) error {
	streams := []string{"sale:*:events"}
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			result, err := e.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group:    groupName,
				Consumer: consumerName,
				Streams:  append(streams, ">"),
				Count:    10,
				Block:    0,
			}).Result()

			if err != nil {
				if err == redis.Nil {
					continue
				}
				log.Printf("Stream read error: %v", err)
				continue
			}

			for _, stream := range result {
				for _, message := range stream.Messages {
					var event StreamEvent
					data, _ := json.Marshal(message.Values)
					if err := json.Unmarshal(data, &event); err != nil {
						log.Printf("Failed to unmarshal event: %v", err)
						continue
					}

					if err := handler(event); err != nil {
						log.Printf("Handler error: %v", err)
						continue
					}

					e.rdb.XAck(ctx, stream.Stream, groupName, message.ID)
				}
			}
		}
	}
}
