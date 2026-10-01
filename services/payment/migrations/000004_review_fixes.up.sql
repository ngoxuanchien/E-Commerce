-- The reconciler claims each PENDING payment for a bounded time instead of holding its row lock
-- across provider calls: reconcile_after is the lease, and a row is claimable only once the lease has
-- expired, so a crashed replica's rows come back on the next interval. The default is the epoch, which
-- is the one sentinel both as a timestamp and in Go's time.Time — '-infinity' is not scannable into
-- one, and it would break every SELECT * over payments.
ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS reconcile_after timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00';
