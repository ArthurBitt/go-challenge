package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseMoney(t *testing.T) {
	m, err := ParseMoney("25.00", "brl")
	if err != nil {
		t.Fatal(err)
	}
	if m.Cents() != 2500 || m.Currency() != "BRL" || m.AmountString() != "25.00" {
		t.Fatalf("got %+v %s", m, m.AmountString())
	}
}

func TestParseMoneyRejectsInvalid(t *testing.T) {
	cases := []string{"", "25", "25.0", "25.000", "1e2", "NaN", "-1.00", "01.00", "+1.00"}
	for _, c := range cases {
		if _, err := ParseMoney(c, "BRL"); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestMoneyCurrencyMismatch(t *testing.T) {
	a, _ := ParseMoney("1.00", "BRL")
	b, _ := ParseMoney("1.00", "USD")
	if _, err := a.Add(b); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestWalletDebitInsufficient(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	player := uuid.Must(uuid.NewV7())
	bal, _ := ParseMoney("10.00", "BRL")
	w, err := NewWallet(id, player, bal, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	debit, _ := ParseMoney("10.01", "BRL")
	if _, err := w.Debit(debit); err == nil {
		t.Fatal("expected insufficient funds")
	}
}

func TestWalletDebitAndCredit(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	player := uuid.Must(uuid.NewV7())
	bal, _ := ParseMoney("100.00", "BRL")
	w, err := NewWallet(id, player, bal, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if w.Version() != 1 {
		t.Fatalf("version %d", w.Version())
	}
	debit, _ := ParseMoney("80.00", "BRL")
	before, err := w.Debit(debit)
	if err != nil {
		t.Fatal(err)
	}
	if before.AmountString() != "100.00" || w.Balance().AmountString() != "20.00" || w.Version() != 2 {
		t.Fatalf("after debit %+v v=%d", w.Balance(), w.Version())
	}
}

func TestWagerRejectsOpeningFromExternal(t *testing.T) {
	if err := ValidateExternalKind(KindOpening); err == nil {
		t.Fatal("expected error")
	}
}

func TestLOSSRequiresZero(t *testing.T) {
	m, _ := ParseMoney("1.00", "BRL")
	if err := ValidateKindAmount(KindLoss, m); err == nil {
		t.Fatal("expected error")
	}
	z, _ := ParseMoney("0.00", "BRL")
	if err := ValidateKindAmount(KindLoss, z); err != nil {
		t.Fatal(err)
	}
}
