# 🏛️ AurumLedger: Architectural Deep Dive & Design Specifications

This document provides the technical specification and architectural deep dive of AurumLedger for engineering design reviews, production audits, and distributed system assessments.

---

## 1. Domain Models & Relational Database Schema

### 1.1 `wallets`
Represents the user's digital gold account. Balance is recorded in milligrams (mg) of fine 24K gold (where 1,000 mg = 1 gram).

```sql
CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    balance BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    currency VARCHAR(10) NOT NULL DEFAULT 'XAU_MG',
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wallets_user_id ON wallets(user_id);
```

### 1.2 `idempotency_keys` (Priority 1)
Stores client-submitted idempotency tokens persistently, guarded by a compound database constraint.

```sql
CREATE TABLE idempotency_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key VARCHAR(128) NOT NULL,
    user_id UUID NOT NULL,
    request_fingerprint VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL, -- 'PENDING', 'COMPLETED', 'FAILED'
    response_code INT DEFAULT 0,
    response_body TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX idx_user_key ON idempotency_keys(user_id, key);
```

### 1.3 `transactions`
The high-level record representing a completed business transfer.

```sql
CREATE TABLE transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id UUID,
    receiver_id UUID,
    amount BIGINT NOT NULL CHECK (amount > 0),
    type VARCHAR(20) NOT NULL, -- 'TRANSFER', 'DEPOSIT', 'WITHDRAWAL'
    status VARCHAR(20) NOT NULL DEFAULT 'COMPLETED',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_transactions_sender_id ON transactions(sender_id);
CREATE INDEX idx_transactions_receiver_id ON transactions(receiver_id);
```

### 1.4 `ledger_entries` (Priority 2)
Immutable, append-only double-entry ledger.

```sql
CREATE TABLE ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    entry_type VARCHAR(10) NOT NULL CHECK (entry_type IN ('DEBIT', 'CREDIT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    balance_after BIGINT NOT NULL CHECK (balance_after >= 0),
    description VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_transaction_id ON ledger_entries(transaction_id);
CREATE INDEX idx_ledger_wallet_id ON ledger_entries(wallet_id);
```

### 1.5 `outbox_events` (Priority 3)
Guarantees reliable message publication without the dual-write anti-pattern.

```sql
CREATE TABLE outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type VARCHAR(50) NOT NULL, -- 'WALLET_TRANSFER'
    aggregate_id UUID NOT NULL,
    event_type VARCHAR(50) NOT NULL,     -- 'TransferCompleted'
    payload TEXT NOT NULL,
    status VARCHAR(20) NOT NULL,        -- 'PENDING', 'PROCESSING', 'PUBLISHED', 'FAILED'
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);

CREATE INDEX idx_outbox_status_created ON outbox_events(status, created_at);
```

### 1.6 `processed_events` (Priority 4)
Guarantees consumer idempotency across message redeliveries.

```sql
CREATE TABLE processed_events (
    event_id UUID PRIMARY KEY,
    event_type VARCHAR(50) NOT NULL,
    consumer_name VARCHAR(50) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status VARCHAR(20) NOT NULL DEFAULT 'SUCCESS'
);
```

---

## 2. Idempotency State Machine

```
              [Client Request with Idempotency-Key]
                                │
                                ▼
                     Check idempotency_keys
                     (user_id, key) in DB Tx
                                │
               ┌────────────────┴────────────────┐
               ▼                                 ▼
          [Key Exists]                     [Key Not Found]
               │                                 │
     ┌─────────┴─────────┐                       ▼
     ▼                   ▼               Insert Record with
Fingerprint Mismatch?   Status?          status='PENDING'
     │                   │                       │
     ▼                   │                       ▼
Reject 422               │               Proceed to Transfer
(Payload Tampering)      │                       │
                         ├─────────────┐         ▼
                         ▼             ▼   Transfer Succeeds?
                    'COMPLETED'    'PENDING'     │
                         │             │    ┌────┴────┐
                         ▼             ▼    ▼         ▼
                   Return Cached   Reject 409  Yes    No
                   Response        (In Flight)  │      │
                                                ▼      ▼
                                           Commit Tx  Rollback
                                           status=    Record
                                           'COMPLETED' Removed
```

---

## 3. Double-Entry Accounting Invariants

1. **Balanced Entries per Transaction**:
   $$\sum_{e \in \text{Tx}} \text{Amount}_{\text{DEBIT}}(e) - \sum_{e \in \text{Tx}} \text{Amount}_{\text{CREDIT}}(e) = 0$$
2. **Conservation of Gold Across Transfers**:
   $$\sum_{w \in \text{Wallets}} \Delta \text{Balance}(w) = 0$$
3. **Wallet Balance Derivation**:
   For any wallet $w$:
   $$\text{Balance}(w) = \sum_{e \in \text{Ledger}(w)} \Big( \mathbf{1}_{\{e.\text{type} = \text{'DEBIT'}\}} \cdot e.\text{amount} - \mathbf{1}_{\{e.\text{type} = \text{'CREDIT'}\}} \cdot e.\text{amount} \Big)$$

---

## 4. RabbitMQ DLX / DLQ Topology

```
┌────────────────────────────────────────────────────────┐
│                   aurumledger.events                   │ (Direct Exchange, Durable)
└───────────────────────────┬────────────────────────────┘
                            │ RoutingKey: "transfer.completed"
                            ▼
┌────────────────────────────────────────────────────────┐
│               aurumledger.wallet_transfers             │ (Durable Queue)
│  x-dead-letter-exchange: "aurumledger.dlx"             │
│  x-dead-letter-routing-key: "transfer.dlq"             │
└──────────────┬──────────────────────────┬──────────────┘
               │                          │
      Ack() (Success / Duplicate)         │ Nack(false, false) (Poison / Max Retries)
               ▼                          ▼
      [Consumer Processed]     ┌────────────────────────┐
                               │    aurumledger.dlx     │ (DLX Exchange)
                               └──────────┬─────────────┘
                                          │ RoutingKey: "transfer.dlq"
                                          ▼
                               ┌────────────────────────┐
                               │ wallet_transfers.dlq   │ (Dead-Letter Queue)
                               └────────────────────────┘
```

