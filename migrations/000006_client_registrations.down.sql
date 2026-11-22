-- 000006_client_registrations.down.sql
-- Reverses 000006: drop the origin lookup index and the client_registrations
-- table, returning the schema to its 000005 shape.
DROP INDEX IF EXISTS idx_client_registrations_allowed_cors_origins;
DROP TABLE IF EXISTS client_registrations;
