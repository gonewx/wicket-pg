-- 000003_session_subject_id.down.sql
-- Reverses 000003: drop the subject_id index and column, returning
-- sessions to its 000001 shape.
DROP INDEX IF EXISTS idx_sessions_subject_id;
ALTER TABLE sessions DROP COLUMN IF EXISTS subject_id;
