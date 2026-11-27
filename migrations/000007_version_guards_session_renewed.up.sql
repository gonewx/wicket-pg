-- 000007_version_guards_session_renewed.up.sql
-- Contract suite 5.0.0 / 6.0.0 support (wicket v0.13.0 / v0.15.0).
--
--   * device_codes.version and backchannel_auth_requests.version are the
--     optimistic concurrency columns behind the version-guarded
--     UpdateByUserCode / UpdateBackchannelAuthenticationRequest: an update is
--     a single conditional UPDATE on (key, version) that bumps the column in
--     the same statement, and every read takes the record's Version from the
--     column. Rows written before this migration carry no Version in their
--     payload and start at version 1 through the column default.
--   * sessions.renewed carries the session's Renewed time so that
--     RenewSession and TouchSession are single atomic UPDATEs on the
--     renewed / expires columns that never rewrite the payload or the
--     client_ids column. Reads take Renewed and Expires from the columns; a
--     NULL value is the zero time. Existing rows are backfilled from the
--     payload in the same transaction (a zero Renewed stays NULL). The
--     Renewed value is read from the decoded model text with a pattern
--     instead of a ::jsonb cast: jsonb rejects the \u0000 escape the codec
--     can write into any string field, and one such session would roll the
--     whole migration back. The pattern cannot match inside a JSON string,
--     whose quotes are always escaped.
--
-- No index is added: every new read and write path is a primary-key or
-- existing unique-key lookup. DDL stays relative to the connection's
-- current search_path like 000001.
ALTER TABLE device_codes ADD COLUMN version bigint NOT NULL DEFAULT 1;
ALTER TABLE backchannel_auth_requests ADD COLUMN version bigint NOT NULL DEFAULT 1;
ALTER TABLE sessions ADD COLUMN renewed timestamptz;

UPDATE sessions SET
    renewed = CASE WHEN d.r IS NULL OR d.r = '0001-01-01T00:00:00Z' THEN NULL
                   ELSE d.r::timestamptz END
FROM (SELECT session_id AS s,
             substring(convert_from(decode(payload->>'payload', 'base64'), 'UTF8')
                       FROM '"Renewed":"([^"]*)"') AS r
      FROM sessions) d
WHERE sessions.session_id = d.s;
