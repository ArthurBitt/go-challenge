package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"junglego/internal/domain"
)

type Store struct {
	Pool *pgxpool.Pool
}

func Connect(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

func (s *Store) Begin(ctx context.Context) (pgx.Tx, error) { return s.Pool.Begin(ctx) }

func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func scanWallet(row interface{ Scan(dest ...any) error }) (*domain.Wallet, error) {
	var (
		id, player       uuid.UUID
		currency         string
		cents            int64
		version          int
		created, updated time.Time
	)
	if err := row.Scan(&id, &player, &currency, &cents, &version, &created, &updated); err != nil {
		return nil, err
	}
	m, err := domain.FromCents(cents, currency)
	if err != nil {
		return nil, err
	}
	return domain.RehydrateWallet(id, player, m, version, created, updated), nil
}

func (s *Store) InsertWallet(ctx context.Context, tx pgx.Tx, w *domain.Wallet) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO wallets (id, player_id, currency, balance_cents, version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		w.ID(), w.PlayerID(), w.Balance().Currency(), w.Balance().Cents(), w.Version(), w.CreatedAt(), w.UpdatedAt())
	return err
}

func (s *Store) GetWallet(ctx context.Context, id uuid.UUID) (*domain.Wallet, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, player_id, currency, balance_cents, version, created_at, updated_at
		FROM wallets WHERE id=$1`, id)
	w, err := scanWallet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

func (s *Store) LockWallet(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Wallet, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, player_id, currency, balance_cents, version, created_at, updated_at
		FROM wallets WHERE id=$1 FOR UPDATE`, id)
	w, err := scanWallet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

func (s *Store) UpdateWallet(ctx context.Context, tx pgx.Tx, w *domain.Wallet) error {
	_, err := tx.Exec(ctx, `
		UPDATE wallets SET balance_cents=$2, version=$3, updated_at=$4 WHERE id=$1`,
		w.ID(), w.Balance().Cents(), w.Version(), w.UpdatedAt())
	return err
}

type WagerRow struct {
	ID                     uuid.UUID
	Origin                 domain.Origin
	Kind                   domain.Kind
	Status                 domain.Status
	ProviderID             *string
	ExternalID             *string
	IdempotencyKey         *string
	PayloadHash            *string
	WalletID               uuid.UUID
	PlayerID               uuid.UUID
	RoundID                *string
	GameID                 *string
	AmountCents            int64
	Currency               string
	ReferenceExternalID    *string
	ReferenceTransactionID *uuid.UUID
	FailureCode            *string
	ResultBalanceCents     *int64
	Attempts               int
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func scanWager(row interface{ Scan(dest ...any) error }) (WagerRow, error) {
	var r WagerRow
	err := row.Scan(
		&r.ID, &r.Origin, &r.Kind, &r.Status,
		&r.ProviderID, &r.ExternalID, &r.IdempotencyKey, &r.PayloadHash,
		&r.WalletID, &r.PlayerID, &r.RoundID, &r.GameID,
		&r.AmountCents, &r.Currency, &r.ReferenceExternalID, &r.ReferenceTransactionID,
		&r.FailureCode, &r.ResultBalanceCents, &r.Attempts, &r.CreatedAt, &r.UpdatedAt,
	)
	return r, err
}

const wagerCols = `id, origin, kind, status, provider_id, external_transaction_id, idempotency_key, payload_hash,
wallet_id, player_id, round_id, game_id, amount_cents, currency, reference_external_id, reference_transaction_id,
failure_code, result_balance_cents, attempts, created_at, updated_at`

func (s *Store) InsertWager(ctx context.Context, tx pgx.Tx, r WagerRow) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key, payload_hash,
			wallet_id, player_id, round_id, game_id, amount_cents, currency, reference_external_id, reference_transaction_id,
			failure_code, result_balance_cents, attempts, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		r.ID, r.Origin, r.Kind, r.Status, r.ProviderID, r.ExternalID, r.IdempotencyKey, r.PayloadHash,
		r.WalletID, r.PlayerID, r.RoundID, r.GameID, r.AmountCents, r.Currency, r.ReferenceExternalID, r.ReferenceTransactionID,
		r.FailureCode, r.ResultBalanceCents, r.Attempts, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *Store) UpdateWager(ctx context.Context, tx pgx.Tx, r WagerRow) error {
	_, err := tx.Exec(ctx, `
		UPDATE wager_transactions SET
			status=$2, reference_transaction_id=$3, failure_code=$4, result_balance_cents=$5,
			attempts=$6, updated_at=$7
		WHERE id=$1`,
		r.ID, r.Status, r.ReferenceTransactionID, r.FailureCode, r.ResultBalanceCents, r.Attempts, r.UpdatedAt)
	return err
}

func (s *Store) GetWagerByID(ctx context.Context, id uuid.UUID) (WagerRow, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+wagerCols+` FROM wager_transactions WHERE id=$1`, id)
	r, err := scanWager(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return WagerRow{}, nil
	}
	return r, err
}

