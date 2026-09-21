package domain

import "fmt"

type Kind string

const (
	KindOpening  Kind = "OPENING"
	KindBet      Kind = "BET"
	KindWin      Kind = "WIN"
	KindLoss     Kind = "LOSS"
	KindRefund   Kind = "REFUND"
	KindRollback Kind = "ROLLBACK"
)

type Status string

const (
	StatusPending          Status = "PENDING"
	StatusPendingReference Status = "PENDING_REFERENCE"
	StatusProcessed        Status = "PROCESSED"
	StatusRejected         Status = "REJECTED"
	StatusFailed           Status = "FAILED"
)

type Origin string

const (
	OriginInternal Origin = "INTERNAL"
	OriginExternal Origin = "EXTERNAL"
)

type Direction string

const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

const (
	FailureInsufficientFunds         = "INSUFFICIENT_FUNDS"
	FailureReversalInsufficientFunds = "REVERSAL_INSUFFICIENT_FUNDS"
	FailureReferenceNotFound         = "REFERENCE_NOT_FOUND"
	FailureReferenceNotProcessed     = "REFERENCE_NOT_PROCESSED"
	FailureReferenceMismatch         = "REFERENCE_MISMATCH"
	FailureAlreadyReversed           = "ALREADY_REVERSED"
	FailureInvalidKind               = "INVALID_KIND"
	FailureWalletNotFound            = "WALLET_NOT_FOUND"
	FailurePlayerMismatch            = "PLAYER_MISMATCH"
)

func ParseKind(s string) (Kind, error) {
	switch Kind(s) {
	case KindBet, KindWin, KindLoss, KindRefund, KindRollback, KindOpening:
		return Kind(s), nil
	default:
		return "", fmt.Errorf("%w: kind %s", ErrInvalidMoney, s)
	}
}

func ValidateExternalKind(k Kind) error {
	if k == KindOpening {
		return fmt.Errorf("%w: OPENING is internal", ErrInvalidKind)
	}
	switch k {
	case KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return nil
	default:
		return ErrInvalidKind
	}
}

var ErrInvalidKind = fmt.Errorf("invalid kind")

func ValidateKindAmount(k Kind, m Money) error {
	switch k {
	case KindLoss:
		if !m.IsZero() {
			return fmt.Errorf("%w: LOSS requires 0.00", ErrInvalidMoney)
		}
	case KindBet, KindWin, KindRefund, KindRollback, KindOpening:
		if !m.IsPositive() && k != KindOpening {
			return fmt.Errorf("%w: %s requires amount > 0", ErrInvalidMoney, k)
		}
		if k == KindOpening && m.IsNegative() {
			return ErrNegativeAmount
		}
	}
	return nil
}

func NeedsReference(k Kind) bool {
	return k == KindRefund || k == KindRollback
}
