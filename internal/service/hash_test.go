package service

import "testing"

func TestCanonicalHashStable(t *testing.T) {
	a := CanonicalHash("provider-a", "tx-1", "p", "w", "r", "g", "BET", "25.00", "BRL", "")
	b := CanonicalHash("provider-a", "tx-1", "p", "w", "r", "g", "BET", "25.00", "BRL", "")
	c := CanonicalHash("provider-a", "tx-1", "p", "w", "r", "g", "BET", "25.01", "BRL", "")
	if a != b {
		t.Fatal("hash should be stable")
	}
	if a == c {
		t.Fatal("different amount must change hash")
	}
}
