package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"junglego/internal/auth"
	"junglego/internal/domain"
	"junglego/internal/postgres"
	"junglego/internal/service"
)

type API struct {
	Svc   *service.Service
	Log   *slog.Logger
	Ready func(r *http.Request) error
}

func NewRouter(a *API, verifier *auth.Verifier) http.Handler {
	r := chi.NewRouter()
	r.Get("/health/live", a.live)
	r.Get("/health/ready", a.ready)

	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.With(auth.RequireInternal).Post("/wallets", a.openWallet)
		r.With(auth.RequireInternal).Get("/wallets/{walletId}", a.getWallet)
		r.With(auth.RequireInternal).Get("/wallets/{walletId}/ledger", a.ledger)
		r.With(auth.RequireInternal).Post("/wallets/{walletId}/reconciliation", a.reconcile)
		r.With(auth.RequireInternal).Get("/wagering/transactions/{transactionId}", a.getTx)
		r.With(auth.RequireProvider).Post("/wagering/transactions", a.postWager)
		r.With(auth.RequireProvider).Get("/providers/{providerId}/wagering/transactions/{externalTransactionId}", a.getByExternal)
	})
	return r
}

func (a *API) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	if a.Ready != nil {
		if err := a.Ready(r); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

type moneyBody struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (a *API) openWallet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID       string    `json:"playerId"`
		InitialBalance moneyBody `json:"initialBalance"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "INVALID_PAYLOAD", "invalid json")
		return
	}
	player, err := uuid.Parse(body.PlayerID)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "playerId")
		return
	}
	m, err := domain.ParseMoney(body.InitialBalance.Amount, body.InitialBalance.Currency)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", err.Error())
		return
	}
	view, err := a.Svc.OpenWallet(r.Context(), service.OpenWalletInput{PlayerID: player, InitialBalance: m})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       view.ID.String(),
		"playerId": view.PlayerID.String(),
		"balance":  view.Balance.Wire(),
		"version":  view.Version,
	})
}

func (a *API) getWallet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "walletId"))
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_PAYLOAD", "walletId")
		return
	}
	view, err := a.Svc.GetWallet(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       view.ID.String(),
		"playerId": view.PlayerID.String(),
		"balance":  view.Balance.Wire(),
		"version":  view.Version,
	})
}

func (a *API) ledger(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "walletId"))
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_PAYLOAD", "walletId")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var after *uuid.UUID
	if c := r.URL.Query().Get("cursor"); c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil {
			fail(w, http.StatusBadRequest, "INVALID_PAYLOAD", "cursor")
			return
		}
		u, err := uuid.Parse(string(raw))
		if err != nil {
			fail(w, http.StatusBadRequest, "INVALID_PAYLOAD", "cursor")
			return
		}
		after = &u
	}
	rows, err := a.Svc.Store.ListLedger(r.Context(), id, after, limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	var next string
	for _, row := range rows {
		amt, _ := domain.FromCents(row.AmountCents, strings.TrimSpace(row.Currency))
		before, _ := domain.FromCents(row.Before, strings.TrimSpace(row.Currency))
		afterBal, _ := domain.FromCents(row.After, strings.TrimSpace(row.Currency))
		items = append(items, map[string]any{
			"id":            row.ID.String(),
			"walletId":      row.WalletID.String(),
			"transactionId": row.TransactionID.String(),
			"direction":     row.Direction,
			"money":         amt.Wire(),
			"balanceBefore": before.Wire(),
			"balanceAfter":  afterBal.Wire(),
			"createdAt":     row.CreatedAt,
		})
		next = base64.RawURLEncoding.EncodeToString([]byte(row.ID.String()))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}

func (a *API) reconcile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "walletId"))
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_PAYLOAD", "walletId")
		return
	}
	res, err := a.Svc.Reconcile(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"walletId":          res.WalletID.String(),
		"storedBalance":     res.StoredBalance.Wire(),
		"calculatedBalance": res.CalculatedBalance.Wire(),
		"difference":        res.Difference.Wire(),
		"consistent":        res.Consistent,
		"checkedEntries":    res.CheckedEntries,
	})
}

func (a *API) postWager(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		fail(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key")
		return
	}
	var body struct {
		ProviderID                     string    `json:"providerId"`
		ExternalTransactionID          string    `json:"externalTransactionId"`
		PlayerID                       string    `json:"playerId"`
		WalletID                       string    `json:"walletId"`
		RoundID                        string    `json:"roundId"`
		GameID                         string    `json:"gameId"`
		Kind                           string    `json:"kind"`
		Money                          moneyBody `json:"money"`
		ReferenceExternalTransactionID string    `json:"referenceExternalTransactionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "INVALID_PAYLOAD", "invalid json")
		return
	}
	if body.ProviderID != "" && body.ProviderID != p.ProviderID {
		fail(w, http.StatusForbidden, "FORBIDDEN", "providerId does not match token")
		return
	}
	kind, err := domain.ParseKind(body.Kind)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "kind")
		return
	}
	player, err := uuid.Parse(body.PlayerID)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "playerId")
		return
	}
	wallet, err := uuid.Parse(body.WalletID)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "walletId")
		return
	}
	m, err := domain.ParseMoney(body.Money.Amount, body.Money.Currency)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", err.Error())
		return
	}
	res, err := a.Svc.ProcessWager(r.Context(), service.ProcessInput{
		ProviderID:                     p.ProviderID,
		ExternalTransactionID:          body.ExternalTransactionID,
		IdempotencyKey:                 key,
		PlayerID:                       player,
		WalletID:                       wallet,
		RoundID:                        body.RoundID,
		GameID:                         body.GameID,
		Kind:                           kind,
		Money:                          m,
		ReferenceExternalTransactionID: body.ReferenceExternalTransactionID,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	status := http.StatusOK
	if res.Status == domain.StatusPendingReference {
		status = http.StatusAccepted
	}
	out := map[string]any{
		"transactionId":    res.TransactionID.String(),
		"status":           res.Status,
		"balance":          res.Balance.Wire(),
		"idempotentReplay": res.IdempotentReplay,
	}
	if res.FailureCode != "" {
		out["failureCode"] = res.FailureCode
	}
	writeJSON(w, status, out)
}

func (a *API) getTx(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "transactionId"))
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_PAYLOAD", "transactionId")
		return
	}
	row, err := a.Svc.GetTransaction(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wagerJSON(row))
}