func (s *Store) GetWagerByIdempotencyTx(ctx context.Context, tx pgx.Tx, provider, key string) (WagerRow, error) {
	row := tx.QueryRow(ctx, `SELECT `+wagerCols+` FROM wager_transactions WHERE provider_id=$1 AND idempotency_key=$2`, provider, key)
	r, err := scanWager(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return WagerRow{}, nil
	}
	return r, err
}

func (s *Store) GetWagerByExternalTx(ctx context.Context, tx pgx.Tx, provider, external string) (WagerRow, error) {
	row := tx.QueryRow(ctx, `SELECT `+wagerCols+` FROM wager_transactions WHERE provider_id=$1 AND external_transaction_id=$2`, provider, external)
	r, err := scanWager(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return WagerRow{}, nil
	}
	return r, err
}

func (s *Store) GetWagerByExternal(ctx context.Context, provider, external string) (WagerRow, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+wagerCols+` FROM wager_transactions WHERE provider_id=$1 AND external_transaction_id=$2`, provider, external)
	r, err := scanWager(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return WagerRow{}, nil
	}
	return r, err
}

func (s *Store) InsertLedger(ctx context.Context, tx pgx.Tx, e domain.LedgerEntry) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO wallet_ledger (id, wallet_id, transaction_id, direction, amount_cents, currency, balance_before_cents, balance_after_cents, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		e.ID, e.WalletID, e.TransactionID, e.Direction, e.Amount.Cents(), e.Amount.Currency(),
		e.BalanceBefore.Cents(), e.BalanceAfter.Cents(), e.CreatedAt)
	return err
}

type LedgerView struct {
	ID            uuid.UUID
	WalletID      uuid.UUID
	TransactionID uuid.UUID
	Direction     string
	AmountCents   int64
	Currency      string
	Before        int64
	After         int64
	CreatedAt     time.Time
}

func (s *Store) ListLedger(ctx context.Context, walletID uuid.UUID, after *uuid.UUID, limit int) ([]LedgerView, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = s.Pool.Query(ctx, `
			SELECT id, wallet_id, transaction_id, direction, amount_cents, currency, balance_before_cents, balance_after_cents, created_at
			FROM wallet_ledger WHERE wallet_id=$1 ORDER BY created_at ASC, id ASC LIMIT $2`, walletID, limit)
	} else {
		rows, err = s.Pool.Query(ctx, `
			SELECT id, wallet_id, transaction_id, direction, amount_cents, currency, balance_before_cents, balance_after_cents, created_at
			FROM wallet_ledger
			WHERE wallet_id=$1 AND (created_at, id) > (
				SELECT created_at, id FROM wallet_ledger WHERE id=$2
			)
			ORDER BY created_at ASC, id ASC LIMIT $3`, walletID, *after, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerView
	for rows.Next() {
		var v LedgerView
		if err := rows.Scan(&v.ID, &v.WalletID, &v.TransactionID, &v.Direction, &v.AmountCents, &v.Currency, &v.Before, &v.After, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SumLedger(ctx context.Context, walletID uuid.UUID) (credits, debits int64, n int, err error) {
	err = s.Pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN direction='CREDIT' THEN amount_cents ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN direction='DEBIT' THEN amount_cents ELSE 0 END),0),
			COUNT(*)
		FROM wallet_ledger WHERE wallet_id=$1`, walletID).Scan(&credits, &debits, &n)
	return
}

func (s *Store) InsertInbox(ctx context.Context, tx pgx.Tx, consumer, messageID, digest string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO inbox_messages (consumer_name, message_id, digest) VALUES ($1,$2,$3)`,
		consumer, messageID, digest)
	return err
}

func (s *Store) GetInbox(ctx context.Context, tx pgx.Tx, consumer, messageID string) (digest string, ok bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT digest FROM inbox_messages WHERE consumer_name=$1 AND message_id=$2`, consumer, messageID).Scan(&digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return digest, true, nil
}

func (s *Store) InsertOutbox(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, eventType string, aggregate uuid.UUID, payload []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (event_id, event_type, aggregate_id, payload) VALUES ($1,$2,$3,$4)`,
		eventID, eventType, aggregate, payload)
	return err
}

type OutboxRow struct {
	EventID   uuid.UUID
	EventType string
	Payload   json.RawMessage
}

func (s *Store) ClaimOutbox(ctx context.Context, limit int) ([]OutboxRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT event_id, event_type, payload FROM outbox_events
		WHERE published_at IS NULL
		ORDER BY created_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxRow
	for rows.Next() {
		var r OutboxRow
		if err := rows.Scan(&r.EventID, &r.EventType, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkOutboxPublished(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE outbox_events SET published_at=now() WHERE event_id=$1`, id)
	return err
}

func (s *Store) ListPendingReferences(ctx context.Context, limit int) ([]WagerRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+wagerCols+` FROM wager_transactions WHERE status='PENDING_REFERENCE' ORDER BY created_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WagerRow
	for rows.Next() {
		r, err := scanWager(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CountLedgerByWallet(ctx context.Context, walletID uuid.UUID) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_ledger WHERE wallet_id=$1`, walletID).Scan(&n)
	return n, err
}
