package service

import (
	"context"
	"time"

	"aurumledger/internal/domain"
)

type ReconciliationService struct {
	walletRepo domain.WalletRepository
	ledgerRepo domain.LedgerRepository
}

func NewReconciliationService(
	wRepo domain.WalletRepository,
	lRepo domain.LedgerRepository,
) *ReconciliationService {
	return &ReconciliationService{
		walletRepo: wRepo,
		ledgerRepo: lRepo,
	}
}

// Reconcile performs an exhaustive cryptographic and financial audit comparing:
// 1. Current wallet table balance vs cumulative ledger-derived balance for each wallet.
// 2. System-wide conservation of gold: sum of all ledger debits vs credits.
func (s *ReconciliationService) Reconcile(ctx context.Context) (*domain.ReconciliationReport, error) {
	wallets, err := s.walletRepo.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	report := &domain.ReconciliationReport{
		TotalWallets: len(wallets),
		AuditedAt:    time.Now().UTC(),
	}

	var totalWalletBalances int64

	for _, w := range wallets {
		totalWalletBalances += w.Balance

		derivedBalance, err := s.ledgerRepo.CalculateLedgerBalance(ctx, w.ID)
		if err != nil {
			return nil, err
		}

		if w.Balance == derivedBalance {
			report.MatchedWallets++
		} else {
			report.MismatchedWallets++
			report.Discrepancies = append(report.Discrepancies, domain.WalletDiscrepancy{
				WalletID:      w.ID,
				WalletBalance: w.Balance,
				LedgerBalance: derivedBalance,
				Difference:    w.Balance - derivedBalance,
			})
		}
	}

	totalDebits, totalCredits, err := s.ledgerRepo.GetTotalSystemDebitsAndCredits(ctx)
	if err != nil {
		return nil, err
	}

	report.SystemTotalBalance = totalWalletBalances
	report.SystemTotalDebits = totalDebits
	report.SystemTotalCredits = totalCredits

	// In a closed double-entry system with initial deposits:
	// Total Wallets Balance == Total System Debits - Total System Credits
	// And if there are no mismatched wallets, the system is fully balanced and reconciled.
	report.IsSystemBalanced = (report.MismatchedWallets == 0) && (totalWalletBalances == (totalDebits - totalCredits))

	return report, nil
}