func (a *API) getByExternal(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	provider := chi.URLParam(r, "providerId")
	if provider != p.ProviderID {
		fail(w, http.StatusForbidden, "FORBIDDEN", "provider mismatch")
		return
	}
	row, err := a.Svc.GetByExternal(r.Context(), provider, chi.URLParam(r, "externalTransactionId"))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wagerJSON(row))
}

func wagerJSON(row postgres.WagerRow) map[string]any {
	m, _ := domain.FromCents(row.AmountCents, strings.TrimSpace(row.Currency))
	out := map[string]any{
		"transactionId": row.ID.String(),
		"kind":          row.Kind,
		"status":        row.Status,
		"walletId":      row.WalletID.String(),
		"playerId":      row.PlayerID.String(),
		"money":         m.Wire(),
	}
	if row.ProviderID != nil {
		out["providerId"] = *row.ProviderID
	}
	if row.ExternalID != nil {
		out["externalTransactionId"] = *row.ExternalID
	}
	if row.FailureCode != nil {
		out["failureCode"] = *row.FailureCode
	}
	if row.ResultBalanceCents != nil {
		bal, _ := domain.FromCents(*row.ResultBalanceCents, strings.TrimSpace(row.Currency))
		out["balance"] = bal.Wire()
	}
	return out
}

func mapErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		fail(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, service.ErrWalletExists):
		fail(w, http.StatusConflict, "WALLET_ALREADY_EXISTS", err.Error())
	case errors.Is(err, service.ErrIdempotencyConflict):
		fail(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", err.Error())
	case errors.Is(err, service.ErrExternalConflict):
		fail(w, http.StatusConflict, "EXTERNAL_TRANSACTION_CONFLICT", err.Error())
	case errors.Is(err, service.ErrValidation):
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", err.Error())
	case errors.Is(err, service.ErrForbidden):
		fail(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	default:
		fail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "unexpected error")
	}
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "message": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
