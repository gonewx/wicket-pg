-- 000007_version_guards_session_renewed.down.sql
-- Drops the columns added by 000007. Renewed / Expires values written by
-- RenewSession or TouchSession after the up migration live only in the
-- columns (the payload keeps the value of the last full write), so a down
-- migration loses those renewals; the expires column itself predates this
-- migration and is kept.
ALTER TABLE sessions DROP COLUMN IF EXISTS renewed;
ALTER TABLE backchannel_auth_requests DROP COLUMN IF EXISTS version;
ALTER TABLE device_codes DROP COLUMN IF EXISTS version;
