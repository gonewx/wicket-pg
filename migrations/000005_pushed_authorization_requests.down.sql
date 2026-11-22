-- 000005_pushed_authorization_requests.down.sql
-- Reverses 000005: drop the reclamation index and the
-- pushed_authorization_requests table, returning the schema to its 000004
-- shape.
DROP INDEX IF EXISTS idx_pushed_authorization_requests_retention_expiration;
DROP TABLE IF EXISTS pushed_authorization_requests;
