package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aurumledger/internal/domain"
	"aurumledger/internal/handler"
	"github.com/google/uuid"
)

func setupTestRouter(t *testing.T) (http.Handler, *TestHarness) {
	h := SetupTestHarness(t)
	apiHandler := handler.NewHandler(h.WalletService, h.ReconService)
	router := handler.NewRouter(apiHandler)
	return router, h
}

func TestHealthCheckAPI(t *testing.T) {
	router, _ := setupTestRouter(t)

	req, _ := http.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["service"] != "AurumLedger" {
		t.Errorf("Expected service name AurumLedger, got %v", body["service"])
	}
}

func TestCreateWalletAPI(t *testing.T) {
	router, _ := setupTestRouter(t)

	userID := uuid.New()
	reqBody, _ := json.Marshal(map[string]interface{}{
		"user_id":         userID.String(),
		"initial_balance": 1000,
	})
	req, _ := http.NewRequest("POST", "/wallets", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}

	var response domain.Wallet
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if response.UserID != userID {
		t.Errorf("Expected user ID %s, got %s", userID, response.UserID)
	}
	if response.Balance != 1000 {
		t.Errorf("Expected initial balance 1000 mg, got %d", response.Balance)
	}
}

func TestGetBalanceAPI(t *testing.T) {
	router, h := setupTestRouter(t)

	userID := uuid.New()
	wallet := h.CreateTestWallet(t, userID, 750)

	req, _ := http.NewRequest("GET", "/wallets/"+wallet.ID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var response domain.Wallet
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if response.Balance != 750 {
		t.Errorf("Expected balance 750, got %d", response.Balance)
	}
}

func TestTransferAPI_IdempotencyAndLedger(t *testing.T) {
	router, h := setupTestRouter(t)

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)
	receiver := h.CreateTestWallet(t, user2, 200)

	idempotencyKey := "api-transfer-key-100"

	// 1. Transfer without Idempotency-Key should return 400 Bad Request
	noKeyBody, _ := json.Marshal(map[string]interface{}{
		"sender_id":   sender.ID.String(),
		"receiver_id": receiver.ID.String(),
		"amount":      100,
	})
	reqNoKey, _ := http.NewRequest("POST", "/transfers", bytes.NewBuffer(noKeyBody))
	reqNoKey.Header.Set("Content-Type", "application/json")
	wNoKey := httptest.NewRecorder()
	router.ServeHTTP(wNoKey, reqNoKey)

	if wNoKey.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 when Idempotency-Key is missing, got %d", wNoKey.Code)
	}

	// 2. Transfer with valid Idempotency-Key and User auth
	validBody, _ := json.Marshal(map[string]interface{}{
		"sender_id":   sender.ID.String(),
		"receiver_id": receiver.ID.String(),
		"amount":      300,
	})
	reqValid, _ := http.NewRequest("POST", "/transfers", bytes.NewBuffer(validBody))
	reqValid.Header.Set("Content-Type", "application/json")
	reqValid.Header.Set("Idempotency-Key", idempotencyKey)
	reqValid.Header.Set("X-User-Id", user1.String())

	wValid := httptest.NewRecorder()
	router.ServeHTTP(wValid, reqValid)

	if wValid.Code != http.StatusOK {
		t.Fatalf("Expected 200 for valid transfer, got %d. Body: %s", wValid.Code, wValid.Body.String())
	}

	// 3. Replay with identical key & body (Idempotent Retry)
	reqReplay, _ := http.NewRequest("POST", "/transfers", bytes.NewBuffer(validBody))
	reqReplay.Header.Set("Content-Type", "application/json")
	reqReplay.Header.Set("Idempotency-Key", idempotencyKey)
	reqReplay.Header.Set("X-User-Id", user1.String())

	wReplay := httptest.NewRecorder()
	router.ServeHTTP(wReplay, reqReplay)

	if wReplay.Code != http.StatusOK {
		t.Fatalf("Expected 200 for idempotent replay, got %d", wReplay.Code)
	}
	if wReplay.Header().Get("X-Idempotent-Replay") != "true" {
		t.Errorf("Expected X-Idempotent-Replay header to be true")
	}

	// 4. Inspect Wallet Ledger history
	reqLedger, _ := http.NewRequest("GET", "/wallets/"+sender.ID.String()+"/ledger", nil)
	wLedger := httptest.NewRecorder()
	router.ServeHTTP(wLedger, reqLedger)

	if wLedger.Code != http.StatusOK {
		t.Fatalf("Expected 200 for ledger query, got %d", wLedger.Code)
	}

	// 5. Test Reconciliation endpoint
	reqRecon, _ := http.NewRequest("POST", "/reconcile", nil)
	wRecon := httptest.NewRecorder()
	router.ServeHTTP(wRecon, reqRecon)

	if wRecon.Code != http.StatusOK {
		t.Fatalf("Expected 200 for reconcile endpoint, got %d", wRecon.Code)
	}

	var reconReport domain.ReconciliationReport
	_ = json.Unmarshal(wRecon.Body.Bytes(), &reconReport)
	if !reconReport.IsSystemBalanced {
		t.Errorf("Reconciliation reported unbalanced system: %+v", reconReport)
	}

	// 6. Test GET /wallets
	reqWallets, _ := http.NewRequest("GET", "/wallets", nil)
	wWallets := httptest.NewRecorder()
	router.ServeHTTP(wWallets, reqWallets)
	if wWallets.Code != http.StatusOK {
		t.Fatalf("Expected 200 for GET /wallets, got %d", wWallets.Code)
	}

	// 7. Test GET /ledger
	reqAllLedger, _ := http.NewRequest("GET", "/ledger", nil)
	wAllLedger := httptest.NewRecorder()
	router.ServeHTTP(wAllLedger, reqAllLedger)
	if wAllLedger.Code != http.StatusOK {
		t.Fatalf("Expected 200 for GET /ledger, got %d", wAllLedger.Code)
	}

	// 8. Test GET /outbox
	reqOutbox, _ := http.NewRequest("GET", "/outbox", nil)
	wOutbox := httptest.NewRecorder()
	router.ServeHTTP(wOutbox, reqOutbox)
	if wOutbox.Code != http.StatusOK {
		t.Fatalf("Expected 200 for GET /outbox, got %d", wOutbox.Code)
	}
}
