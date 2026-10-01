DROP INDEX IF EXISTS processed_events_processed_at_idx;
DROP INDEX IF EXISTS provider_events_received_idx;
DROP INDEX IF EXISTS refunds_provider_ref_idx;
ALTER TABLE payments DROP COLUMN IF EXISTS reconcile_after;
