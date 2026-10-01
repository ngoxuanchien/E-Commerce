-- name: BalanceByCode :one
SELECT coalesce(sum(e.amount), 0)::bigint AS balance
FROM entries e JOIN accounts a ON a.id = e.account_id
WHERE a.code = @code;

-- name: EscrowBalanceBySeller :one
SELECT coalesce(sum(e.amount), 0)::bigint AS balance
FROM entries e JOIN accounts a ON a.id = e.account_id
WHERE a.owner_user_id = @seller_user_id AND a.code LIKE 'escrow:%';

-- name: ListAccountEntries :many
SELECT e.id, e.transaction_id, e.amount, e.created_at, t.kind, t.refs
FROM entries e
JOIN accounts a ON a.id = e.account_id
JOIN journal_transactions t ON t.id = e.transaction_id
WHERE a.code = @code
  AND (sqlc.narg(before_created_at)::timestamptz IS NULL
       OR (e.created_at, e.id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY e.created_at DESC, e.id DESC
LIMIT @page_limit;

-- name: ListSellerPayouts :many
SELECT * FROM payouts
WHERE seller_user_id = @seller_user_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(before_requested_at)::timestamptz IS NULL
       OR (requested_at, id) < (sqlc.narg(before_requested_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY requested_at DESC, id DESC
LIMIT @page_limit;

-- name: BalancesByAccountType :many
SELECT a.type, coalesce(sum(e.amount), 0)::bigint AS balance
FROM accounts a LEFT JOIN entries e ON e.account_id = a.id
GROUP BY a.type
ORDER BY a.type;

-- name: TotalOfAllEntries :one
SELECT coalesce(sum(amount), 0)::bigint AS total FROM entries;

-- name: CountUnbalancedTransactions :one
SELECT count(*) FROM (
    SELECT t.id
    FROM journal_transactions t LEFT JOIN entries e ON e.transaction_id = t.id
    GROUP BY t.id
    HAVING count(e.id) < 2 OR coalesce(sum(e.amount), 0) <> 0
) unbalanced;

-- name: CountLiabilitiesInDebit :one
SELECT count(*) FROM (
    SELECT a.id
    FROM accounts a JOIN entries e ON e.account_id = a.id
    WHERE a.type = 'liability'
    GROUP BY a.id
    HAVING sum(e.amount) > 0
) in_debit;
