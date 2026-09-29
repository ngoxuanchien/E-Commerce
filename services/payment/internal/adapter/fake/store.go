package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

// Hooks are the optional test hooks stored per session.
type Hooks struct {
	Webhook string `json:"webhook,omitempty"` // "" | duplicate | drop | stale_after
	Refund  string `json:"refund,omitempty"`  // "" | fail
	Cancel  string `json:"cancel,omitempty"`  // "" | unsupported
}

// ErrSessionClosed is returned by Submit when the session already has an outcome.
var ErrSessionClosed = errors.New("fake: session already has an outcome")

const (
	outcomeSucceed = "succeed"
	outcomeFail    = "fail"
	outcomeExpired = "expired"
)

type store struct{ pool *pgxpool.Pool }

type session struct {
	outcome *string
	hooks   Hooks
	amount  int64
	expires time.Time
}

func isBadUUID(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "22P02"
}

func (s store) create(ctx context.Context, paymentID string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO fake_sessions (payment_id) VALUES ($1) ON CONFLICT DO NOTHING`, paymentID)
	return err
}

// get loads the session joined with its payment's amount and expiry.
func (s store) get(ctx context.Context, paymentID string) (session, error) {
	var (
		out  session
		raw  []byte
		outc *string
	)
	err := s.pool.QueryRow(ctx, `SELECT f.outcome, f.hooks, p.amount, p.expires_at
		FROM fake_sessions f JOIN payments p ON p.id = f.payment_id WHERE f.payment_id = $1`, paymentID).
		Scan(&outc, &raw, &out.amount, &out.expires)
	if errors.Is(err, pgx.ErrNoRows) || isBadUUID(err) {
		return session{}, app.ErrNotFound
	}
	if err != nil {
		return session{}, err
	}
	out.outcome = outc
	out.expires = out.expires.UTC()
	// The column default is '[]'; only an object carries hooks.
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &out.hooks); err != nil {
			return session{}, fmt.Errorf("decode hooks: %w", err)
		}
	}
	return out, nil
}

// submit records the outcome and hooks once; it returns ErrSessionClosed if an outcome exists.
func (s store) submit(ctx context.Context, paymentID, outcome string, h Hooks) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE fake_sessions SET outcome=$2, hooks=$3 WHERE payment_id=$1 AND outcome IS NULL`,
		paymentID, outcome, raw)
	if isBadUUID(err) {
		return app.ErrNotFound
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.get(ctx, paymentID); err != nil {
			return err
		}
		return ErrSessionClosed
	}
	return nil
}

// expire marks a session without an outcome as expired; false when it already had one.
func (s store) expire(ctx context.Context, paymentID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE fake_sessions SET outcome=$2 WHERE payment_id=$1 AND outcome IS NULL`, paymentID, outcomeExpired)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s store) addRefunded(ctx context.Context, paymentID string, amount int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE fake_sessions SET refunded = refunded + $2 WHERE payment_id=$1`, paymentID, amount)
	return err
}
