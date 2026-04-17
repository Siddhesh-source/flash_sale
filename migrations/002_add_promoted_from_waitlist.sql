-- 002_add_promoted_from_waitlist.sql

ALTER TABLE reservations ADD COLUMN IF NOT EXISTS promoted_from_waitlist BOOLEAN DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_reservations_promoted ON reservations(promoted_from_waitlist);
