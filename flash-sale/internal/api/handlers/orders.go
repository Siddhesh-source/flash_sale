package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/flashsale/backend/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrdersHandler struct {
	db *pgxpool.Pool
}

func NewOrdersHandler(db *pgxpool.Pool) *OrdersHandler {
	return &OrdersHandler{db: db}
}

func (h *OrdersHandler) GetOrder(w http.ResponseWriter, r *http.Request) {
	orderIDStr := chi.URLParam(r, "id")
	orderID, err := uuid.Parse(orderIDStr)
	if err != nil {
		http.Error(w, "Invalid order ID", http.StatusBadRequest)
		return
	}

	var order models.Order
	query := `
		SELECT o.id, o.reservation_id, o.user_id, o.sale_id, o.amount, o.status, o.idempotency_key, o.created_at
		FROM orders o
		WHERE o.id = $1
	`
	err = h.db.QueryRow(r.Context(), query, orderID).Scan(
		&order.ID, &order.ReservationID, &order.UserID, &order.SaleID, &order.Amount, &order.Status, &order.IdempotencyKey, &order.CreatedAt,
	)
	if err != nil {
		http.Error(w, "Order not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}
