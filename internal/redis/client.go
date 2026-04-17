package redis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("flash-sale/redis")

type Client struct {
	rdb                *redis.Client
	replica            *redis.Client // read-only replica; falls back to rdb if nil
	reserveSHA         string
	releaseSHA         string
	confirmSHA         string
	waitlistJoinSHA    string
	waitlistPromoteSHA string
}

func NewClient(redisURL string, replicaURL ...string) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis URL: %w", err)
	}

	rdb := redis.NewClient(opts)

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	client := &Client{rdb: rdb}

	if len(replicaURL) > 0 && replicaURL[0] != "" {
		ropts, err := redis.ParseURL(replicaURL[0])
		if err == nil {
			replica := redis.NewClient(ropts)
			if replica.Ping(ctx).Err() == nil {
				client.replica = replica
			}
		}
	}

	if err := client.loadScripts(ctx); err != nil {
		return nil, fmt.Errorf("failed to load lua scripts: %w", err)
	}

	return client, nil
}

// ReadClient returns the replica client if available, otherwise the primary.
// Use for non-critical reads such as waitlist position queries.
func (c *Client) ReadClient() *redis.Client {
	if c.replica != nil {
		return c.replica
	}
	return c.rdb
}

func (c *Client) loadScripts(ctx context.Context) error {
	scriptsDir := "internal/redis/scripts"

	reserveScript, err := os.ReadFile(filepath.Join(scriptsDir, "reserve.lua"))
	if err != nil {
		return fmt.Errorf("failed to read reserve.lua: %w", err)
	}
	releaseScript, err := os.ReadFile(filepath.Join(scriptsDir, "release.lua"))
	if err != nil {
		return fmt.Errorf("failed to read release.lua: %w", err)
	}
	confirmScript, err := os.ReadFile(filepath.Join(scriptsDir, "confirm.lua"))
	if err != nil {
		return fmt.Errorf("failed to read confirm.lua: %w", err)
	}
	waitlistJoinScript, err := os.ReadFile(filepath.Join(scriptsDir, "waitlist_join.lua"))
	if err != nil {
		return fmt.Errorf("failed to read waitlist_join.lua: %w", err)
	}
	waitlistPromoteScript, err := os.ReadFile(filepath.Join(scriptsDir, "waitlist_promote.lua"))
	if err != nil {
		return fmt.Errorf("failed to read waitlist_promote.lua: %w", err)
	}

	if c.reserveSHA, err = c.rdb.ScriptLoad(ctx, string(reserveScript)).Result(); err != nil {
		return fmt.Errorf("failed to load reserve script: %w", err)
	}
	if c.releaseSHA, err = c.rdb.ScriptLoad(ctx, string(releaseScript)).Result(); err != nil {
		return fmt.Errorf("failed to load release script: %w", err)
	}
	if c.confirmSHA, err = c.rdb.ScriptLoad(ctx, string(confirmScript)).Result(); err != nil {
		return fmt.Errorf("failed to load confirm script: %w", err)
	}
	if c.waitlistJoinSHA, err = c.rdb.ScriptLoad(ctx, string(waitlistJoinScript)).Result(); err != nil {
		return fmt.Errorf("failed to load waitlist_join script: %w", err)
	}
	if c.waitlistPromoteSHA, err = c.rdb.ScriptLoad(ctx, string(waitlistPromoteScript)).Result(); err != nil {
		return fmt.Errorf("failed to load waitlist_promote script: %w", err)
	}
	return nil
}

// Reserve atomically decrements available and increments reserved stock.
// Returns 1 on success, -1 if sold out.
func (c *Client) Reserve(ctx context.Context, saleID string) (int64, error) {
	ctx, span := tracer.Start(ctx, "redis.reserve_lua")
	defer span.End()

	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	reservedKey := fmt.Sprintf("sale:%s:reserved", saleID)
	span.SetAttributes(
		attribute.String("redis.key", availableKey),
		attribute.String("sale_id", saleID),
	)

	result, err := c.rdb.EvalSha(ctx, c.reserveSHA, []string{availableKey, reservedKey}).Result()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("reserve script failed: %w", err)
	}

	v := result.(int64)
	span.SetAttributes(attribute.Int64("redis.result", v))
	return v, nil
}

// Release atomically increments available and decrements reserved stock.
func (c *Client) Release(ctx context.Context, saleID string) error {
	ctx, span := tracer.Start(ctx, "redis.release_lua")
	defer span.End()

	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	reservedKey := fmt.Sprintf("sale:%s:reserved", saleID)
	span.SetAttributes(
		attribute.String("redis.key", availableKey),
		attribute.String("sale_id", saleID),
	)

	_, err := c.rdb.EvalSha(ctx, c.releaseSHA, []string{availableKey, reservedKey}).Result()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("release script failed: %w", err)
	}
	return nil
}

// Confirm atomically decrements reserved stock (item sold).
func (c *Client) Confirm(ctx context.Context, saleID string) (int64, error) {
	ctx, span := tracer.Start(ctx, "redis.confirm_lua")
	defer span.End()

	reservedKey := fmt.Sprintf("sale:%s:reserved", saleID)
	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	metaKey := fmt.Sprintf("sale:%s:meta", saleID)
	span.SetAttributes(
		attribute.String("redis.key", reservedKey),
		attribute.String("sale_id", saleID),
	)

	result, err := c.rdb.EvalSha(ctx, c.confirmSHA, []string{reservedKey, availableKey, metaKey}).Result()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("confirm script failed: %w", err)
	}

	v := result.(int64)
	span.SetAttributes(attribute.Int64("redis.result", v))
	return v, nil
}

// WaitlistJoin adds a user to the waitlist if stock is unavailable.
func (c *Client) WaitlistJoin(ctx context.Context, saleID, userID string, timestamp int64) (int64, error) {
	ctx, span := tracer.Start(ctx, "redis.waitlist_join_lua")
	defer span.End()

	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	waitlistKey := fmt.Sprintf("sale:%s:waitlist", saleID)
	span.SetAttributes(
		attribute.String("redis.key", waitlistKey),
		attribute.String("sale_id", saleID),
		attribute.String("user_id", userID),
	)

	result, err := c.rdb.EvalSha(ctx, c.waitlistJoinSHA, []string{availableKey, waitlistKey}, userID, timestamp).Result()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("waitlist_join script failed: %w", err)
	}

	v := result.(int64)
	span.SetAttributes(attribute.Int64("redis.result", v))
	return v, nil
}

// WaitlistPromote pops the next user from the waitlist and reserves stock for them.
func (c *Client) WaitlistPromote(ctx context.Context, saleID string) (string, error) {
	ctx, span := tracer.Start(ctx, "redis.waitlist_promote_lua")
	defer span.End()

	waitlistKey := fmt.Sprintf("sale:%s:waitlist", saleID)
	availableKey := fmt.Sprintf("sale:%s:available", saleID)
	reservedKey := fmt.Sprintf("sale:%s:reserved", saleID)
	span.SetAttributes(
		attribute.String("redis.key", waitlistKey),
		attribute.String("sale_id", saleID),
	)

	result, err := c.rdb.EvalSha(ctx, c.waitlistPromoteSHA, []string{waitlistKey, availableKey, reservedKey}).Result()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("waitlist_promote script failed: %w", err)
	}

	promoted := result.(string)
	span.SetAttributes(attribute.String("promoted_user_id", promoted))
	return promoted, nil
}

func (c *Client) GetClient() *redis.Client {
	return c.rdb
}

func (c *Client) Close() error {
	if c.replica != nil {
		c.replica.Close()
	}
	return c.rdb.Close()
}
