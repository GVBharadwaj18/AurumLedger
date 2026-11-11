# 🚀 How to Run AurumLedger

This guide provides step-by-step instructions for running, testing, and demonstrating **AurumLedger** on any operating system.

---

## 🖥️ Interactive Web Cockpit (Frontend UI)

AurumLedger includes an **embedded interactive fintech dashboard ("AurumLedger Cockpit")** compiled directly into the binary (zero build steps, zero Node.js/npm dependencies).

Simply open your browser to:
👉 **`http://localhost:8080/`** or **`http://localhost:8080/dashboard`**

### What You Can Do in the Web Cockpit:
1. **Multi-User Login & Account Switcher (Gullak FinTech Experience)**:
   - Switch active logged-in user with 1 click:
     - 👑 **Alice Sharma** (Daily Saver: 2,500 mg)
     - 📈 **Bob Patel** (Investor: 1,500 mg)
     - 🏦 **Charlie Verma** (Gold Lease: 1,000 mg)
     - 🛍️ **Diana Singh** (Merchant: 750 mg)
     - 🏛️ **Gullak Treasury Vault** (Liquidity: 10,000 mg)
   - When logged in as Alice, send gold to Bob. Then switch to Bob and send gold back!
2. **Transfer 24K Gold Console**:
   - Sender is automatically bound to your authenticated account.
   - Select recipient from other users.
   - Enter gold amounts in milligrams (with automatic conversion to grams & ₹ rupee estimates).
   - Click **"🎲 New Key"** to generate a unique receipt ID.
   - Click **"🔁 Replay Key"** to test duplicate prevention and see the `Duplicate Prevented` badge without double-charging!
3. **One-Click Security, Concurrency & Tampering Lab**:
   - Click **"Test Security (403)"** to simulate spending another user's wallet without their login, proving strict tenant authorization.
   - Click **"Test Tampering (422)"** to simulate altering the amount with an existing receipt key, demonstrating immediate rejection.
   - Click **"🔥 10x Concurrent"** to fire 10 simultaneous transfers and verify deterministic row locks prevent deadlocks.
4. **Live Accounting Book (Double-Entry Ledger)**:
   - Real-time audit table displaying paired `CREDIT TO RECEIVER (+)` and `DEBIT FROM SENDER (-)` records with running balance snapshots.
5. **Background Notifications (Transactional Outbox)**:
   - Real-time stream showing events safely saved to the database before delivery to RabbitMQ.
6. **Verify Vault Balance (Reconciliation Engine)**:
   - Click **"🔍 Run Vault Mathematical Audit"** to mathematically verify that $\sum \text{Debits} == \sum \text{Credits}$ across the entire system.

---

## ⚡ Option 1: Docker Compose (Recommended for Full Production Topology)

The fastest and most robust way to run the entire production stack (PostgreSQL 16, Redis 7, RabbitMQ with Web Management, and the AurumLedger Go server) with a single command:

```bash
docker compose up -d --build
```

### What Docker Compose Launches:

| Service | Port | Description |
| :--- | :--- | :--- |
| **AurumLedger API** | `8080` | High-concurrency digital gold transfer & accounting engine |
| **PostgreSQL 16** | `5432` | Primary database with ACID transactions & row locking |
| **RabbitMQ 3.13** | `5672` | AMQP broker with durable exchanges & Dead-Letter Queues |
| **RabbitMQ Web UI** | `15672` | Management dashboard (Username: `guest`, Password: `guest`) |
| **Redis 7** | `6379` | In-memory cache layer for wallet balances |

To check the health of all running containers:
```bash
docker compose ps
```

To stop all services:
```bash
docker compose down
```

---

## 💻 Option 2: Local Run with Go

AurumLedger includes **zero-setup local fallback**:
- If PostgreSQL is available, it connects to PostgreSQL.
- If PostgreSQL is offline, it automatically boots with an embedded database (`sqlite://aurumledger.db`), allowing instant evaluation without requiring any infrastructure to be running.

