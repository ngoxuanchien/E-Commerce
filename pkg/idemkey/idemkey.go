// Package idemkey stores Idempotency-Key claims and their final responses
// (P1 WS3 §4 step 1, with the P1.5 reclaim of orphaned rows).
//
// created_at is the claim. Claim inserts a row, or takes over an orphaned one:
// same request hash, not completed (status_code IS NULL), and claimed before staleBefore. Complete and
// Release act only while the caller's claim is still the row's claim, so a
// run whose key was reclaimed can neither overwrite nor free it. Release
// never frees a completed key.
//
// SchemaSQL uses CREATE TABLE IF NOT EXISTS, which does nothing on a database
// that already has an idempotency_keys table of another shape (order_db's P1
// table has no scope column). A service adopting this package there needs a
// new table name or a migration.
package idemkey

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaSQL creates idempotency_keys. Copy it verbatim into a migration.
//
//go:embed schema.sql
var SchemaSQL string

// ErrClaimLost: a newer request reclaimed the key; roll the caller's transaction back.
var ErrClaimLost = errors.New("idemkey: claim lost to a newer request")

// DB is satisfied by pgx.Tx and *pgxpool.Pool.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Response is a rendered HTTP response stored for replays.
type Response struct {
	Status int
	Body   []byte
}

// Record is an existing key. Response is nil while its request is in progress.
type Record struct {
	RequestHash []byte
	Response    *Response
	ClaimedAt   time.Time
}

// Claim claims (scope, userID, key) at `at`. created reports whether the caller
// now holds the claim (inserted, or reclaimed an orphan; the zero staleBefore
// never reclaims). Otherwise rec describes the existing row: compare its hash
// (different → key reused), then its Response (nil → in progress).
func Claim(ctx context.Context, db DB, scope, userID, key string, requestHash []byte, at, staleBefore time.Time) (Record, bool, error) {
	at = at.UTC().Truncate(time.Microsecond) // Postgres stores µs; the claim is compared for equality
	claimed := Record{RequestHash: requestHash, ClaimedAt: at}
	tag, err := db.Exec(ctx, `INSERT INTO idempotency_keys (scope, user_id, key, request_hash, created_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, scope, userID, key, requestHash, at)
	if err != nil {
		return Record{}, false, fmt.Errorf("idemkey: insert: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return claimed, true, nil
	}
	tag, err = db.Exec(ctx, `UPDATE idempotency_keys SET created_at = $5
		WHERE scope = $1 AND user_id = $2 AND key = $3 AND request_hash = $4
		  AND status_code IS NULL AND created_at < $6`, scope, userID, key, requestHash, at, staleBefore)
	if err != nil {
		return Record{}, false, fmt.Errorf("idemkey: reclaim: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return claimed, true, nil
	}
	rec, found, err := Get(ctx, db, scope, userID, key)
	if err != nil {
		return Record{}, false, err
	}
	if !found { // released between our insert and this read: report "in progress" so the client retries
		return Record{RequestHash: requestHash}, false, nil
	}
	return rec, false, nil
}

// Get reads a key without claiming it.
func Get(ctx context.Context, db DB, scope, userID, key string) (Record, bool, error) {
	var rec Record
	var body json.RawMessage
	var status *int32
	err := db.QueryRow(ctx, `SELECT request_hash, response, status_code, created_at FROM idempotency_keys
		WHERE scope = $1 AND user_id = $2 AND key = $3`, scope, userID, key).Scan(&rec.RequestHash, &body, &status, &rec.ClaimedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("idemkey: get: %w", err)
	}
	rec.ClaimedAt = rec.ClaimedAt.UTC()
	if status != nil {
		rec.Response = &Response{Status: int(*status), Body: []byte(body)}
	}
	return rec, true, nil
}

// Complete stores the final response. ErrClaimLost when claimedAt is no
// longer the key's claim.
func Complete(ctx context.Context, db DB, scope, userID, key string, claimedAt time.Time, resp Response) error {
	claimedAt = claimedAt.UTC().Truncate(time.Microsecond) // compared for equality with the stored µs claim
	var body any                                           // SQL NULL for an empty body; status_code marks completion
	if len(resp.Body) > 0 {
		body = json.RawMessage(resp.Body)
	}
	tag, err := db.Exec(ctx, `UPDATE idempotency_keys SET response = $5, status_code = $6
		WHERE scope = $1 AND user_id = $2 AND key = $3 AND created_at = $4`,
		scope, userID, key, claimedAt, body, resp.Status)
	if err != nil {
		return fmt.Errorf("idemkey: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrClaimLost
	}
	return nil
}

// Release frees the key after a transient failure, if claimedAt is still its
// claim and the key is not completed. A completed key is kept: the caller may
// have seen an error although its transaction committed (e.g. the connection
// dropped during COMMIT), and a retry must replay the stored response.
func Release(ctx context.Context, db DB, scope, userID, key string, claimedAt time.Time) error {
	claimedAt = claimedAt.UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, `DELETE FROM idempotency_keys
		WHERE scope = $1 AND user_id = $2 AND key = $3 AND created_at = $4
		  AND status_code IS NULL`, scope, userID, key, claimedAt); err != nil {
		return fmt.Errorf("idemkey: release: %w", err)
	}
	return nil
}

// Purge deletes keys claimed before `before` (callers use a 24 h TTL).
func Purge(ctx context.Context, db DB, before time.Time) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("idemkey: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}
