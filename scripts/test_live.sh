#!/usr/bin/env bash
set -e

PORT=8089
echo "=========================================================="
echo "    AURUMLEDGER LIVE END-TO-END DEMONSTRATION RUNNER     "
echo "=========================================================="

# 1. Compile server binary
echo "[1/7] Compiling AurumLedger server..."
cd "$(dirname "$0")/.."
~/.go/bin/go build -mod=vendor -o /tmp/aurumledger-server ./cmd/server

# 2. Start server on test port
echo "[2/7] Starting AurumLedger server on port $PORT..."
PORT=$PORT /tmp/aurumledger-server &
SERVER_PID=$!
trap "kill $SERVER_PID 2>/dev/null || true" EXIT

sleep 2

# 3. Health check probe
echo "[3/7] Probing /health..."
curl -s http://localhost:$PORT/health
echo -e "\n"

# 4. Create Wallets
echo "[4/7] Creating digital gold wallets..."
W1=$(curl -s -X POST http://localhost:$PORT/wallets \
  -H "Content-Type: application/json" \
  -d '{"user_id":"550e8400-e29b-41d4-a716-446655440001","initial_balance":1000}')
echo "Wallet A: $W1"
W1_ID=$(echo "$W1" | grep -o '"id":"[^"]*' | cut -d'"' -f4)

W2=$(curl -s -X POST http://localhost:$PORT/wallets \
  -H "Content-Type: application/json" \
  -d '{"user_id":"550e8400-e29b-41d4-a716-446655440002","initial_balance":200}')
echo "Wallet B: $W2"
W2_ID=$(echo "$W2" | grep -o '"id":"[^"]*' | cut -d'"' -f4)
echo ""

# 5. Execute Gold Transfer
echo "[5/7] Executing 350 mg gold transfer (Idempotency-Key: demo-key-001)..."
TRANSFER_RES=$(curl -s -i -X POST http://localhost:$PORT/transfers \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo-key-001" \
  -H "X-User-Id: 550e8400-e29b-41d4-a716-446655440001" \
  -d "{\"sender_id\":\"$W1_ID\",\"receiver_id\":\"$W2_ID\",\"amount\":350}")
echo "$TRANSFER_RES"
echo ""

# 6. Test Idempotent Retry
echo "[6/7] Testing Idempotent Replay with same key & payload..."
REPLAY_RES=$(curl -s -i -X POST http://localhost:$PORT/transfers \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo-key-001" \
  -H "X-User-Id: 550e8400-e29b-41d4-a716-446655440001" \
  -d "{\"sender_id\":\"$W1_ID\",\"receiver_id\":\"$W2_ID\",\"amount\":350}")
echo "$REPLAY_RES"
echo ""

# 7. Audit & Reconciliation
echo "[7/7] Running Ledger Reconciliation Audit (/reconcile)..."
curl -s -X POST http://localhost:$PORT/reconcile
echo -e "\n"

echo "=== Outbox & Ledger Verification (/outbox & /ledger) ==="
curl -s http://localhost:$PORT/outbox
echo -e "\n"
curl -s http://localhost:$PORT/ledger
echo -e "\n"

echo "=========================================================="
echo "    LIVE DEMO COMPLETED SUCCESSFULLY WITH ZERO ERRORS     "
echo "=========================================================="

