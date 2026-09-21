package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Wallet struct {
	id        uuid.UUID
	playerID  uuid.UUID
	balance   Money
	version   int
	createdAt time.Time
	updatedAt time.Time
}

func NewWallet(id, playerID uuid.UUID, initial Money, now time.Time) (*Wallet, error) {
	if id == uuid.Nil || playerID == uuid.Nil {
		return nil, ErrInvalidWallet
	}
	if initial.IsNegative() {
		return nil, ErrNegativeAmount
	}
	return &Wallet{
		id:        id,
		playerID:  playerID,
		balance:   initial,
		version:   1,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func RehydrateWallet(id, playerID uuid.UUID, balance Money, version int, createdAt, updatedAt time.Time) *Wallet {
	return &Wallet{
		id:        id,
		playerID:  playerID,
		balance:   balance,
		version:   version,
		createdAt: createdAt,
		updatedAt: updatedAt,
	}
}

func (w *Wallet) ID() uuid.UUID        { return w.id }
func (w *Wallet) PlayerID() uuid.UUID  { return w.playerID }
func (w *Wallet) Balance() Money       { return w.balance }
func (w *Wallet) Version() int         { return w.version }
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }

func (w *Wallet) Debit(amount Money) (before Money, err error) {
	if !amount.IsPositive() {
		return Money{}, fmt.Errorf("%w: debit requires positive amount", ErrInvalidMoney)
	}
	if !w.balance.SameCurrency(amount) {
		return Money{}, ErrCurrencyMismatch
	}
	if w.balance.Cents() < amount.Cents() {
		return Money{}, ErrInsufficientFunds
	}
	before = w.balance
	next, err := w.balance.Sub(amount)
	if err != nil {
		return Money{}, err
	}
	w.balance = next
	w.version++
	return before, nil
}

func (w *Wallet) Credit(amount Money) (before Money, err error) {
	if !amount.IsPositive() {
		return Money{}, fmt.Errorf("%w: credit requires positive amount", ErrInvalidMoney)
	}
	if !w.balance.SameCurrency(amount) {
		return Money{}, ErrCurrencyMismatch
	}
	before = w.balance
	next, err := w.balance.Add(amount)
	if err != nil {
		return Money{}, err
	}
	w.balance = next
	w.version++
	return before, nil
}

func (w *Wallet) Touch(now time.Time) {
	w.updatedAt = now
}
