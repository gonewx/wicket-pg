-- 000002_device_flow_user_code.up.sql
-- Device-flow dual-code support: the device_codes table already keyed on
-- handle (= device code) gains the user_code column as the second lookup
-- key. The unique index backs both the FindByUserCode/UpdateByUserCode
-- query path and the insert-only duplicate rejection for user codes
-- (SQLSTATE 23505 on either key maps to the duplicate sentinel). DDL stays
-- relative to the connection's current search_path like 000001.
ALTER TABLE device_codes ADD COLUMN user_code text NOT NULL;
CREATE UNIQUE INDEX idx_device_codes_user_code ON device_codes (user_code);
