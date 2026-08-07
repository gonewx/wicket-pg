-- 000003_session_subject_id.up.sql
-- Session subject dimension: the sessions table gains the subject_id
-- column backing the batch revocation paths (GetSessionsBySubjectID /
-- DeleteSessionsBySubjectID). The column is nullable — an empty subject is
-- stored as NULL, mirroring the in-memory semantics where an empty subject
-- is never indexed, so a query for "" can never match. The index backs the
-- "revoke every session of a subject" access pattern. DDL stays relative
-- to the connection's current search_path like 000001.
ALTER TABLE sessions ADD COLUMN subject_id text;
CREATE INDEX idx_sessions_subject_id ON sessions (subject_id);
