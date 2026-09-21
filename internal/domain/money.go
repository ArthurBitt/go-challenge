package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrInvalidMoney      = errors.New("invalid money")
	ErrCurrencyMismatch  = errors.New("currency mismatch")
	ErrMoneyOverflow     = errors.New("money overflow")
	ErrNegativeAmount    = errors.New("negative amount not allowed")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrInvalidWallet     = errors.New("invalid wallet")
)

var moneyPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.[0-9]{2}$`)

const maxCents int64 = 9_223_372_036_854_775_807

// Money is an immutable amount in minor units (cents) plus an ISO currency.
type Money struct {
	cents    int64
	currency string
}

func ParseMoney(amount, currency string) (Money, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return Money{}, fmt.Errorf("%w: currency", ErrInvalidMoney)
	}
	amount = strings.TrimSpace(amount)
	if !moneyPattern.MatchString(amount) {
		return Money{}, fmt.Errorf("%w: amount %q", ErrInvalidMoney, amount)
	}
	parts := strings.Split(amount, ".")
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %v", ErrInvalidMoney, err)
	}
	frac, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %v", ErrInvalidMoney, err)
	}
	if whole > (maxCents-frac)/100 {
		return Money{}, ErrMoneyOverflow
	}
	cents := whole*100 + frac
	return Money{cents: cents, currency: currency}, nil
}

func Zero(currency string) (Money, error) {
	return ParseMoney("0.00", currency)
}

func FromCents(cents int64, currency string) (Money, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return Money{}, fmt.Errorf("%w: currency", ErrInvalidMoney)
	}
	return Money{cents: cents, currency: currency}, nil
}

func (m Money) Cents() int64     { return m.cents }
func (m Money) Currency() string { return m.currency }
func (m Money) IsZero() bool     { return m.cents == 0 }
func (m Money) IsNegative() bool { return m.cents < 0 }
func (m Money) IsPositive() bool { return m.cents > 0 }

func (m Money) AmountString() string {
	neg := ""
	cents := m.cents
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", neg, cents/100, cents%100)
}

func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	if other.cents > 0 && m.cents > maxCents-other.cents {
		return Money{}, ErrMoneyOverflow
	}
	if other.cents < 0 && m.cents < -maxCents-other.cents {
		return Money{}, ErrMoneyOverflow
	}
	return Money{cents: m.cents + other.cents, currency: m.currency}, nil
}

func (m Money) Sub(other Money) (Money, error) {
	neg, err := other.Negate()
	if err != nil {
		return Money{}, err
	}
	return m.Add(neg)
}

func (m Money) Negate() (Money, error) {
	if m.cents == -maxCents-1 { // impossible for our stored range except math.MinInt64
		return Money{}, ErrMoneyOverflow
	}
	return Money{cents: -m.cents, currency: m.currency}, nil
}

func (m Money) Equal(other Money) bool {
	return m.currency == other.currency && m.cents == other.cents
}

func (m Money) SameCurrency(other Money) bool {
	return m.currency == other.currency
}

func (m Money) Wire() map[string]string {
	return map[string]string{"amount": m.AmountString(), "currency": m.currency}
}
