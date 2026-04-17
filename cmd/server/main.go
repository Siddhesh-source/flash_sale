package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flashsale/backend/internal/api"
	"github.com/flashsale/backend/internal/config"
	"github.com/flashsale/backend/internal/events"
	"github.com/flashsale/backend/internal/inventory"
	"github.com/flashsale/backend/internal/queue"
	"github.com/flashsale/backend/internal/ratelimit"
	"github.com/flashsale/backend/internal/recovery"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/flashsale/backend/internal/reservation"
	"github.com/flashsale/backend/internal/telemetry"
	"github.com/flashsale/backend/internal/waitlist"
	"github.com/flashsale/backend/internal/worker"
	ws "github.com/flashsale/backend/internal/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize OpenTelemetry tracing
	otlpEndpoint := getEnvOrDefault("OTLP_ENDPOINT", "localhost:4317")
	tp, err := telemetry.InitTracer("flash-sale", otlpEndpoint, "1.0.0", getEnvOrDefault("APP_ENV", "development"))
	if err != nil {
		log.Printf("Warning: failed to initialize tracer: %v — continuing without tracing", err)
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			tp.Shutdown(ctx)
		}()
	}

	redisClient, err := redisclient.NewClient(cfg.RedisURL, cfg.RedisReplicaURL)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer redisClient.Close()

	db, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	log.Println("Running startup reconciliation...")
	if err := recovery.ReconcileOnStartup(context.Background(), db, redisClient.GetClient()); err != nil {
		log.Fatalf("Startup reconciliation failed: %v", err)
	}
	api.SetReconciliationComplete()

	rateLimiter, err := ratelimit.NewLimiter(redisClient.GetClient(), cfg.RateLimitCapacity, cfg.RateLimitRatePerSec)
	if err != nil {
		log.Fatalf("Failed to create rate limiter: %v", err)
	}

	go worker.StartReconciliationWorker(context.Background(), db, redisClient.GetClient(), time.Duration(cfg.ReconciliationIntervalSeconds)*time.Second)

	publisher := events.NewPublisher(redisClient.GetClient())

	inventoryService := inventory.NewService(redisClient, db)
	reservationService := reservation.NewService(redisClient, db, cfg.ReservationTTLSeconds, publisher)
	waitlistService := waitlist.NewService(redisClient)

	hub := ws.NewHub()
	go hub.Run()

	expiryHandler := queue.NewExpiryHandler(redisClient, db, cfg.ReservationTTLSeconds, publisher)
	if err := expiryHandler.Start(context.Background()); err != nil {
		log.Fatalf("Failed to start expiry handler: %v", err)
	}

	// Redis Pub/Sub subscriber: fan out sale events to WebSocket clients
	go func() {
		rdb := redisClient.GetClient()
		salePatternPubsub := rdb.PSubscribe(context.Background(), "sale:*:pubsub")
		defer salePatternPubsub.Close()
		ch := salePatternPubsub.Channel()
		for msg := range ch {
			// channel format: sale:{id}:pubsub — extract sale ID
			parts := splitChannel(msg.Channel)
			if len(parts) == 3 {
				hub.Publish(parts[1], []byte(msg.Payload))
			}
		}
	}()

	// Redis Pub/Sub subscriber: fan out user-targeted events
	go func() {
		rdb := redisClient.GetClient()
		userPatternPubsub := rdb.PSubscribe(context.Background(), "user:*:events")
		defer userPatternPubsub.Close()
		ch := userPatternPubsub.Channel()
		for msg := range ch {
			// channel format: user:{id}:events — extract user ID
			parts := splitChannel(msg.Channel)
			if len(parts) == 3 {
				hub.PublishToUser(parts[1], []byte(msg.Payload))
			}
		}
	}()

	wsHandler := ws.NewHandler(hub, inventoryService)
	router := api.NewRouter(inventoryService, reservationService, waitlistService, wsHandler, db, rateLimiter)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.ServerPort),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Server starting on port %s", cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// splitChannel splits a Redis channel name like "sale:abc:pubsub" into ["sale","abc","pubsub"].
func splitChannel(channel string) []string {
	result := make([]string, 0, 3)
	start := 0
	for i := 0; i < len(channel); i++ {
		if channel[i] == ':' {
			result = append(result, channel[start:i])
			start = i + 1
			if len(result) == 2 {
				// remaining is the suffix (could contain colons in uuid, but our
				// keys are sale:{uuid}:pubsub so this is safe to take the rest)
				result = append(result, channel[start:])
				return result
			}
		}
	}
	result = append(result, channel[start:])
	return result
}
