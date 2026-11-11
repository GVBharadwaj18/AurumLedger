package handler

import (
	_ "embed"
	"net/http"
)

//go:embed static/index.html
var dashboardHTML []byte

func NewRouter(h *Handler) http.Handler {
	mux := http.NewServeMux()

	// 1. Interactive Web Dashboard (Cockpit UI)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/dashboard" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(dashboardHTML)
	})

	mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(dashboardHTML)
	})

	// 2. Health Probe
	mux.HandleFunc("GET /health", h.HealthCheck)

	// 3. Digital Gold Wallet & Ledger Core Endpoints
	mux.HandleFunc("POST /wallets", h.CreateWallet)
	mux.HandleFunc("GET /wallets", h.ListWallets)
	mux.HandleFunc("GET /wallets/{id}", h.GetBalance)
	mux.HandleFunc("POST /transfers", h.Transfer)
	mux.HandleFunc("GET /wallets/{id}/ledger", h.GetLedgerHistory)
	mux.HandleFunc("GET /ledger", h.GetAllLedger)
	mux.HandleFunc("GET /outbox", h.GetOutbox)
	mux.HandleFunc("POST /reconcile", h.Reconcile)
	mux.HandleFunc("POST /seed", h.SeedDemo)

	return mux
}
