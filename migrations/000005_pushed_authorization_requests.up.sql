-- 000005_pushed_authorization_requests.up.sql
-- Pushed authorization requests (RFC 9126): the pushed_authorization_requests
-- table backs the PushedAuthorizationRequestStore port
-- (StorePushedAuthorizationRequest / GetPushedAuthorizationRequest /
-- TakePushedAuthorizationRequest / RemoveExpired).
--
--   * key is the primary key. Every key comes from
--     storage.PushedAuthorizationRequestKey; the store never sees the
--     request_uri and never generates keys. The primary key backs the
--     single-row read and the atomic take (DELETE ... RETURNING).
--   * client_id, creation_time, entry_expiration and retention_expiration
--     are visible columns copied from the model at write time, for operator
--     queries and future indexes. They are never read back: the model is
--     decoded from payload only, because timestamptz keeps microseconds and
--     the model times may carry nanoseconds. An empty client id is stored
--     as NULL.
--   * payload holds the whole model in the container
--     {"version":1,"dataProtected":false,"payload":"<base64>"}; the opaque
--     request bytes may hold NUL and invalid UTF-8, so they only travel
--     base64-encoded.
--   * retention_expiration is the reclamation column. RemoveExpired deletes
--     rows whose retention_expiration precedes the caller's cutoff; the index
--     backs that path. The port has no enumeration by client, so client_id
--     is not indexed.
--
-- DDL stays relative to the connection's current search_path like 000001.
CREATE TABLE pushed_authorization_requests (
    key                  text PRIMARY KEY,
    client_id            text,
    creation_time        timestamptz NOT NULL,
    entry_expiration     timestamptz NOT NULL,
    retention_expiration timestamptz NOT NULL,
    payload              jsonb NOT NULL
);
CREATE INDEX idx_pushed_authorization_requests_retention_expiration
    ON pushed_authorization_requests (retention_expiration);
