package domain

import "errors"

var (
	ErrWalletNotFound              = errors.New("wallet not found")
	ErrInsufficientFunds           = errors.New("insufficient gold balance")
	ErrInvalidAmount               = errors.New("transfer amount must be strictly positive")
	ErrSelfTransfer                = errors.New("cannot transfer gold to self")
	ErrInternalServerError         = errors.New("internal server error")
	ErrUnauthorizedWallet          = errors.New("unauthorized: authenticated user does not own sender wallet")
	ErrIdempotencyKeyRequired      = errors.New("Idempotency-Key header is required")
	ErrIdempotencyConflict         = errors.New("a transfer with this idempotency key is currently processing")
	ErrIdempotencyPayloadMismatch  = errors.New("idempotency key reused with different request payload")
	ErrLedgerUnbalanced            = errors.New("double-entry ledger imbalance detected: debits do not equal credits")
	ErrWalletFrozen                = errors.New("wallet is frozen or inactive")
	ErrTransactionNotFound         = errors.New("transaction not found")
	ErrInvalidPayload              = errors.New("invalid request payload")
)
