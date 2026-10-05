-- The reconciler purges processed_events older than 30 days. This index lives in its own migration
-- because 000004 was already pushed, so a database at version 4 would never reach it.
CREATE INDEX IF NOT EXISTS processed_events_processed_at_idx ON processed_events (processed_at);
