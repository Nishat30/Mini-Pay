// Package ledger implements double-entry bookkeeping, account management, fee calculation,
// and financial invariant enforcement for MiniPay.
package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/minipay/minipay/internal/platform"
	"github.com/minipay/minipay/internal/platform/db"
)

// Account kinds supported by MiniPay.
const (
	KindProcessorClearing = "processor_clearing"
	KindMerchantPayable   = "merchant_payable"
	KindPlatformFees      = "platform_fees"

	DirectionDebit  = "debit"
	DirectionCredit = "credit"

	// Default fee configuration: 2.9% + $0.30 (290 basis points + 30 minor units)
	DefaultFeeBasisPoints = 290
	DefaultFeeFixedAmount = 30
)

var (
	ErrFeeExceedsAmount = errors.New("calculated fee exceeds payment amount")
	ErrUnbalancedEntry  = errors.New("ledger entries are unbalanced")
	ErrInvalidCurrency  = errors.New("currency mismatch in ledger entry")
)

// Entry defines a single ledger entry.
type Entry struct {
	AccountID string
	Direction string
	Amount    int64
	Currency  string
}

// Accounts holds the resolved account IDs for a transaction.
type Accounts struct {
	ClearingAccountID string
	MerchantAccountID string
	PlatformFeesID    string
}

// CalculateFee computes the transaction fee: floor(amount * basisPoints / 10000) + fixedAmount.
// It returns an error if the fee is negative or exceeds the amount.
func CalculateFee(amount int64, basisPoints int64, fixedAmount int64) (int64, error) {
	if amount <= 0 {
		return 0, errors.New("amount must be positive")
	}
	fee := (amount * basisPoints / 10000) + fixedAmount
	if fee < 0 {
		return 0, errors.New("fee cannot be negative")
	}
	if fee >= amount {
		return 0, fmt.Errorf("%w: fee %d >= amount %d", ErrFeeExceedsAmount, fee, amount)
	}
	return fee, nil
}

// CaptureEntries generates double-entry records for a payment capture:
// - Debit: Processor Clearing (full amount)
// - Credit: Merchant Payable (net = amount - fee)
// - Credit: Platform Fees (fee)
//
// By definition: Debits (amount) == Credits (net + fee = amount).
func CaptureEntries(accounts Accounts, currency string, amount, fee int64) ([]Entry, error) {
	if amount <= 0 {
		return nil, errors.New("amount must be positive")
	}
	if fee < 0 || fee >= amount {
		return nil, ErrFeeExceedsAmount
	}

	netAmount := amount - fee

	entries := []Entry{
		{
			AccountID: accounts.ClearingAccountID,
			Direction: DirectionDebit,
			Amount:    amount,
			Currency:  currency,
		},
		{
			AccountID: accounts.MerchantAccountID,
			Direction: DirectionCredit,
			Amount:    netAmount,
			Currency:  currency,
		},
		{
			AccountID: accounts.PlatformFeesID,
			Direction: DirectionCredit,
			Amount:    fee,
		},
	}
	entries[2].Currency = currency

	// Verify balance assertion in memory before returning
	var debits, credits int64
	for _, e := range entries {
		if e.Currency != currency {
			return nil, ErrInvalidCurrency
		}
		if e.Direction == DirectionDebit {
			debits += e.Amount
		} else if e.Direction == DirectionCredit {
			credits += e.Amount
		}
	}
	if debits != credits {
		return nil, fmt.Errorf("%w: debits %d != credits %d", ErrUnbalancedEntry, debits, credits)
	}

	return entries, nil
}

// RefundEntries generates double-entry records for a full or partial refund.
// For MiniPay Phase 1, the merchant refunds the principal and MiniPay retains processing fees.
// - Debit: Merchant Payable (refundAmount)
// - Credit: Processor Clearing (refundAmount)
func RefundEntries(accounts Accounts, currency string, refundAmount int64) ([]Entry, error) {
	if refundAmount <= 0 {
		return nil, errors.New("refund amount must be positive")
	}

	entries := []Entry{
		{
			AccountID: accounts.MerchantAccountID,
			Direction: DirectionDebit,
			Amount:    refundAmount,
			Currency:  currency,
		},
		{
			AccountID: accounts.ClearingAccountID,
			Direction: DirectionCredit,
			Amount:    refundAmount,
			Currency:  currency,
		},
	}

	return entries, nil
}

// Service provides database ledger operations.
type Service struct {
	queries *db.Queries
}

// NewService creates a new ledger service.
func NewService(queries *db.Queries) *Service {
	return &Service{queries: queries}
}

