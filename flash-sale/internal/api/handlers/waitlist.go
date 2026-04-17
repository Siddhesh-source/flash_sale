package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/flashsale/backend/internal/waitlist"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type WaitlistHandler struct {
	waitlistService *waitlist.Service
}

func NewWaitlistHandler(waitlistService *waitlist.Service) *WaitlistHandler {
	return &WaitlistHandler{
		waitlistService: waitlistService,
	}
}

type JoinWaitlistRequest struct {
	UserID uuid.UUID `json:"user_id"`
}

func (h *WaitlistHandler) Join(w http.ResponseWriter, r *http.Request) {
	saleIDStr := chi.URLParam(r, "id")
	saleID, err := uuid.Parse(saleIDStr)
	if err != nil {
		http.Error(w, "Invalid sale ID", http.StatusBadRequest)
		return
	}

	var req JoinWaitlistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	response, err := h.waitlistService.Join(r.Context(), saleID, req.UserID)
	if err != nil {
		if err.Error() == "stock available" {
			http.Error(w, "Stock available, please reserve directly", http.StatusConflict)
			return
		}
		if err.Error() == "already in waitlist" {
			http.Error(w, "Already in waitlist", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

func (h *WaitlistHandler) GetPosition(w http.ResponseWriter, r *http.Request) {
	saleIDStr := chi.URLParam(r, "id")
	saleID, err := uuid.Parse(saleIDStr)
	if err != nil {
		http.Error(w, "Invalid sale ID", http.StatusBadRequest)
		return
	}

	userIDStr := r.URL.Query().Get("user_id")
	if userIDStr == "" {
		http.Error(w, "user_id query parameter required", http.StatusBadRequest)
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	response, err := h.waitlistService.GetPosition(r.Context(), saleID, userID)
	if err != nil {
		http.Error(w, "User not in waitlist", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *WaitlistHandler) Leave(w http.ResponseWriter, r *http.Request) {
	saleIDStr := chi.URLParam(r, "id")
	saleID, err := uuid.Parse(saleIDStr)
	if err != nil {
		http.Error(w, "Invalid sale ID", http.StatusBadRequest)
		return
	}

	var req JoinWaitlistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.waitlistService.Leave(r.Context(), saleID, req.UserID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
