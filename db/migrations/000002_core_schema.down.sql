-- 000002_core_schema.down.sql — Rolls back core database schema and triggers.
-- Drops all tables in reverse dependency order and removes trigger functions.

DROP TRIGGER IF EXISTS payment_events_immutable ON payment_events;
DROP TRIGGER IF EXISTS ledger_transactions_immutable ON ledger_transactions;
DROP TRIGGER IF EXISTS ledger_entries_immutable ON ledger_entries;
DROP TRIGGER IF EXISTS ledger_entries_balanced ON ledger_entries;

DROP FUNCTION IF EXISTS forbid_mutation();
DROP FUNCTION IF EXISTS assert_ledger_balanced();

DROP TABLE IF EXISTS risk_decisions;
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS ledger_transactions;
DROP TABLE IF EXISTS ledger_accounts;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS payment_events;
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS merchants;
