-- Backfill delivered_at (added NULL by 000005) for orders that were already
-- DELIVERED or COMPLETED before P2, from their latest DELIVERED history row.
-- Without it auto-complete (delivered_at <= cutoff) never matches them.
UPDATE orders o
SET delivered_at = h.at
FROM (SELECT order_id, max(created_at) AS at
      FROM order_status_history
      WHERE to_status = 'DELIVERED'
      GROUP BY order_id) h
WHERE o.id = h.order_id
  AND o.status IN ('DELIVERED', 'COMPLETED')
  AND o.delivered_at IS NULL;
