-- Track how many times Bindery has retried importing a completed download that
-- previously failed (importFailed). Used to cap automatic retries (Bug #7).
ALTER TABLE downloads ADD COLUMN import_retry_count INTEGER NOT NULL DEFAULT 0;
