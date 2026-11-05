-- 000001_init.up.sql — Baseline schema placeholder.
-- This migration creates the initial extensions and settings.
-- It owns: enabling required Postgres extensions.
-- Invariant: must be idempotent (uses IF NOT EXISTS).

-- We'll need pgcrypto for gen_random_bytes if needed.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Set timezone to UTC for consistency.
SET timezone = 'UTC';

