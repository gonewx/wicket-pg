-- 000006_client_registrations.up.sql
-- Dynamic client registration: the client_registrations table backs the
-- ClientRegistrationStore port (RegisterClient / FindClientRegistration /
-- FindClientById / UpdateClientRegistration / RemoveClientRegistration) and
-- the ClientOriginLookup port (FindClientsByAllowedOrigin), both served by
-- the same store value from the same rows.
--
--   * client_id is the primary key. Registration is a plain INSERT, so the
--     primary key rejects a client id that is registered or that was
--     registered and removed (SQLSTATE 23505 -> ErrDuplicateHandle).
--   * revision is the optimistic concurrency column and the only source of
--     the record's revision on read. Updates and removals are a single
--     conditional UPDATE on (client_id, NOT removed, revision); an update
--     bumps it in the same statement.
--   * removed marks a tombstone. A tombstone stays in the same row and keeps
--     only client_id, revision and registration_time: payload and the token
--     digest are NULL and allowed_cors_origins is '{}', so no secret hash or
--     digest survives a removal. Every read filters on NOT removed.
--   * registration_access_token_digest, registration_time and
--     allowed_cors_origins are visible columns copied from the record at
--     write time. They are never read back: the record is decoded from
--     payload only (the time may carry nanoseconds, timestamptz keeps
--     microseconds).
--   * allowed_cors_origins backs the origin lookup with an exact text match
--     (allowed_cors_origins @> ARRAY[$1]::text[]); the GIN index serves it.
--     A client without origins stores '{}', never NULL.
--   * payload holds the encoded client, the digest and the registration
--     time in the container {"version":1,"dataProtected":false,"payload":
--     "<base64>"}.
--   * The two CHECK constraints tie the tombstone shape to the removed
--     flag: payload and digest are present exactly on live rows.
--
-- Registered clients have no server-side lifecycle: there is no expiry
-- column and no reclamation path. DDL stays relative to the connection's
-- current search_path like 000001.
CREATE TABLE client_registrations (
    client_id                        text PRIMARY KEY,
    revision                         bigint NOT NULL,
    removed                          boolean NOT NULL DEFAULT false,
    registration_access_token_digest text,
    registration_time                timestamptz NOT NULL,
    allowed_cors_origins             text[] NOT NULL,
    payload                          jsonb,
    CONSTRAINT client_registrations_payload_tombstone CHECK (removed = (payload IS NULL)),
    CONSTRAINT client_registrations_digest_tombstone CHECK (removed = (registration_access_token_digest IS NULL))
);
CREATE INDEX idx_client_registrations_allowed_cors_origins
    ON client_registrations USING gin (allowed_cors_origins);