### 1. Install Dependencies
```bash
go mod tidy
```

### 2. Run the Server

On Windows, use the pre-compiled binary or `run.bat` / `run.ps1` (running via `go run` directly is blocked by Windows 11 Application Control because it writes temporary binaries into `%TEMP%`):

```powershell
# Option A: One-click runner script
.\run.ps1
# or
.\run.bat

# Option B: Run pre-compiled binary directly
.\server.exe

# Option C: Build and run
go build -mod=vendor -o server.exe ./cmd/server
.\server.exe
```

> [!NOTE]
> **If Windows displays *"An Application Control policy has blocked this file"* or SmartScreen pop-up:**
> 1. **Unblock the file**: Right-click `server.exe` -> **Properties** -> Check **"Unblock"** at the bottom -> Click **OK**. (Or run `Unblock-File .\server.exe` in PowerShell).
> 2. **Enable Windows Developer Mode**: Open **Windows Settings** -> **System** -> **For developers** -> Toggle **Developer Mode: ON** (allows running locally compiled binaries).
> 3. **Smart App Control**: If Windows 11 Smart App Control is set to strict "On", set it to "Evaluation" or "Off" in Windows Security -> App & Browser Control.
> 4. **Or use Docker (Zero OS Policy Restrictions)**: `docker compose up --build` runs everything inside Linux containers, 100% immune to Windows host execution blocks.

The server starts on port `8080`:
```
==================================================================
   AURUMLEDGER - High-Concurrency Digital Gold Accounting Engine  
==================================================================
.env configuration loaded successfully
Connected to PostgreSQL & AurumLedger schemas migrated successfully
AurumLedger API server listening on :8080
```

---

## 🧪 Running the Automated Test Suite

AurumLedger features a 100% automated test suite validating all four priorities:

```bash
go test -v -mod=vendor ./tests/...
```

### What the Test Suite Verifies:

1. **Persistent Idempotency (`idempotency_test.go`)**:
   - `TestIdempotentTransfer_SameRequestMultipleTimes`: Validates that repeated retries return the original transaction and gold is never transferred twice.
   - `TestIdempotentTransfer_ConcurrentDuplicateRequests`: Validates race-condition safety when identical keys arrive simultaneously.
   - `TestIdempotentTransfer_NetworkTimeoutRetry`: Simulates client timeout and asserts that retrying returns the original response.
   - `TestIdempotentTransfer_PayloadMismatchRejection`: Asserts that reusing a key with a different amount or receiver is strictly rejected (`422 Unprocessable Entity`).

2. **Double-Entry Ledger Accounting (`ledger_test.go`)**:
   - `TestDoubleEntryLedger_BalancedEntries`: Asserts paired `CREDIT` (sender) and `DEBIT` (receiver) entries created per transaction ($\sum \text{Debits} == \sum \text{Credits}$).
   - `TestDoubleEntryLedger_FailedTransferNoPartialRecords`: Verifies atomic rollback on insufficient funds (zero partial entries).
   - `TestDoubleEntryLedger_ConcurrentTransfersPreserveInvariants`: Asserts that 20 concurrent cross-transfers preserve the total gold quantity.
   - `TestDoubleEntryLedger_ReconciliationDetectsInconsistency`: Proves that the reconciliation engine detects tampered wallet balances.

3. **Transactional Outbox Pattern (`outbox_test.go`)**:
   - `TestOutbox_AtomicWithTransfer`: Asserts outbox event is created inside the same database transaction.
   - `TestOutbox_RabbitMQUnavailableRetries`: Asserts transfers succeed when broker is down and retry count increments.
   - `TestOutbox_ApplicationRestartRecoversPendingEvents`: Verifies crash recovery on restart.
   - `TestOutbox_MultiplePublishersConcurrencySafe`: Tests `SELECT ... FOR UPDATE SKIP LOCKED` preventing duplicate publishing across scaled pods.

