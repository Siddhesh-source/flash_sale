package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/flashsale/backend/internal/inventory"
	"github.com/flashsale/backend/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type SalesHandler struct {
	inventoryService *inventory.Service
}

func NewSalesHandler(inventoryService *inventory.Service) *SalesHandler {
	return &SalesHandler{
		inventoryService: inventoryService,
	}
}

type CreateSaleRequest struct {
	Name       string    `json:"name"`
	TotalStock int       `json:"total_stock"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
}

type SaleResponse struct {
	*models.Sale
	Stats *models.SaleStats `json:"stats,omitempty"`
}

type UpdateStatusRequest struct {
	Status models.SaleStatus `json:"status"`
}

func (h *SalesHandler) CreateSale(w http.ResponseWriter, r *http.Request) {
	var req CreateSaleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	sale := &models.Sale{
		Name:       req.Name,
		TotalStock: req.TotalStock,
		StartTime:  req.StartTime,
		EndTime:    req.EndTime,
	}

	if err := h.inventoryService.CreateSale(r.Context(), sale); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(SaleResponse{Sale: sale})
}

func (h *SalesHandler) GetSale(w http.ResponseWriter, r *http.Request) {
	saleIDStr := chi.URLParam(r, "id")
	saleID, err := uuid.Parse(saleIDStr)
	if err != nil {
		http.Error(w, "Invalid sale ID", http.StatusBadRequest)
		return
	}

	sale, stats, err := h.inventoryService.GetSale(r.Context(), saleID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SaleResponse{Sale: sale, Stats: stats})
}

func (h *SalesHandler) UpdateSaleStatus(w http.ResponseWriter, r *http.Request) {
	saleIDStr := chi.URLParam(r, "id")
	saleID, err := uuid.Parse(saleIDStr)
	if err != nil {
		http.Error(w, "Invalid sale ID", http.StatusBadRequest)
		return
	}

	var req UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.inventoryService.UpdateSaleStatus(r.Context(), saleID, req.Status); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
