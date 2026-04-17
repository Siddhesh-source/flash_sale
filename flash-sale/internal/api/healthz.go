package api

import (
	"net/http"
	"sync/atomic"
)

var reconciliationComplete atomic.Bool

func SetReconciliationComplete() {
	reconciliationComplete.Store(true)
}

func HealthzHandler(w http.ResponseWriter, r *http.Request) {
	if !reconciliationComplete.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Reconciliation in progress"))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