// EnsureAccounts retrieves or creates the system clearing, merchant payable, and platform fee accounts.
func (s *Service) EnsureAccounts(ctx context.Context, merchantID string, currency string) (Accounts, error) {
	// 1. Processor clearing account (system-level, merchant_id is NULL)
	clearing, err := s.queries.GetAccount(ctx, db.GetAccountParams{
		MerchantID: pgtype.Text{Valid: false},
		Kind:       KindProcessorClearing,
		Currency:   currency,
	})
	if err != nil {
		clearingID := platform.NewID(platform.PrefixLedgerAccount)
		_, err = s.queries.CreateAccountLazy(ctx, db.CreateAccountLazyParams{
			ID:         clearingID,
			MerchantID: pgtype.Text{Valid: false},
			Kind:       KindProcessorClearing,
			Currency:   currency,
		})
		if err != nil {
			return Accounts{}, fmt.Errorf("failed to create processor clearing account: %w", err)
		}
		clearing, err = s.queries.GetAccount(ctx, db.GetAccountParams{
			MerchantID: pgtype.Text{Valid: false},
			Kind:       KindProcessorClearing,
			Currency:   currency,
		})
		if err != nil {
			return Accounts{}, fmt.Errorf("failed to retrieve processor clearing account: %w", err)
		}
	}

	// 2. Platform fees account (system-level, merchant_id is NULL)
	fees, err := s.queries.GetAccount(ctx, db.GetAccountParams{
		MerchantID: pgtype.Text{Valid: false},
		Kind:       KindPlatformFees,
		Currency:   currency,
	})
	if err != nil {
		feesID := platform.NewID(platform.PrefixLedgerAccount)
		_, err = s.queries.CreateAccountLazy(ctx, db.CreateAccountLazyParams{
			ID:         feesID,
			MerchantID: pgtype.Text{Valid: false},
			Kind:       KindPlatformFees,
			Currency:   currency,
		})
		if err != nil {
			return Accounts{}, fmt.Errorf("failed to create platform fees account: %w", err)
		}
		fees, err = s.queries.GetAccount(ctx, db.GetAccountParams{
			MerchantID: pgtype.Text{Valid: false},
			Kind:       KindPlatformFees,
			Currency:   currency,
		})
		if err != nil {
			return Accounts{}, fmt.Errorf("failed to retrieve platform fees account: %w", err)
		}
	}

	// 3. Merchant payable account (merchant-specific)
	merchantAcc, err := s.queries.GetAccount(ctx, db.GetAccountParams{
		MerchantID: pgtype.Text{String: merchantID, Valid: true},
		Kind:       KindMerchantPayable,
		Currency:   currency,
	})
	if err != nil {
		accID := platform.NewID(platform.PrefixLedgerAccount)
		_, err = s.queries.CreateAccountLazy(ctx, db.CreateAccountLazyParams{
			ID:         accID,
			MerchantID: pgtype.Text{String: merchantID, Valid: true},
			Kind:       KindMerchantPayable,
			Currency:   currency,
		})
		if err != nil {
			return Accounts{}, fmt.Errorf("failed to create merchant payable account: %w", err)
		}
		merchantAcc, err = s.queries.GetAccount(ctx, db.GetAccountParams{
			MerchantID: pgtype.Text{String: merchantID, Valid: true},
			Kind:       KindMerchantPayable,
			Currency:   currency,
		})
		if err != nil {
			return Accounts{}, fmt.Errorf("failed to retrieve merchant payable account: %w", err)
		}
	}

	return Accounts{
		ClearingAccountID: clearing.ID,
		MerchantAccountID: merchantAcc.ID,
		PlatformFeesID:    fees.ID,
	}, nil
}

// PostCapture creates a capture ledger transaction and inserts balanced ledger entries.
// It is strictly idempotent via reference: "capture:<payment_id>".
func (s *Service) PostCapture(ctx context.Context, txQueries *db.Queries, paymentID, merchantID, currency string, amount int64) error {
	ref := fmt.Sprintf("capture:%s", paymentID)

	// Check if already posted
	_, err := txQueries.GetLedgerTransactionByRef(ctx, ref)
	if err == nil {
		// Already posted, idempotent success
		return nil
	}

	accounts, err := s.EnsureAccounts(ctx, merchantID, currency)
	if err != nil {
		return err
	}

	fee, err := CalculateFee(amount, DefaultFeeBasisPoints, DefaultFeeFixedAmount)
	if err != nil {
		return err
	}

	entries, err := CaptureEntries(accounts, currency, amount, fee)
	if err != nil {
		return err
	}

	txID := platform.NewID(platform.PrefixLedgerTransaction)
	txRow, err := txQueries.CreateLedgerTransaction(ctx, db.CreateLedgerTransactionParams{
		ID:        txID,
		PaymentID: paymentID,
		Kind:      "capture",
		Reference: ref,
	})
	if err != nil {
		return fmt.Errorf("failed to create ledger transaction: %w", err)
	}

	for _, entry := range entries {
		_, err = txQueries.CreateLedgerEntry(ctx, db.CreateLedgerEntryParams{
			TransactionID: txRow.ID,
			AccountID:     entry.AccountID,
			Direction:     entry.Direction,
			Amount:        entry.Amount,
			Currency:      entry.Currency,
		})
		if err != nil {
			return fmt.Errorf("failed to insert ledger entry: %w", err)
		}
	}

	return nil
}

// InvariantViolation describes an integrity check failure in the ledger.
type InvariantViolation struct {
	Check       string
	Description string
}

// CheckInvariants performs cross-cutting integrity assertions across all ledger records:
// 1. Transaction balance (sum debits == sum credits)
// 2. Currency match (entries match account currency)
func (s *Service) CheckInvariants(ctx context.Context) ([]InvariantViolation, error) {
	var violations []InvariantViolation

	// 1. Check for unbalanced transactions
	unbalanced, err := s.queries.CheckUnbalancedTransactions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check unbalanced transactions: %w", err)
	}
	for _, u := range unbalanced {
		violations = append(violations, InvariantViolation{
			Check:       "transaction_balance",
			Description: fmt.Sprintf("transaction %s is unbalanced with diff %d", u.TransactionID, u.Diff),
		})
	}

	// 2. Check for currency mismatches
	mismatches, err := s.queries.CheckCurrencyMismatchEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check currency mismatches: %w", err)
	}
	for _, m := range mismatches {
		violations = append(violations, InvariantViolation{
			Check:       "currency_consistency",
			Description: fmt.Sprintf("entry %d in tx %s has currency %s != account %s", m.ID, m.TransactionID, m.EntryCurrency, m.AccountCurrency),
		})
	}

	return violations, nil
}
