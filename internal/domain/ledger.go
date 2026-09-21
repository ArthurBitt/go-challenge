package domain

import (
	"time"

	"github.com/google/uuid"
)

type LedgerEntry struct {
	ID            uuid.UUID
	WalletID      uuid.UUID
	TransactionID uuid.UUID
	Direction     Direction
	Amount        Money
	BalanceBefore Money
	BalanceAfter  Money
	CreatedAt     time.Time
}

func NewLedgerEntry(id, walletID, txID uuid.UUID, dir Direction, amount, before, after Money, now time.Time) (LedgerEntry, error) {
	var expected Money
	var err error
	switch dir {
	case DirectionCredit:
		expected, err = before.Add(amount)
	case DirectionDebit:
		expected, err = before.Sub(amount)
	default:
		return LedgerEntry{}, fmtErr("invalid direction")
	}
	if err != nil {
		return LedgerEntry{}, err
	}
	if !expected.Equal(after) {
		return LedgerEntry{}, fmtErr("ledger identity broken")
	}
	return LedgerEntry{
		ID:            id,
		WalletID:      walletID,
		TransactionID: txID,
		Direction:     dir,
		Amount:        amount,
		BalanceBefore: before,
		BalanceAfter:  after,
		CreatedAt:     now,
	}, nil
}

func fmtErr(msg string) error { return &simpleError{msg} }

type simpleError struct{ s string }

func (e *simpleError) Error() string { return e.s }
