package ratelimit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter struct {
	rdb            *redis.Client
	tokenBucketSHA string
	capacity       float64
	ratePerSec     float64
}

func NewLimiter(rdb *redis.Client, capacity, ratePerSec float64) (*Limiter, error) {
	limiter := &Limiter{
		rdb:        rdb,
		capacity:   capacity,
		ratePerSec: ratePerSec,
	}

	if err := limiter.loadScript(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to load token bucket script: %w", err)
	}

	return limiter, nil
}

func (l *Limiter) loadScript(ctx context.Context) error {
	scriptPath := filepath.Join("internal", "redis", "scripts", "token_bucket.lua")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read token_bucket.lua: %w", err)
	}

	sha, err := l.rdb.ScriptLoad(ctx, string(script)).Result()
	if err != nil {
		return fmt.Errorf("failed to load script: %w", err)
	}

	l.tokenBucketSHA = sha
	return nil
}

func (l *Limiter) Allow(ctx context.Context, userID string) (bool, int, error) {
	key := fmt.Sprintf("ratelimit:%s", userID)
	now := time.Now().UnixMilli()

	result, err := l.rdb.EvalSha(ctx, l.tokenBucketSHA, []string{key}, l.capacity, l.ratePerSec, now).Result()
	if err != nil {
		return false, 0, fmt.Errorf("token bucket script failed: %w", err)
	}

	remaining := int(result.(int64))
	if remaining == -1 {
		return false, 0, nil
	}

	return true, remaining, nil
}
