-- 000001_init.down.sql
-- Rolls back the initial schema in strict mirror order: indexes first,
-- then business tables, then the migration bookkeeping table.

DROP INDEX IF EXISTS idx_key_records_public_id_unique;
DROP INDEX IF EXISTS idx_sessions_expires;
DROP INDEX IF EXISTS idx_backchannel_auth_requests_expires_at;
DROP INDEX IF EXISTS idx_device_codes_expires_at;
DROP INDEX IF EXISTS idx_persisted_grants_expires_at;
DROP INDEX IF EXISTS idx_user_consents_expires_at;
DROP INDEX IF EXISTS idx_reference_tokens_expires_at;
DROP INDEX IF EXISTS idx_refresh_tokens_expires_at;
DROP INDEX IF EXISTS idx_authorization_codes_expires_at;
DROP INDEX IF EXISTS idx_persisted_grants_type;
DROP INDEX IF EXISTS idx_persisted_grants_client_id;
DROP INDEX IF EXISTS idx_persisted_grants_session_id;
DROP INDEX IF EXISTS idx_persisted_grants_subject_id;

DROP TABLE IF EXISTS key_records;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS backchannel_auth_requests;
DROP TABLE IF EXISTS device_codes;
DROP TABLE IF EXISTS persisted_grants;
DROP TABLE IF EXISTS user_consents;
DROP TABLE IF EXISTS reference_tokens;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS authorization_codes;

DROP TABLE IF EXISTS schema_migrations;
