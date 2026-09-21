CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    player_id UUID NOT NULL,
    currency CHAR(3) NOT NULL,
    balance_cents BIGINT NOT NULL CHECK (balance_cents >= 0),
    version INT NOT NULL CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (player_id, currency)
);

CREATE TABLE wager_transactions (
    id UUID PRIMARY KEY,
    origin TEXT NOT NULL CHECK (origin IN ('INTERNAL', 'EXTERNAL')),
    kind TEXT NOT NULL CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    status TEXT NOT NULL CHECK (status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),
    provider_id TEXT,
    external_transaction_id TEXT,
    idempotency_key TEXT,
    payload_hash TEXT,
    wallet_id UUID NOT NULL REFERENCES wallets (id),
    player_id UUID NOT NULL,
    round_id TEXT,
    game_id TEXT,
    amount_cents BIGINT NOT NULL,
    currency CHAR(3) NOT NULL,
    reference_external_id TEXT,
    reference_transaction_id UUID REFERENCES wager_transactions (id),
    failure_code TEXT,
    result_balance_cents BIGINT,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX wager_external_idempotency
    ON wager_transactions (provider_id, idempotency_key)
    WHERE origin = 'EXTERNAL';

CREATE UNIQUE INDEX wager_external_id
    ON wager_transactions (provider_id, external_transaction_id)
    WHERE origin = 'EXTERNAL';

CREATE UNIQUE INDEX wager_opening_once
    ON wager_transactions (wallet_id)
    WHERE kind = 'OPENING';

CREATE UNIQUE INDEX wager_processed_refund_once
    ON wager_transactions (reference_transaction_id)
    WHERE kind = 'REFUND' AND status = 'PROCESSED';

CREATE UNIQUE INDEX wager_processed_rollback_once
    ON wager_transactions (reference_transaction_id)
    WHERE kind = 'ROLLBACK' AND status = 'PROCESSED';

CREATE INDEX wager_pending_reference
    ON wager_transactions (status)
    WHERE status = 'PENDING_REFERENCE';

CREATE TABLE wallet_ledger (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES wallets (id),
    transaction_id UUID NOT NULL REFERENCES wager_transactions (id),
    direction TEXT NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    currency CHAR(3) NOT NULL,
    balance_before_cents BIGINT NOT NULL,
    balance_after_cents BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (wallet_id, transaction_id),
    CHECK (
        (direction = 'CREDIT' AND balance_after_cents = balance_before_cents + amount_cents)
        OR (direction = 'DEBIT' AND balance_after_cents = balance_before_cents - amount_cents)
    )
);

CREATE TABLE inbox_messages (
    consumer_name TEXT NOT NULL,
    message_id TEXT NOT NULL,
    digest TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_name, message_id)
);

CREATE TABLE outbox_events (
    event_id UUID PRIMARY KEY,
    event_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

CREATE INDEX outbox_unpublished ON outbox_events (created_at) WHERE published_at IS NULL;