4. **Reliable Consumer & Failure Handling (`consumer_test.go`)**:
   - `TestConsumer_IdempotentDuplicateDelivery`: Asserts duplicate deliveries are deduplicated via `processed_events`.
   - `TestConsumer_PoisonMessageRoutedToDLQ`: Asserts malformed payloads are rejected to the Dead-Letter Queue without worker crashes.
   - `TestConsumer_RetryAndExponentialBackoff`: Verifies exponential backoff retry for transient failures.

5. **High-Concurrency Stress Test (`concurrency_test.go`)**:
   - `TestConcurrentTransfers`: 50 concurrent goroutines executing simultaneous transfers, verifying deterministic lock ordering, zero deadlocks, and 100% balanced ledger reconciliation.

---

## 🎬 Live End-to-End Demonstration Script

To run the automated live demo script that boots the server, creates wallets, executes transfers, tests idempotency replays, runs a reconciliation audit, and verifies ledger records:

```bash
bash scripts/test_live.sh
```

---

## 📡 Step-by-Step API Usage Guide (cURL Examples)

### 1. Health Probe
```bash
curl -X GET http://localhost:8080/health
```
**Response (`200 OK`)**:
```json
{
  "service": "AurumLedger",
  "status": "UP",
  "version": "1.0.0"
}
```

---

### 2. Create Digital Gold Wallets
Create Wallet A with 1,000 mg of 24K gold (1 gram):
```bash
curl -X POST http://localhost:8080/wallets \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "550e8400-e29b-41d4-a716-446655440001",
    "initial_balance": 1000
  }'
```

Create Wallet B with 200 mg of 24K gold:
```bash
curl -X POST http://localhost:8080/wallets \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "550e8400-e29b-41d4-a716-446655440002",
    "initial_balance": 200
  }'
```

---

### 3. Check Wallet Balance
```bash
curl -X GET http://localhost:8080/wallets/<WALLET_A_ID>
```

---

### 4. Execute a Gold Transfer (With Idempotency Key)
Transfers 350 mg of gold from Wallet A to Wallet B:

```bash
curl -X POST http://localhost:8080/transfers \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: gold-tx-unique-key-001" \
  -H "X-User-Id: 550e8400-e29b-41d4-a716-446655440001" \
  -d '{
    "sender_id": "<WALLET_A_ID>",
    "receiver_id": "<WALLET_B_ID>",
    "amount": 350
  }'
```

**Response (`200 OK`)**:
```json
{
  "transaction": {
    "id": "e4f9c2d4-41f3-4ff4-ae33-14d3c0f70003",
    "sender_id": "97ee5aa5-e058-4e94-8ae2-8769a9ceda40",
    "receiver_id": "aa9ee091-9b6e-4414-9765-ec33e71ecb94",
    "amount": 350,
    "type": "TRANSFER",
    "status": "COMPLETED",
    "created_at": "2026-11-10T18:02:15Z"
  },
  "is_idempotent_replay": false,
  "sender_balance_after": 650,
  "receiver_balance_after": 550
}
```

---

### 5. Test Idempotent Retry (Simulate Client Timeout / Network Drop)
Send the exact same request with the same `Idempotency-Key`:
```bash
curl -i -X POST http://localhost:8080/transfers \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: gold-tx-unique-key-001" \
  -H "X-User-Id: 550e8400-e29b-41d4-a716-446655440001" \
  -d '{
    "sender_id": "<WALLET_A_ID>",
    "receiver_id": "<WALLET_B_ID>",
    "amount": 350
  }'
```
**Response (`200 OK`)**:
- HTTP Header includes: `X-Idempotent-Replay: true`
- Returns original transaction details.
- Gold is **not** transferred again.

---

