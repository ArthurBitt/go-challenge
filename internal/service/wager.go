package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"junglego/internal/domain"
	"junglego/internal/postgres"
)

const ConsumerName = "wager-ingress"

type Service struct {
	Store                *postgres.Store
	Log                  *slog.Logger
	Now                  func() time.Time
	ReferenceMaxAttempts int
}

func New(store *postgres.Store, log *slog.Logger) *Service {
	return &Service{
		Store:                store,
		Log:                  log,
		Now:                  func() time.Time { return time.Now().UTC() },
		ReferenceMaxAttempts: 10,
	}
}

type OpenWalletInput struct {
	PlayerID       uuid.UUID
	InitialBalance domain.Money
}

type WalletView struct {
	ID       uuid.UUID
	PlayerID uuid.UUID
	Balance  domain.Money
	Version  int
}

func (s *Service) OpenWallet(ctx context.Context, in OpenWalletInput) (WalletView, error) {
	now := s.Now()
	id, err := uuid.NewV7()
	if err != nil {
		return WalletView{}, err
	}
	w, err := domain.NewWallet(id, in.PlayerID, in.InitialBalance, now)
	if err != nil {
		return WalletView{}, err
	}
	tx, err := s.Store.Begin(ctx)
	if err != nil {
		return WalletView{}, err
	}
	defer tx.Rollback(ctx)

	if err := s.Store.InsertWallet(ctx, tx, w); err != nil {
		if postgres.IsUniqueViolation(err) {
			return WalletView{}, ErrWalletExists
		}
		return WalletView{}, err
	}

	if in.InitialBalance.IsPositive() {
		txID, err := uuid.NewV7()
		if err != nil {
			return WalletView{}, err
		}
		row := postgres.WagerRow{
			ID:                 txID,
			Origin:             domain.OriginInternal,
			Kind:               domain.KindOpening,
			Status:             domain.StatusProcessed,
			WalletID:           w.ID(),
			PlayerID:           w.PlayerID(),
			AmountCents:        in.InitialBalance.Cents(),
			Currency:           in.InitialBalance.Currency(),
			ResultBalanceCents: ptrInt(in.InitialBalance.Cents()),
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := s.Store.InsertWager(ctx, tx, row); err != nil {
			return WalletView{}, err
		}
		ledgerID, err := uuid.NewV7()
		if err != nil {
			return WalletView{}, err
		}
		zero, _ := domain.Zero(in.InitialBalance.Currency())
		entry, err := domain.NewLedgerEntry(ledgerID, w.ID(), txID, domain.DirectionCredit, in.InitialBalance, zero, in.InitialBalance, now)
		if err != nil {
			return WalletView{}, err
		}
		if err := s.Store.InsertLedger(ctx, tx, entry); err != nil {
			return WalletView{}, err
		}
		if err := s.emitProcessed(ctx, tx, row, w, &entry); err != nil {
			return WalletView{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return WalletView{}, err
	}
	return WalletView{ID: w.ID(), PlayerID: w.PlayerID(), Balance: w.Balance(), Version: w.Version()}, nil
}

func (s *Service) GetWallet(ctx context.Context, id uuid.UUID) (WalletView, error) {
	w, err := s.Store.GetWallet(ctx, id)
	if err != nil {
		return WalletView{}, err
	}
	if w == nil {
		return WalletView{}, ErrNotFound
	}
	return WalletView{ID: w.ID(), PlayerID: w.PlayerID(), Balance: w.Balance(), Version: w.Version()}, nil
}

type ProcessInput struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       uuid.UUID
	WalletID                       uuid.UUID
	RoundID                        string
	GameID                         string
	Kind                           domain.Kind
	Money                          domain.Money
	ReferenceExternalTransactionID string
	MessageID                      string
	MessageDigest                  string
}

type ProcessResult struct {
	TransactionID    uuid.UUID
	Status           domain.Status
	FailureCode      string
	Balance          domain.Money
	IdempotentReplay bool
}

func (s *Service) ProcessWager(ctx context.Context, in ProcessInput) (ProcessResult, error) {
	if err := domain.ValidateExternalKind(in.Kind); err != nil {
		return ProcessResult{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := domain.ValidateKindAmount(in.Kind, in.Money); err != nil {
		return ProcessResult{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if domain.NeedsReference(in.Kind) && in.ReferenceExternalTransactionID == "" {
		return ProcessResult{}, fmt.Errorf("%w: reference required", ErrValidation)
	}

	hash := CanonicalHash(
		in.ProviderID, in.ExternalTransactionID, in.PlayerID.String(), in.WalletID.String(),
		in.RoundID, in.GameID, string(in.Kind), in.Money.AmountString(), in.Money.Currency(),
		in.ReferenceExternalTransactionID,
	)

	tx, err := s.Store.Begin(ctx)
	if err != nil {
		return ProcessResult{}, err
	}
	defer tx.Rollback(ctx)

	if in.MessageID != "" {
		if err := s.Store.InsertInbox(ctx, tx, ConsumerName, in.MessageID, in.MessageDigest); err != nil {
			if postgres.IsUniqueViolation(err) {
				existing, ok, gerr := s.Store.GetInbox(ctx, tx, ConsumerName, in.MessageID)
				if gerr != nil {
					return ProcessResult{}, gerr
				}
				if ok && existing != in.MessageDigest {
					return ProcessResult{}, ErrInboxConflict
				}
				got, gerr := s.Store.GetWagerByIdempotencyTx(ctx, tx, in.ProviderID, in.IdempotencyKey)
				if gerr != nil {
					return ProcessResult{}, gerr
				}
				if got.ID != uuid.Nil {
					if err := tx.Commit(ctx); err != nil {
						return ProcessResult{}, err
					}
					return resultFromRow(got, true)
				}
				if err := tx.Commit(ctx); err != nil {
					return ProcessResult{}, err
				}
				return ProcessResult{}, nil
			}
			return ProcessResult{}, err
		}
	}

	existing, err := s.Store.GetWagerByIdempotencyTx(ctx, tx, in.ProviderID, in.IdempotencyKey)
	if err != nil {
		return ProcessResult{}, err
	}
	if existing.ID != uuid.Nil {
		if existing.PayloadHash == nil || *existing.PayloadHash != hash {
			return ProcessResult{}, ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return ProcessResult{}, err
		}
		return resultFromRow(existing, true)
	}

	byExt, err := s.Store.GetWagerByExternalTx(ctx, tx, in.ProviderID, in.ExternalTransactionID)
	if err != nil {
		return ProcessResult{}, err
	}
	if byExt.ID != uuid.Nil {
		// Same payload already committed under this external id (concurrent replay).
		if byExt.PayloadHash != nil && *byExt.PayloadHash == hash {
			if err := tx.Commit(ctx); err != nil {
				return ProcessResult{}, err
			}
			return resultFromRow(byExt, true)
		}
		return ProcessResult{}, ErrExternalConflict
	}

	now := s.Now()
	txID, err := uuid.NewV7()
	if err != nil {
		return ProcessResult{}, err
	}
	row := postgres.WagerRow{
		ID:                  txID,
		Origin:              domain.OriginExternal,
		Kind:                in.Kind,
		Status:              domain.StatusPending,
		ProviderID:          ptrStr(in.ProviderID),
		ExternalID:          ptrStr(in.ExternalTransactionID),
		IdempotencyKey:      ptrStr(in.IdempotencyKey),
		PayloadHash:         ptrStr(hash),
		WalletID:            in.WalletID,
		PlayerID:            in.PlayerID,
		RoundID:             ptrStr(in.RoundID),
		GameID:              ptrStr(in.GameID),
		AmountCents:         in.Money.Cents(),
		Currency:            in.Money.Currency(),
		ReferenceExternalID: nilIfEmpty(in.ReferenceExternalTransactionID),
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	res, err := s.applyLocked(ctx, tx, &row, false)
	if err != nil {
		return ProcessResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProcessResult{}, err
	}
	return res, nil
}

func (s *Service) ResumePending(ctx context.Context, id uuid.UUID) error {
	tx, err := s.Store.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `SELECT `+wagerSelect+` FROM wager_transactions WHERE id=$1 FOR UPDATE`, id)
	existing, err := scanWagerRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if existing.Status != domain.StatusPendingReference {
		return tx.Commit(ctx)
	}
	existing.Attempts++
	_, err = s.applyLocked(ctx, tx, &existing, true)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const wagerSelect = `id, origin, kind, status, provider_id, external_transaction_id, idempotency_key, payload_hash,
wallet_id, player_id, round_id, game_id, amount_cents, currency, reference_external_id, reference_transaction_id,
failure_code, result_balance_cents, attempts, created_at, updated_at`

func scanWagerRow(row pgx.Row) (postgres.WagerRow, error) {
	var r postgres.WagerRow
	err := row.Scan(
		&r.ID, &r.Origin, &r.Kind, &r.Status,
		&r.ProviderID, &r.ExternalID, &r.IdempotencyKey, &r.PayloadHash,
		&r.WalletID, &r.PlayerID, &r.RoundID, &r.GameID,
		&r.AmountCents, &r.Currency, &r.ReferenceExternalID, &r.ReferenceTransactionID,
		&r.FailureCode, &r.ResultBalanceCents, &r.Attempts, &r.CreatedAt, &r.UpdatedAt,
	)
	return r, err
}

func (s *Service) applyLocked(ctx context.Context, tx pgx.Tx, row *postgres.WagerRow, resume bool) (ProcessResult, error) {
	now := s.Now()
	row.UpdatedAt = now

	w, err := s.Store.LockWallet(ctx, tx, row.WalletID)
	if err != nil {
		return ProcessResult{}, err
	}
	if w == nil {
		return s.reject(ctx, tx, row, w, domain.FailureWalletNotFound, resume)
	}
	if w.PlayerID() != row.PlayerID {
		return s.reject(ctx, tx, row, w, domain.FailurePlayerMismatch, resume)
	}
	if w.Balance().Currency() != row.Currency {
		return s.reject(ctx, tx, row, w, domain.FailureReferenceMismatch, resume)
	}

	amount, err := domain.FromCents(row.AmountCents, row.Currency)
	if err != nil {
		return ProcessResult{}, err
	}

	var lookupKind domain.Kind
	if domain.NeedsReference(row.Kind) || (row.Kind == domain.KindWin && row.ReferenceExternalID != nil && *row.ReferenceExternalID != "") {
		refExt := ""
		if row.ReferenceExternalID != nil {
			refExt = *row.ReferenceExternalID
		}
		if refExt != "" {
			ref, err := s.Store.GetWagerByExternalTx(ctx, tx, deref(row.ProviderID), refExt)
			if err != nil {
				return ProcessResult{}, err
			}
			if ref.ID == uuid.Nil {
				if !resume && row.Attempts == 0 {
					return s.pendingReference(ctx, tx, row, w)
				}
				if row.Attempts >= s.ReferenceMaxAttempts {
					return s.reject(ctx, tx, row, w, domain.FailureReferenceNotFound, resume)
				}
				return s.pendingReference(ctx, tx, row, w)
			}
			if ref.Status == domain.StatusPendingReference {
				if row.Attempts >= s.ReferenceMaxAttempts {
					return s.reject(ctx, tx, row, w, domain.FailureReferenceNotFound, resume)
				}
				return s.pendingReference(ctx, tx, row, w)
			}
			if ref.Status != domain.StatusProcessed {
				return s.reject(ctx, tx, row, w, domain.FailureReferenceNotProcessed, resume)
			}
			if err := matchReference(row, ref); err != nil {
				return s.reject(ctx, tx, row, w, domain.FailureReferenceMismatch, resume)
			}
			row.ReferenceTransactionID = &ref.ID
			lookupKind = ref.Kind

			if row.Kind == domain.KindRefund && ref.Kind != domain.KindBet {
				return s.reject(ctx, tx, row, w, domain.FailureReferenceMismatch, resume)
			}
			if row.Kind == domain.KindRollback && ref.Kind != domain.KindBet && ref.Kind != domain.KindWin && ref.Kind != domain.KindRefund {
				return s.reject(ctx, tx, row, w, domain.FailureReferenceMismatch, resume)
			}
			if row.AmountCents != ref.AmountCents {
				return s.reject(ctx, tx, row, w, domain.FailureReferenceMismatch, resume)
			}

			if reversed, err := alreadyReversed(ctx, tx, s, ref, row.Kind); err != nil {
				return ProcessResult{}, err
			} else if reversed {
				return s.reject(ctx, tx, row, w, domain.FailureAlreadyReversed, resume)
			}
		}
	}

	var (
		entry   *domain.LedgerEntry
		changed bool
		refKind = lookupKind
	)

	switch row.Kind {
	case domain.KindBet:
		before, err := w.Debit(amount)
		if err != nil {
			if errors.Is(err, domain.ErrInsufficientFunds) {
				return s.reject(ctx, tx, row, w, domain.FailureInsufficientFunds, resume)
			}
			return ProcessResult{}, err
		}
		e, err := s.ledger(row.ID, w, domain.DirectionDebit, amount, before)
		if err != nil {
			return ProcessResult{}, err
		}
		entry = &e
		changed = true
	case domain.KindWin:
		before, err := w.Credit(amount)
		if err != nil {
			return ProcessResult{}, err
		}
		e, err := s.ledger(row.ID, w, domain.DirectionCredit, amount, before)
		if err != nil {
			return ProcessResult{}, err
		}
		entry = &e
		changed = true
	case domain.KindLoss:
		// no money movement
	case domain.KindRefund:
		before, err := w.Credit(amount)
		if err != nil {
			return ProcessResult{}, err
		}
		e, err := s.ledger(row.ID, w, domain.DirectionCredit, amount, before)
		if err != nil {
			return ProcessResult{}, err
		}
		entry = &e
		changed = true
	case domain.KindRollback:
		dir := domain.DirectionDebit
		var before domain.Money
		switch refKind {
		case domain.KindBet:
			before, err = w.Credit(amount)
			dir = domain.DirectionCredit
		case domain.KindWin, domain.KindRefund:
			before, err = w.Debit(amount)
			dir = domain.DirectionDebit
		default:
			return s.reject(ctx, tx, row, w, domain.FailureReferenceMismatch, resume)
		}
		if err != nil {
			if errors.Is(err, domain.ErrInsufficientFunds) {
				return s.reject(ctx, tx, row, w, domain.FailureReversalInsufficientFunds, resume)
			}
			return ProcessResult{}, err
		}
		e, err := s.ledger(row.ID, w, dir, amount, before)
		if err != nil {
			return ProcessResult{}, err
		}
		entry = &e
		changed = true
	default:
		return s.reject(ctx, tx, row, w, domain.FailureInvalidKind, resume)
	}

	w.Touch(now)
	row.Status = domain.StatusProcessed
	row.FailureCode = nil
	cents := w.Balance().Cents()
	row.ResultBalanceCents = &cents

	if resume {
		if err := s.Store.UpdateWager(ctx, tx, *row); err != nil {
			return ProcessResult{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `SAVEPOINT before_wager_insert`); err != nil {
			return ProcessResult{}, err
		}
		if err := s.Store.InsertWager(ctx, tx, *row); err != nil {
			if postgres.IsUniqueViolation(err) {
				if _, rerr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT before_wager_insert`); rerr != nil {
					return ProcessResult{}, rerr
				}
				existing, gerr := s.Store.GetWagerByIdempotencyTx(ctx, tx, deref(row.ProviderID), deref(row.IdempotencyKey))
				if gerr != nil {
					return ProcessResult{}, gerr
				}
				if existing.ID != uuid.Nil && existing.PayloadHash != nil && row.PayloadHash != nil && *existing.PayloadHash == *row.PayloadHash {
					return resultFromRow(existing, true)
				}
				byExt, gerr := s.Store.GetWagerByExternalTx(ctx, tx, deref(row.ProviderID), deref(row.ExternalID))
				if gerr != nil {
					return ProcessResult{}, gerr
				}
				if byExt.ID != uuid.Nil && byExt.PayloadHash != nil && row.PayloadHash != nil && *byExt.PayloadHash == *row.PayloadHash {
					return resultFromRow(byExt, true)
				}
			}
			return ProcessResult{}, err
		}
	}
	if changed {
		if err := s.Store.UpdateWallet(ctx, tx, w); err != nil {
			return ProcessResult{}, err
		}
		if entry != nil {
			if err := s.Store.InsertLedger(ctx, tx, *entry); err != nil {
				return ProcessResult{}, err
			}
		}
	}
	if err := s.emitProcessed(ctx, tx, *row, w, entry); err != nil {
		return ProcessResult{}, err
	}
	return resultFromRow(*row, false)
}

func alreadyReversed(ctx context.Context, tx pgx.Tx, s *Service, ref postgres.WagerRow, incoming domain.Kind) (bool, error) {
	// Look for a processed reversal of the same kind pointing at ref.
	var n int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM wager_transactions
		WHERE reference_transaction_id=$1 AND kind=$2 AND status='PROCESSED'`,
		ref.ID, incoming).Scan(&n)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	// A BET consumed by either refund or rollback cannot take the other.
	if ref.Kind == domain.KindBet {
		err = tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM wager_transactions
			WHERE reference_transaction_id=$1 AND kind IN ('REFUND','ROLLBACK') AND status='PROCESSED'`,
			ref.ID).Scan(&n)
		if err != nil {
			return false, err
		}
		return n > 0, nil
	}
	return false, nil
}

func matchReference(row *postgres.WagerRow, ref postgres.WagerRow) error {
	if deref(row.ProviderID) != deref(ref.ProviderID) {
		return ErrValidation
	}
	if row.PlayerID != ref.PlayerID || row.WalletID != ref.WalletID {
		return ErrValidation
	}
	if deref(row.RoundID) != deref(ref.RoundID) {
		return ErrValidation
	}
	if row.Currency != ref.Currency {
		return ErrValidation
	}
	return nil
}

func (s *Service) pendingReference(ctx context.Context, tx pgx.Tx, row *postgres.WagerRow, w *domain.Wallet) (ProcessResult, error) {
	row.Status = domain.StatusPendingReference
	bal := w.Balance().Cents()
	row.ResultBalanceCents = &bal
	if row.CreatedAt.IsZero() {
		row.CreatedAt = s.Now()
	}
	exists := false
	var dummy uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM wager_transactions WHERE id=$1`, row.ID).Scan(&dummy)
	if err == nil {
		exists = true
	}
	if exists {
		if err := s.Store.UpdateWager(ctx, tx, *row); err != nil {
			return ProcessResult{}, err
		}
	} else {
		if err := s.Store.InsertWager(ctx, tx, *row); err != nil {
			return ProcessResult{}, err
		}
		if err := s.emitEvent(ctx, tx, "WagerTransactionPendingReference", row.WalletID, map[string]any{
			"transactionId": row.ID.String(),
			"status":        row.Status,
			"walletId":      row.WalletID.String(),
		}); err != nil {
			return ProcessResult{}, err
		}
	}
	return resultFromRow(*row, false)
}

func (s *Service) reject(ctx context.Context, tx pgx.Tx, row *postgres.WagerRow, w *domain.Wallet, code string, resume bool) (ProcessResult, error) {
	row.Status = domain.StatusRejected
	row.FailureCode = ptrStr(code)
	if w != nil {
		c := w.Balance().Cents()
		row.ResultBalanceCents = &c
	}
	if resume || rowExists(ctx, tx, row.ID) {
		if err := s.Store.UpdateWager(ctx, tx, *row); err != nil {
			return ProcessResult{}, err
		}
	} else {
		if err := s.Store.InsertWager(ctx, tx, *row); err != nil {
			return ProcessResult{}, err
		}
	}
	if err := s.emitEvent(ctx, tx, "WagerTransactionRejected", row.WalletID, map[string]any{
		"transactionId": row.ID.String(),
		"failureCode":   code,
		"walletId":      row.WalletID.String(),
		"kind":          string(row.Kind),
	}); err != nil {
		return ProcessResult{}, err
	}
	return resultFromRow(*row, false)
}

func rowExists(ctx context.Context, tx pgx.Tx, id uuid.UUID) bool {
	var dummy uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM wager_transactions WHERE id=$1`, id).Scan(&dummy)
	return err == nil
}

func (s *Service) ledger(txID uuid.UUID, w *domain.Wallet, dir domain.Direction, amount, before domain.Money) (domain.LedgerEntry, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return domain.LedgerEntry{}, err
	}
	return domain.NewLedgerEntry(id, w.ID(), txID, dir, amount, before, w.Balance(), s.Now())
}

func (s *Service) emitProcessed(ctx context.Context, tx pgx.Tx, row postgres.WagerRow, w *domain.Wallet, entry *domain.LedgerEntry) error {
	if err := s.emitEvent(ctx, tx, "WagerTransactionProcessed", row.WalletID, map[string]any{
		"transactionId": row.ID.String(),
		"walletId":      row.WalletID.String(),
		"kind":          string(row.Kind),
		"status":        string(row.Status),
	}); err != nil {
		return err
	}
	if entry == nil {
		return nil
	}
	return s.emitEvent(ctx, tx, "WalletBalanceChanged", w.ID(), map[string]any{
		"walletId":      w.ID().String(),
		"transactionId": row.ID.String(),
		"direction":     string(entry.Direction),
		"money":         entry.Amount.Wire(),
		"balanceBefore": entry.BalanceBefore.Wire(),
		"balanceAfter":  entry.BalanceAfter.Wire(),
		"walletVersion": w.Version(),
	})
}

func (s *Service) emitEvent(ctx context.Context, tx pgx.Tx, typ string, aggregate uuid.UUID, data map[string]any) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	envelope := map[string]any{
		"eventId":       eventID.String(),
		"eventType":     typ,
		"aggregateId":   aggregate.String(),
		"correlationId": aggregate.String(),
		"occurredAt":    s.Now().Format(time.RFC3339Nano),
		"version":       1,
		"data":          data,
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return s.Store.InsertOutbox(ctx, tx, eventID, typ, aggregate, b)
}

func (s *Service) GetTransaction(ctx context.Context, id uuid.UUID) (postgres.WagerRow, error) {
	r, err := s.Store.GetWagerByID(ctx, id)
	if err != nil {
		return postgres.WagerRow{}, err
	}
	if r.ID == uuid.Nil {
		return postgres.WagerRow{}, ErrNotFound
	}
	return r, nil
}

func (s *Service) GetByExternal(ctx context.Context, provider, external string) (postgres.WagerRow, error) {
	r, err := s.Store.GetWagerByExternal(ctx, provider, external)
	if err != nil {
		return postgres.WagerRow{}, err
	}
	if r.ID == uuid.Nil {
		return postgres.WagerRow{}, ErrNotFound
	}
	return r, nil
}

type ReconcileResult struct {
	WalletID          uuid.UUID
	StoredBalance     domain.Money
	CalculatedBalance domain.Money
	Difference        domain.Money
	Consistent        bool
	CheckedEntries    int
}

func (s *Service) Reconcile(ctx context.Context, walletID uuid.UUID) (ReconcileResult, error) {
	w, err := s.Store.GetWallet(ctx, walletID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if w == nil {
		return ReconcileResult{}, ErrNotFound
	}
	credits, debits, n, err := s.Store.SumLedger(ctx, walletID)
	if err != nil {
		return ReconcileResult{}, err
	}
	calc, err := domain.FromCents(credits-debits, w.Balance().Currency())
	if err != nil {
		return ReconcileResult{}, err
	}
	diff, err := w.Balance().Sub(calc)
	if err != nil {
		return ReconcileResult{}, err
	}
	ok := diff.IsZero()
	if !ok {
		s.Log.Error("reconciliation divergence",
			"walletId", walletID,
			"stored", w.Balance().AmountString(),
			"calculated", calc.AmountString(),
		)
	}
	return ReconcileResult{
		WalletID:          walletID,
		StoredBalance:     w.Balance(),
		CalculatedBalance: calc,
		Difference:        diff,
		Consistent:        ok,
		CheckedEntries:    n,
	}, nil
}

func resultFromRow(r postgres.WagerRow, replay bool) (ProcessResult, error) {
	cur := r.Currency
	cents := int64(0)
	if r.ResultBalanceCents != nil {
		cents = *r.ResultBalanceCents
	}
	m, err := domain.FromCents(cents, cur)
	if err != nil {
		return ProcessResult{}, err
	}
	code := ""
	if r.FailureCode != nil {
		code = *r.FailureCode
	}
	return ProcessResult{
		TransactionID:    r.ID,
		Status:           r.Status,
		FailureCode:      code,
		Balance:          m,
		IdempotentReplay: replay,
	}, nil
}

func ptrStr(s string) *string { return &s }
func ptrInt(n int64) *int64   { return &n }
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
