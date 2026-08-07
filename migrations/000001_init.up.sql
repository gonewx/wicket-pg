-- 000001_init.up.sql
-- Initial schema: migration bookkeeping table, business tables, and the
-- indexes that back the store read and cleanup paths. All DDL is relative
-- to the connection's current search_path; no schema name is written, so
-- hosts and test factories can pin search_path per schema.

-- Migration bookkeeping (applied versions; idempotent by design).
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text PRIMARY KEY,
    applied_at timestamptz NOT NULL
);

-- Business tables. Handles are primary keys for single-point lookups;
-- payload holds the versioned container {"version":1,"dataProtected":false,
-- "payload":"<base64>"}. expires_at/expires are nullable: a NULL value
-- means "never expires" and is never cleaned up.

CREATE TABLE authorization_codes (
    handle     text PRIMARY KEY,
    expires_at timestamptz,
    payload    jsonb NOT NULL
);

CREATE TABLE refresh_tokens (
    handle     text PRIMARY KEY,
    expires_at timestamptz,
    version    bigint NOT NULL,
    payload    jsonb NOT NULL
);

CREATE TABLE reference_tokens (
    handle     text PRIMARY KEY,
    expires_at timestamptz,
    payload    jsonb NOT NULL
);

CREATE TABLE user_consents (
    subject_id text NOT NULL,
    client_id  text NOT NULL,
    expires_at timestamptz,
    payload    jsonb NOT NULL,
    PRIMARY KEY (subject_id, client_id)
);

CREATE TABLE persisted_grants (
    key        text PRIMARY KEY,
    subject_id text NOT NULL,
    session_id text NOT NULL,
    client_id  text NOT NULL,
    type       text NOT NULL,
    expires_at timestamptz,
    payload    jsonb NOT NULL
);

CREATE TABLE device_codes (
    handle     text PRIMARY KEY,
    expires_at timestamptz,
    payload    jsonb NOT NULL
);

CREATE TABLE backchannel_auth_requests (
    handle     text PRIMARY KEY,
    expires_at timestamptz,
    payload    jsonb NOT NULL
);

CREATE TABLE sessions (
    session_id text PRIMARY KEY,
    client_ids text[] NOT NULL,
    expires    timestamptz,
    payload    jsonb NOT NULL
);

CREATE TABLE key_records (
    handle    text PRIMARY KEY,
    public_id text NOT NULL,
    phase     text NOT NULL,
    version   bigint NOT NULL,
    payload   jsonb NOT NULL
);

-- Indexes. Every index backs a real read or cleanup path:
--   * expires_at/expires: DELETE ... WHERE expires_at IS NOT NULL AND
--     expires_at < $cutoff cleanup (cutoff comes from the caller, never
--     from a clock function inside SQL);
--   * persisted_grants subject_id/session_id/client_id/type: batch
--     revocation filters, including = ANY(...) on client_id and type;
--   * key_records public_id: uniqueness among non-retired records.

CREATE INDEX idx_authorization_codes_expires_at ON authorization_codes (expires_at);
CREATE INDEX idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);
CREATE INDEX idx_reference_tokens_expires_at ON reference_tokens (expires_at);
CREATE INDEX idx_user_consents_expires_at ON user_consents (expires_at);
CREATE INDEX idx_persisted_grants_expires_at ON persisted_grants (expires_at);
CREATE INDEX idx_device_codes_expires_at ON device_codes (expires_at);
CREATE INDEX idx_backchannel_auth_requests_expires_at ON backchannel_auth_requests (expires_at);
CREATE INDEX idx_sessions_expires ON sessions (expires);

CREATE INDEX idx_persisted_grants_subject_id ON persisted_grants (subject_id, client_id, type);
CREATE INDEX idx_persisted_grants_session_id ON persisted_grants (session_id);
CREATE INDEX idx_persisted_grants_client_id ON persisted_grants (client_id, type);
CREATE INDEX idx_persisted_grants_type ON persisted_grants (type);

CREATE UNIQUE INDEX idx_key_records_public_id_unique ON key_records (public_id) WHERE phase <> 'retired';
