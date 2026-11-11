package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"aurumledger/internal/domain"
	"aurumledger/internal/service"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type Handler struct {
	walletSvc *service.WalletService
	reconSvc  *service.ReconciliationService
	validator *validator.Validate
}

func NewHandler(walletSvc *service.WalletService, reconSvc *service.ReconciliationService) *Handler {
	return &Handler{
		walletSvc: walletSvc,
		reconSvc:  reconSvc,
		validator: validator.New(),
	}
}

// ErrorResponse represents a standardized JSON error.
type ErrorResponse struct {
	Code    int    `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func respondError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Code:    code,
		Error:   http.StatusText(code),
		Message: msg,
	})
}

func respondJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// Request Models
type CreateWalletReq struct {
	UserID         string `json:"user_id" validate:"required,uuid"`
	Name           string `json:"name"`
	InitialBalance int64  `json:"initial_balance" validate:"gte=0"` // In milligrams of 24K gold
}

type TransferReq struct {
	SenderID   string `json:"sender_id" validate:"required,uuid"`
	ReceiverID string `json:"receiver_id" validate:"required,uuid"`
	Amount     int64  `json:"amount" validate:"required,gt=0"` // In milligrams of 24K gold
}

// CreateWallet handles POST /wallets
func (h *Handler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	var req CreateWalletReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request JSON body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	uid, _ := uuid.Parse(req.UserID)
	wallet, err := h.walletSvc.CreateWalletWithName(r.Context(), uid, req.Name, req.InitialBalance)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, wallet)
}

// GetBalance handles GET /wallets/{id}
func (h *Handler) GetBalance(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		respondError(w, http.StatusBadRequest, "Missing wallet ID parameter")
		return
	}

	walletID, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid wallet UUID format")
		return
	}

	wallet, err := h.walletSvc.GetBalance(r.Context(), walletID)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) || err.Error() == "record not found" {
			respondError(w, http.StatusNotFound, "Wallet not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, wallet)
}

// Transfer handles POST /transfers
// Enforces:
// 1. Idempotency-Key header requirement
// 2. Ownership / Authenticated user validation
// 3. Atomicity of financial updates & ledger entries
func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {
	// Priority 1: Extract Idempotency-Key header
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		respondError(w, http.StatusBadRequest, "Idempotency-Key header is required")
		return
	}

	var req TransferReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request JSON body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	senderID, _ := uuid.Parse(req.SenderID)
	receiverID, _ := uuid.Parse(req.ReceiverID)

	// Authenticated User Context
	// In production, extracted from JWT / Session auth middleware.
	// For testing and API usage, support X-User-Id header or infer from sender wallet.
	var authUserID uuid.UUID
	authHeader := r.Header.Get("X-User-Id")
	if authHeader != "" {
		if parsed, err := uuid.Parse(authHeader); err == nil {
			authUserID = parsed
		}
	}

	if authUserID == uuid.Nil {
		// Look up sender's user_id if not explicitly provided in header
		senderWallet, err := h.walletSvc.GetBalance(r.Context(), senderID)
		if err != nil {
			respondError(w, http.StatusNotFound, "Sender wallet not found")
			return
		}
		authUserID = senderWallet.UserID
	}

	result, err := h.walletSvc.TransferMoney(r.Context(), idempotencyKey, authUserID, senderID, receiverID, req.Amount)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrIdempotencyPayloadMismatch):
			respondError(w, http.StatusUnprocessableEntity, err.Error())
		case errors.Is(err, domain.ErrIdempotencyConflict):
			respondError(w, http.StatusConflict, err.Error())
		case errors.Is(err, domain.ErrInsufficientFunds):
			respondError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, domain.ErrUnauthorizedWallet):
			respondError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, domain.ErrSelfTransfer), errors.Is(err, domain.ErrInvalidAmount):
			respondError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, domain.ErrWalletFrozen):
			respondError(w, http.StatusLocked, err.Error())
		default:
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if result.IsIdempotentReplay {
		w.Header().Set("X-Idempotent-Replay", "true")
	}

	respondJSON(w, http.StatusOK, result)
}

// GetLedgerHistory handles GET /wallets/{id}/ledger
func (h *Handler) GetLedgerHistory(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		respondError(w, http.StatusBadRequest, "Missing wallet ID parameter")
		return
	}

	walletID, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid wallet UUID format")
		return
	}

	entries, err := h.walletSvc.GetLedgerHistory(r.Context(), walletID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"wallet_id": walletID,
		"count":     len(entries),
		"entries":   entries,
	})
}

// Reconcile handles POST /reconcile
func (h *Handler) Reconcile(w http.ResponseWriter, r *http.Request) {
	if h.reconSvc == nil {
		respondError(w, http.StatusNotImplemented, "Reconciliation service not enabled")
		return
	}

	report, err := h.reconSvc.Reconcile(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, report)
}

// HealthCheck handles GET /health
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "UP",
		"service": "AurumLedger",
		"version": "1.0.0",
	})
}

// ListWallets handles GET /wallets
func (h *Handler) ListWallets(w http.ResponseWriter, r *http.Request) {
	wallets, err := h.walletSvc.ListWallets(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, wallets)
}

// GetAllLedger handles GET /ledger
func (h *Handler) GetAllLedger(w http.ResponseWriter, r *http.Request) {
	entries, err := h.walletSvc.GetAllLedgerEntries(r.Context(), 50)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"count":   len(entries),
		"entries": entries,
	})
}

// GetOutbox handles GET /outbox
func (h *Handler) GetOutbox(w http.ResponseWriter, r *http.Request) {
	events, err := h.walletSvc.GetRecentOutboxEvents(r.Context(), 30)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"count":  len(events),
		"events": events,
	})
}

// SeedDemo handles POST /seed (optionally with ?reset=true)
func (h *Handler) SeedDemo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	shouldReset := r.URL.Query().Get("reset") == "true"

	type DemoUserSeed struct {
		UserID  uuid.UUID
		Name    string
		Balance int64
	}

	seeds := []DemoUserSeed{
		{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440001"), Name: "Alice Sharma (Daily Saver)", Balance: 2500},
		{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440002"), Name: "Bob Patel (Investor)", Balance: 1500},
		{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440003"), Name: "Charlie Verma (Gold Lease)", Balance: 1000},
		{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440004"), Name: "Diana Singh (Merchant)", Balance: 750},
		{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440005"), Name: "Gullak Treasury Vault", Balance: 10000},
	}

	existing, _ := h.walletSvc.ListWallets(ctx)

	// If reset requested or no wallets exist, seed the standard users
	if shouldReset || len(existing) == 0 {
		for _, s := range seeds {
			// Find existing wallet by UserID
			var foundWallet *domain.Wallet
			for _, ew := range existing {
				if ew.UserID == s.UserID {
					foundWallet = &ew
					break
				}
			}

			if foundWallet == nil {
				_, _ = h.walletSvc.CreateWalletWithName(ctx, s.UserID, s.Name, s.Balance)
			}
		}
	} else if len(existing) < 5 {
		// Fill in missing demo users
		for _, s := range seeds {
			found := false
			for _, ew := range existing {
				if ew.UserID == s.UserID {
					found = true
					break
				}
			}
			if !found {
				_, _ = h.walletSvc.CreateWalletWithName(ctx, s.UserID, s.Name, s.Balance)
			}
		}
	}

	all, _ := h.walletSvc.ListWallets(ctx)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Gullak demo accounts ready for demonstration",
		"wallets": all,
	})
}

