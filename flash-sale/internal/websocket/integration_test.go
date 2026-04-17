package websocket_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flashsale/backend/internal/config"
	"github.com/flashsale/backend/internal/events"
	"github.com/flashsale/backend/internal/inventory"
	redisclient "github.com/flashsale/backend/internal/redis"
	"github.com/flashsale/backend/internal/reservation"
	ws "github.com/flashsale/backend/internal/websocket"
	gorillaws "github.com/gorilla/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// splitChannel splits "sale:{id}:pubsub" → ["sale", "{id}", "pubsub"]
func splitChannel(channel string) []string {
	result := make([]string, 0, 3)
	start := 0
	for i := 0; i < len(channel); i++ {
		if channel[i] == ':' {
			result = append(result, channel[start:i])
			start = i + 1
			if len(result) == 2 {
				result = append(result, channel[start:])
				return result
			}
		}
	}
	result = append(result, channel[start:])
	return result
}

func setupIntegration(t *testing.T) (*ws.Hub, *inventory.Service, *reservation.Service, *httptest.Server, *pgxpool.Pool, func()) {
	t.Helper()

	cfg, err := config.Load()
	require.NoError(t, err)

	rc, err := redisclient.NewClient(cfg.RedisURL)
	require.NoError(t, err)

	db, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	require.NoError(t, err)

	publisher := events.NewPublisher(rc.GetClient())
	invSvc := inventory.NewService(rc, db)
	resSvc := reservation.NewService(rc, db, cfg.ReservationTTLSeconds, publisher)

	hub := ws.NewHub()
	go hub.Run()

	// Redis → Hub fan-out goroutine
	rdb := rc.GetClient()
	salePubsub := rdb.PSubscribe(context.Background(), "sale:*:pubsub")
	userPubsub := rdb.PSubscribe(context.Background(), "user:*:events")
	go func() {
		for msg := range salePubsub.Channel() {
			parts := splitChannel(msg.Channel)
			if len(parts) == 3 {
				hub.Publish(parts[1], []byte(msg.Payload))
			}
		}
	}()
	go func() {
		for msg := range userPubsub.Channel() {
			parts := splitChannel(msg.Channel)
			if len(parts) == 3 {
				hub.PublishToUser(parts[1], []byte(msg.Payload))
			}
		}
	}()

	upgrader := gorillaws.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsHandler := ws.NewHandler(hub, invSvc)

	mux := http.NewServeMux()
	// Simple adapter: extract sale ID from path /ws/sales/{id}
	mux.HandleFunc("/ws/sales/", func(w http.ResponseWriter, r *http.Request) {
		// Use gorilla websocket directly to set URL param manually
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ws/sales/"), "/")
		saleID := parts[0]
		userID := r.URL.Query().Get("user_id")

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		client := ws.NewClient(hub, conn)
		hub.Subscribe(saleID, userID, client)

		// Send initial stock snapshot
		saleUUID, err := uuid.Parse(saleID)
		if err == nil {
			_, stats, err := invSvc.GetSale(context.Background(), saleUUID)
			if err == nil {
				msg := map[string]interface{}{
					"event": "stock_update",
					"data":  map[string]interface{}{"remaining": stats.Available},
				}
				if b, err := json.Marshal(msg); err == nil {
					client.DirectSend(b)
				}
			}
		}

		go client.StartWritePump()
		go client.StartReadPump()
	})
	_ = wsHandler

	srv := httptest.NewServer(mux)

	cleanup := func() {
		srv.Close()
		salePubsub.Close()
		userPubsub.Close()
		rc.Close()
		db.Close()
	}

	return hub, invSvc, resSvc, srv, db, cleanup
}

func TestLiveStockBroadcast(t *testing.T) {
	hub, invSvc, resSvc, srv, db, cleanup := setupIntegration(t)
	defer cleanup()
	_ = hub
	_ = db

	ctx := context.Background()

	sale := &inventory.SaleInput{
		Name:       "Integration Test Sale",
		TotalStock: 100,
		StartTime:  time.Now(),
		EndTime:    time.Now().Add(1 * time.Hour),
	}
	createdSale, err := invSvc.CreateSaleAndReturn(ctx, sale)
	require.NoError(t, err)

	wsURL := fmt.Sprintf("ws%s/ws/sales/%s", strings.TrimPrefix(srv.URL, "http"), createdSale.ID)
	wsConn, _, err := gorillaws.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer wsConn.Close()

	// Read initial stock_update on connect
	wsConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, initialMsg, err := wsConn.ReadMessage()
	require.NoError(t, err, "should receive initial stock snapshot")
	assert.Contains(t, string(initialMsg), "stock_update")

	// Now reserve in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		userID := uuid.New()
		itemID := uuid.New()
		resSvc.Reserve(ctx, userID, createdSale.ID, itemID, "")
	}()

	// Expect stock_update event within 500ms
	wsConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, liveMsg, err := wsConn.ReadMessage()
	require.NoError(t, err, "should receive live stock_update after reserve")

	var event map[string]interface{}
	require.NoError(t, json.Unmarshal(liveMsg, &event))
	assert.Equal(t, "stock_update", event["event"])
	data := event["data"].(map[string]interface{})
	assert.Equal(t, float64(99), data["remaining"])
}
