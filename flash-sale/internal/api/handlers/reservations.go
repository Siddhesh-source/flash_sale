package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/flashsale/backend/internal/reservation"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ReservationsHandler struct {
	reservationService *reservation.Service
}

func NewReservationsHandler(reservationService *reservation.Service) *ReservationsHandler {
	return &ReservationsHandler{
		reservationService: reservationService,
	}
}

type ReserveRequest struct {
	UserID uuid.UUID `json:"user_id"`
	ItemID uuid.UUID `json:"item_id"`
}

func (h *ReservationsHandler) Reserve(w http.ResponseWriter, r *http.Request) {
	saleIDStr := chi.URLParam(r, "id")
	saleID, err := uuid.Parse(saleIDStr)
	if err != nil {
		http.Error(w, "Invalid sale ID", http.StatusBadRequest)
		return
	}

	var req ReserveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")

	reservation, err := h.reservationService.Reserve(r.Context(), req.UserID, saleID, req.ItemID, idempotencyKey)
	if err != nil {
		if err.Error() == "sold out" {
			http.Error(w, "Sold out", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(reservation)
}

func (h *ReservationsHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	reservationIDStr := chi.URLParam(r, "id")
	reservationID, err := uuid.Parse(reservationIDStr)
	if err != nil {
		http.Error(w, "Invalid reservation ID", http.StatusBadRequest)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")

	order, err := h.reservationService.Confirm(r.Context(), reservationID, idempotencyKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(order)
}

func (h *ReservationsHandler) Release(w http.ResponseWriter, r *http.Request) {
	reservationIDStr := chi.URLParam(r, "id")
	reservationID, err := uuid.Parse(reservationIDStr)
	if err != nil {
		http.Error(w, "Invalid reservation ID", http.StatusBadRequest)
		return
	}

	if err := h.reservationService.Release(r.Context(), reservationID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