### 6. Test Key Tampering Rejection
Try reusing `gold-tx-unique-key-001` with a different amount (e.g. 500 instead of 350):
```bash
curl -X POST http://localhost:8080/transfers \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: gold-tx-unique-key-001" \
  -H "X-User-Id: 550e8400-e29b-41d4-a716-446655440001" \
  -d '{
    "sender_id": "<WALLET_A_ID>",
    "receiver_id": "<WALLET_B_ID>",
    "amount": 500
  }'
```
**Response (`422 Unprocessable Entity`)**:
```json
{
  "code": 422,
  "error": "Unprocessable Entity",
  "message": "idempotency key reused with different request payload"
}
```

---

### 7. View Immutable Double-Entry Ledger History
```bash
curl -X GET http://localhost:8080/wallets/<WALLET_A_ID>/ledger
```
**Response**:
```json
{
  "wallet_id": "97ee5aa5-e058-4e94-8ae2-8769a9ceda40",
  "count": 2,
  "entries": [
    {
      "id": "...",
      "transaction_id": "...",
      "entry_type": "DEBIT",
      "amount": 1000,
      "balance_after": 1000,
      "description": "Initial gold deposit"
    },
    {
      "id": "...",
      "transaction_id": "...",
      "entry_type": "CREDIT",
      "amount": 350,
      "balance_after": 650,
      "description": "Gold transfer outflow to wallet aa9ee091-..."
    }
  ]
}
```

---

### 8. Run System-Wide Ledger Reconciliation Audit
Audits all wallets against ledger-derived balances and checks conservation of gold:
```bash
curl -X POST http://localhost:8080/reconcile
```
**Response (`200 OK`)**:
```json
{
  "total_wallets": 2,
  "matched_wallets": 2,
  "mismatched_wallets": 0,
  "system_total_balance": 1200,
  "system_total_debits": 1550,
  "system_total_credits": 350,
  "is_system_balanced": true,
  "audited_at": "2026-11-10T18:02:15Z"
}
```

---

### 9. Inspect Global Double-Entry Ledger Stream
```bash
curl -X GET http://localhost:8080/ledger
```
**Sample Output**:
```json
{
  "count": 4,
  "entries": [
    {
      "transaction_id": "90e75a6c-4861-4122-83bf-197e415fb899",
      "wallet_id": "550e8400-e29b-41d4-a716-446655440002",
      "entry_type": "DEBIT",
      "amount": 350,
      "balance_after": 550,
      "description": "Transfer from wallet 550e8400-e29b-41d4-a716-446655440001",
      "created_at": "2026-11-11T12:00:00Z"
    },
    {
      "transaction_id": "90e75a6c-4861-4122-83bf-197e415fb899",
      "wallet_id": "550e8400-e29b-41d4-a716-446655440001",
      "entry_type": "CREDIT",
      "amount": 350,
      "balance_after": 650,
      "description": "Transfer to wallet 550e8400-e29b-41d4-a716-446655440002",
      "created_at": "2026-11-11T12:00:00Z"
    }
  ]
}
```

---

### 10. Inspect Transactional Outbox Events
```bash
curl -X GET http://localhost:8080/outbox
```
**Sample Output**:
```json
{
  "count": 2,
  "events": [
    {
      "id": "e2c39178-57d6-44e2-b1bc-ea63c4826132",
      "aggregate_type": "WALLET_TRANSFER",
      "aggregate_id": "90e75a6c-4861-4122-83bf-197e415fb899",
      "event_type": "TRANSFER_COMPLETED",
      "status": "PUBLISHED",
      "retry_count": 0,
      "created_at": "2026-11-11T12:00:00Z",
      "published_at": "2026-11-11T12:00:00Z"
    }
  ]
}
```

---

## ⚙️ Environment Variables (`.env`)

| Variable | Default Value | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | Port the HTTP API listens on |
| `DATABASE_URL` | `host=localhost user=postgres password=postgres dbname=wallet_db port=5432 sslmode=disable` | PostgreSQL DSN (or `sqlite://aurumledger.db`) |
| `REDIS_ADDR` | `localhost:6379` | Redis host and port |
| `RABBITMQ_URL` | `amqp://guest:guest@localhost:5672/` | RabbitMQ connection URI |

