-- 000004_scoped_removal_columns.up.sql
-- Scoped removal locator columns: the grant tables gain the real columns
-- that back removal by client and by session (RemoveXxxByClient /
-- RemoveXxxBySession on the authorization code, refresh token and reference
-- token ports, RemoveUserConsentsByClient, RemoveDeviceCodesBySession).
--
--   * authorization_codes / refresh_tokens / reference_tokens: client_id and
--     session_id. A reference token's session is its sid claim.
--   * device_codes: session_id only. The device flow port has no removal by
--     client, so no client_id column is derived for it.
--   * user_consents already carries client_id, but its composite primary key
--     leads with subject_id, so removal by client gets its own index.
--
-- The columns are nullable and an empty value is stored as NULL, as with
-- sessions.subject_id (000003): NULL never enters an equality match, so a
-- removal for "" can never select a record that has no client or session.
--
-- Existing rows are backfilled in this same transaction from the payload
-- container {"version":1,"dataProtected":false,"payload":"<base64>"}; the
-- decoded model JSON keys are the Go field names. A reference token gets a
-- session only when exactly one claim of type sid is present and its value is
-- a string: more than one sid claim, whatever their value types, or a single
-- non-string one yields NULL, the same result Token.SessionId() returns.
-- Indexes are built after the backfill.
--
-- Operational notes:
--   * The backfill rewrites every row of four tables inside one transaction;
--     on large tables run Up in a maintenance window, or reclaim expired
--     records with RemoveExpired first.
--   * Stop every writer running a pre-000004 build before running Up. After
--     Up, an old build's inserts leave the locator columns NULL and its
--     UpdateRefreshToken / UpdateByUserCode leave stale values; the backfill
--     runs once, inside this transaction, and never revisits those rows.
--   * A payload whose model JSON holds the escape \u0000 (a NUL character in
--     any string field) cannot be cast to jsonb, so Up fails with
--     "unsupported Unicode escape sequence" and the whole transaction rolls
--     back, leaving 000003 intact. Remove the offending records by handle,
--     or let them expire and reclaim them with RemoveExpired, then run Up
--     again.
--   * DDL stays relative to the connection's current search_path like
--     000001.

ALTER TABLE authorization_codes ADD COLUMN client_id text;
ALTER TABLE authorization_codes ADD COLUMN session_id text;
ALTER TABLE refresh_tokens ADD COLUMN client_id text;
ALTER TABLE refresh_tokens ADD COLUMN session_id text;
ALTER TABLE reference_tokens ADD COLUMN client_id text;
ALTER TABLE reference_tokens ADD COLUMN session_id text;
ALTER TABLE device_codes ADD COLUMN session_id text;

UPDATE authorization_codes SET
    client_id  = NULLIF(d.m->>'ClientId', ''),
    session_id = NULLIF(d.m->>'SessionId', '')
FROM (SELECT handle AS h,
             convert_from(decode(payload->>'payload', 'base64'), 'UTF8')::jsonb AS m
      FROM authorization_codes) d
WHERE authorization_codes.handle = d.h;

UPDATE refresh_tokens SET
    client_id  = NULLIF(d.m->>'ClientId', ''),
    session_id = NULLIF(d.m->>'SessionId', '')
FROM (SELECT handle AS h,
             convert_from(decode(payload->>'payload', 'base64'), 'UTF8')::jsonb AS m
      FROM refresh_tokens) d
WHERE refresh_tokens.handle = d.h;

UPDATE reference_tokens SET
    client_id  = NULLIF(d.m->>'ClientId', ''),
    session_id = (SELECT CASE WHEN count(*) = 1 THEN
                             NULLIF(min(c->>'Value') FILTER (WHERE jsonb_typeof(c->'Value') = 'string'), '')
                         END
                  FROM jsonb_array_elements(
                           COALESCE(NULLIF(d.m->'Claims', 'null'::jsonb), '[]'::jsonb)) c
                  WHERE c->>'Type' = 'sid')
FROM (SELECT handle AS h,
             convert_from(decode(payload->>'payload', 'base64'), 'UTF8')::jsonb AS m
      FROM reference_tokens) d
WHERE reference_tokens.handle = d.h;

UPDATE device_codes SET
    session_id = NULLIF(d.m->>'SessionId', '')
FROM (SELECT handle AS h,
             convert_from(decode(payload->>'payload', 'base64'), 'UTF8')::jsonb AS m
      FROM device_codes) d
WHERE device_codes.handle = d.h;

CREATE INDEX idx_authorization_codes_client_id ON authorization_codes (client_id);
CREATE INDEX idx_authorization_codes_session_id ON authorization_codes (session_id);
CREATE INDEX idx_refresh_tokens_client_id ON refresh_tokens (client_id);
CREATE INDEX idx_refresh_tokens_session_id ON refresh_tokens (session_id);
CREATE INDEX idx_reference_tokens_client_id ON reference_tokens (client_id);
CREATE INDEX idx_reference_tokens_session_id ON reference_tokens (session_id);
CREATE INDEX idx_device_codes_session_id ON device_codes (session_id);
CREATE INDEX idx_user_consents_client_id ON user_consents (client_id);
