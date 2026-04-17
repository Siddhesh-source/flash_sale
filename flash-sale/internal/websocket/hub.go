package websocket

import (
	"log"
	"sync"

	"github.com/flashsale/backend/internal/metrics"
)

type registration struct {
	client *Client
	saleID string
	userID string
}

type BroadcastMsg struct {
	SaleID  string
	Payload []byte
}

type Hub struct {
	rooms      map[string]map[*Client]bool
	userRooms  map[string]map[*Client]bool
	register   chan registration
	unregister chan *Client
	broadcast  chan BroadcastMsg
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		rooms:      make(map[string]map[*Client]bool),
		userRooms:  make(map[string]map[*Client]bool),
		register:   make(chan registration, 256),
		unregister: make(chan *Client, 256),
		broadcast:  make(chan BroadcastMsg, 1024),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case reg := <-h.register:
			h.mu.Lock()
			if reg.saleID != "" {
				if h.rooms[reg.saleID] == nil {
					h.rooms[reg.saleID] = make(map[*Client]bool)
				}
				h.rooms[reg.saleID][reg.client] = true
			}
			if reg.userID != "" {
				if h.userRooms[reg.userID] == nil {
					h.userRooms[reg.userID] = make(map[*Client]bool)
				}
				h.userRooms[reg.userID][reg.client] = true
			}
			h.mu.Unlock()
			metrics.WebSocketConnections.Inc()
			log.Printf("Client registered: sale=%s user=%s", reg.saleID, reg.userID)

		case client := <-h.unregister:
			h.mu.Lock()
			if client.saleID != "" {
				if clients, ok := h.rooms[client.saleID]; ok {
					delete(clients, client)
					if len(clients) == 0 {
						delete(h.rooms, client.saleID)
					}
				}
			}
			if client.userID != "" {
				if clients, ok := h.userRooms[client.userID]; ok {
					delete(clients, client)
					if len(clients) == 0 {
						delete(h.userRooms, client.userID)
					}
				}
			}
			close(client.send)
			h.mu.Unlock()
			metrics.WebSocketConnections.Dec()
			log.Printf("Client unregistered: sale=%s user=%s", client.saleID, client.userID)

		case msg := <-h.broadcast:
			h.mu.RLock()
			clients := h.rooms[msg.SaleID]
			h.mu.RUnlock()

			for client := range clients {
				select {
				case client.send <- msg.Payload:
				default:
					go h.Unregister(client)
				}
			}
		}
	}
}

func (h *Hub) Subscribe(saleID, userID string, client *Client) {
	client.saleID = saleID
	client.userID = userID
	h.register <- registration{client: client, saleID: saleID, userID: userID}
}

func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

func (h *Hub) Publish(saleID string, payload []byte) {
	h.broadcast <- BroadcastMsg{SaleID: saleID, Payload: payload}
}

func (h *Hub) PublishToUser(userID string, payload []byte) {
	h.mu.RLock()
	clients := h.userRooms[userID]
	h.mu.RUnlock()

	for client := range clients {
		select {
		case client.send <- payload:
		default:
			go h.Unregister(client)
		}
	}
}
