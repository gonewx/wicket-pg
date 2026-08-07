-- 000002_device_flow_user_code.down.sql
-- Reverses 000002: drop the user_code unique index and column, returning
-- device_codes to its 000001 shape.
DROP INDEX IF EXISTS idx_device_codes_user_code;
ALTER TABLE device_codes DROP COLUMN IF EXISTS user_code;
