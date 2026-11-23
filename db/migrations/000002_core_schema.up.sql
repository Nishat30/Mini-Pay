-- 000002_core_schema.up.sql — Core database schema and triggers for MiniPay.
-- Owns: table definitions for merchants, payments, payment_events, refunds,
-- idempotency_keys, ledger_accounts, ledger_transactions, ledger_entries,
-- outbox_events, webhook_deliveries, risk_decisions, and ledger integrity triggers.
-- Must never: be modified after deployment (use new migrations for schema changes).
-- Relies on: PostgreSQL 16+ (UNIQUE NULLS NOT DISTINCT, deferred constraint triggers).

-- 1. merchants
CREATE TABLE merchants (
    id VARCHAR(32) PRIMARY KEY,
    name TEXT NOT NULL,
    api_key_hash BYTEA NOT NULL UNIQUE,
    webhook_url TEXT NULL,
    webhook_secret TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. payments
CREATE TABLE payments (
    id VARCHAR(32) PRIMARY KEY,
    merchant_id VARCHAR(32) NOT NULL REFERENCES merchants(id),
    amount BIGINT NOT NULL CHECK (amount > 0),
    amount_refunded BIGINT NOT NULL DEFAULT 0,
    currency CHAR(3) NOT NULL CHECK (currency IN ('usd', 'eur', 'gbp', 'inr', 'jpy')),
    status TEXT NOT NULL CHECK (status IN ('processing', 'requires_review', 'succeeded', 'failed', 'refunded')),
    processor TEXT NOT NULL CHECK (processor IN ('simulator', 'stripe')),
    processor_ref TEXT NULL,
    failure_code TEXT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT payments_amount_refunded_check CHECK (amount_refunded BETWEEN 0 AND amount)
);

CREATE INDEX idx_payments_merchant_created ON payments (merchant_id, created_at DESC);

-- 3. payment_events (append-only)
CREATE TABLE payment_events (
    id BIGSERIAL PRIMARY KEY,
    payment_id VARCHAR(32) NOT NULL REFERENCES payments(id),
    from_status TEXT NULL,
    to_status TEXT NOT NULL,
    reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 4. refunds
CREATE TABLE refunds (
    id VARCHAR(32) PRIMARY KEY,
    payment_id VARCHAR(32) NOT NULL REFERENCES payments(id),
    amount BIGINT NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 5. idempotency_keys
CREATE TABLE idempotency_keys (
    merchant_id VARCHAR(32) NOT NULL REFERENCES merchants(id),
    key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
    request_hash BYTEA NOT NULL,
    state TEXT NOT NULL DEFAULT 'completed' CHECK (state IN ('started', 'completed')),
    response_status INT NOT NULL,
    response_body JSONB NOT NULL,
    locked_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '24 hours'),
    PRIMARY KEY (merchant_id, key)
);

-- 6. ledger_accounts
CREATE TABLE ledger_accounts (
    id VARCHAR(32) PRIMARY KEY,
    merchant_id VARCHAR(32) NULL REFERENCES merchants(id),
    kind TEXT NOT NULL CHECK (kind IN ('processor_clearing', 'merchant_payable', 'platform_fees')),
    currency CHAR(3) NOT NULL CHECK (currency IN ('usd', 'eur', 'gbp', 'inr', 'jpy')),
    UNIQUE NULLS NOT DISTINCT (merchant_id, kind, currency)
);

-- 7. ledger_transactions
CREATE TABLE ledger_transactions (
    id VARCHAR(32) PRIMARY KEY,
    payment_id VARCHAR(32) NOT NULL REFERENCES payments(id),
    kind TEXT NOT NULL CHECK (kind IN ('capture', 'refund')),
    reference TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 8. ledger_entries
CREATE TABLE ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    transaction_id VARCHAR(32) NOT NULL REFERENCES ledger_transactions(id),
    account_id VARCHAR(32) NOT NULL REFERENCES ledger_accounts(id),
    direction TEXT NOT NULL CHECK (direction IN ('debit', 'credit')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency CHAR(3) NOT NULL CHECK (currency IN ('usd', 'eur', 'gbp', 'inr', 'jpy'))
);

CREATE INDEX idx_ledger_entries_account_id ON ledger_entries (account_id);

-- 9. outbox_events
CREATE TABLE outbox_events (
    id BIGSERIAL PRIMARY KEY,
    merchant_id VARCHAR(32) NOT NULL REFERENCES merchants(id),
    aggregate_id VARCHAR(32) NOT NULL,
    type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ NULL,
    attempts INT NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_outbox_unprocessed ON outbox_events (available_at, id) WHERE processed_at IS NULL;

-- 10. webhook_deliveries
CREATE TABLE webhook_deliveries (
    id VARCHAR(32) PRIMARY KEY,
    merchant_id VARCHAR(32) NOT NULL REFERENCES merchants(id),
    event_id BIGINT NOT NULL REFERENCES outbox_events(id),
    state TEXT NOT NULL CHECK (state IN ('pending', 'succeeded', 'dead')),
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status INT NULL,
    last_error TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, event_id)
);

CREATE INDEX idx_webhook_pending ON webhook_deliveries (next_attempt_at) WHERE state = 'pending';

-- 11. risk_decisions
CREATE TABLE risk_decisions (
    id BIGSERIAL PRIMARY KEY,
    payment_id VARCHAR(32) NOT NULL REFERENCES payments(id),
    rule TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('allow', 'review', 'block')),
    detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Triggers protecting the ledger and append-only tables

-- 1. Balance assertion trigger (deferred constraint trigger)
CREATE OR REPLACE FUNCTION assert_ledger_balanced() RETURNS trigger AS $$
BEGIN
  IF (SELECT COALESCE(SUM(CASE direction WHEN 'debit' THEN amount ELSE -amount END), 0)
        FROM ledger_entries WHERE transaction_id = NEW.transaction_id) <> 0 THEN
    RAISE EXCEPTION 'ledger transaction % is unbalanced', NEW.transaction_id;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER ledger_entries_balanced
  AFTER INSERT ON ledger_entries
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_ledger_balanced();

-- 2. Immutability trigger for ledger and append-only event tables
CREATE OR REPLACE FUNCTION forbid_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'table % rows are append-only and cannot be updated or deleted', TG_TABLE_NAME;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_entries_immutable
  BEFORE UPDATE OR DELETE ON ledger_entries
  FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER ledger_transactions_immutable
  BEFORE UPDATE OR DELETE ON ledger_transactions
  FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER payment_events_immutable
  BEFORE UPDATE OR DELETE ON payment_events
  FOR EACH ROW EXECUTE FUNCTION forbid_mutation();
