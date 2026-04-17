package waitlist

import (
	"context"
	"fmt"
	"time"

	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/google/uuid"
)

type Service struct {
	redis *redisclient.Client
}

func NewService(redis *redisclient.Client) *Service {
	return &Service{
		redis: redis,
	}
}

type JoinResponse struct {
	Position int       `json:"position"`
	JoinedAt time.Time `json:"joined_at"`
}

type PositionResponse struct {
	Position     int `json:"position"`
	TotalWaiting int `json:"total_waiting"`
}

func (s *Service) Join(ctx context.Context, saleID, userID uuid.UUID) (*JoinResponse, error) {
	timestamp := time.Now().Unix()
	
	rank, err := s.redis.WaitlistJoin(ctx, saleID.String(), userID.String(), timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to join waitlist: %w", err)
	}

	if rank == -1 {
		return nil, fmt.Errorf("stock available")
	}

	if rank == -2 {
		return nil, fmt.Errorf("already in waitlist")
	}

	return &JoinResponse{
		Position: int(rank) + 1,
		JoinedAt: time.Unix(timestamp, 0),
	}, nil
}

func (s *Service) GetPosition(ctx context.Context, saleID, userID uuid.UUID) (*PositionResponse, error) {
	rdb := s.redis.GetClient()
	waitlistKey := fmt.Sprintf("sale:%s:waitlist", saleID)

	rank, err := rdb.ZRank(ctx, waitlistKey, userID.String()).Result()
	if err != nil {
		return nil, fmt.Errorf("user not in waitlist")
	}

	total, err := rdb.ZCard(ctx, waitlistKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get waitlist size: %w", err)
	}

	return &PositionResponse{
		Position:     int(rank) + 1,
		TotalWaiting: int(total),
	}, nil
}

func (s *Service) Leave(ctx context.Context, saleID, userID uuid.UUID) error {
	rdb := s.redis.GetClient()
	waitlistKey := fmt.Sprintf("sale:%s:waitlist", saleID)

	removed, err := rdb.ZRem(ctx, waitlistKey, userID.String()).Result()
	if err != nil {
		return fmt.Errorf("failed to leave waitlist: %w", err)
	}

	if removed == 0 {
		return fmt.Errorf("user not in waitlist")
	}

	return nil
}
