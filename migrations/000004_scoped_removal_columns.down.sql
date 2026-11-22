-- 000004_scoped_removal_columns.down.sql
-- Reverses 000004: drop the scoped removal indexes and locator columns,
-- returning the grant tables to their 000003 shape. The payload column is
-- untouched, so a later Up backfills the columns again.
DROP INDEX IF EXISTS idx_user_consents_client_id;
DROP INDEX IF EXISTS idx_device_codes_session_id;
DROP INDEX IF EXISTS idx_reference_tokens_session_id;
DROP INDEX IF EXISTS idx_reference_tokens_client_id;
DROP INDEX IF EXISTS idx_refresh_tokens_session_id;
DROP INDEX IF EXISTS idx_refresh_tokens_client_id;
DROP INDEX IF EXISTS idx_authorization_codes_session_id;
DROP INDEX IF EXISTS idx_authorization_codes_client_id;
ALTER TABLE device_codes DROP COLUMN IF EXISTS session_id;
ALTER TABLE reference_tokens DROP COLUMN IF EXISTS session_id;
ALTER TABLE reference_tokens DROP COLUMN IF EXISTS client_id;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS session_id;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS client_id;
ALTER TABLE authorization_codes DROP COLUMN IF EXISTS session_id;
ALTER TABLE authorization_codes DROP COLUMN IF EXISTS client_id;
