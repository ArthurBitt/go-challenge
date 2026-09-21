package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func CanonicalHash(provider, external, player, wallet, round, game, kind, amount, currency, reference string) string {
	payload := map[string]string{
		"amount":                         amount,
		"currency":                       currency,
		"externalTransactionId":          external,
		"gameId":                         game,
		"kind":                           kind,
		"playerId":                       player,
		"providerId":                     provider,
		"referenceExternalTransactionId": reference,
		"roundId":                        round,
		"walletId":                       wallet,
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
