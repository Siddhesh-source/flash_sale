package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/flashsale/backend/internal/inventory"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Handler struct {
	hub              *Hub
	inventoryService *inventory.Service
}

func NewHandler(hub *Hub, inventoryService *inventory.Service) *Handler {
	return &Handler{
		hub:              hub,
		inventoryService: inventoryService,
	}
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	saleIDStr := chi.URLParam(r, "id")
	saleID, err := uuid.Parse(saleIDStr)
	if err != nil {
		http.Error(w, "Invalid sale ID", http.StatusBadRequest)
		return
	}

	userIDStr := r.URL.Query().Get("user_id")
	var userID string
	if userIDStr != "" {
		if _, err := uuid.Parse(userIDStr); err == nil {
			userID = userIDStr
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	client := NewClient(h.hub, conn)
	h.hub.Subscribe(saleID.String(), userID, client)

	sale, stats, err := h.inventoryService.GetSale(context.Background(), saleID)
	if err == nil {
		initialMsg := map[string]interface{}{
			"event": "stock_update",
			"data": map[string]interface{}{
				"remaining": stats.Available,
			},
		}
		if msgBytes, err := json.Marshal(initialMsg); err == nil {
			client.send <- msgBytes
		}
	}

	go client.StartWritePump()
	go client.StartReadPump()

	log.Printf("WebSocket connected: sale=%s user=%s", saleID, userID)
}
