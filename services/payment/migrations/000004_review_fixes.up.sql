-- The reconciler claims each PENDING payment for a bounded time instead of holding its row lock
-- across provider calls: reconcile_after is the lease, and a row is claimable only once the lease has
-- expired, so a crashed replica's rows come back on the next interval. The default is the epoch, which
-- is the one sentinel both as a timestamp and in Go's time.Time — '-infinity' is not scannable into
-- one, and it would break every SELECT * over payments.
ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS reconcile_after timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00';

-- The provider-hint path looks a refund up by (payments.provider, provider_refund_ref). The provider
-- lives on payments, so the index cannot be provider-scoped, and it is deliberately not UNIQUE: refs
-- are only meaningful per provider, and a violation on a cross-provider collision would make every
-- UpdateRefund of that row fail, wedging the refund worker. The join filters by provider.
CREATE INDEX IF NOT EXISTS refunds_provider_ref_idx ON refunds (provider_refund_ref)
    WHERE provider_refund_ref IS NOT NULL;

-- For age-based scans (and a future purge) of the webhook dedupe table.
CREATE INDEX IF NOT EXISTS provider_events_received_idx ON provider_events (received_at);

-- For the reconciler's age-based purge of the idempotent-consumer processed_events table.
CREATE INDEX IF NOT EXISTS processed_events_processed_at_idx ON processed_events (processed_at);
