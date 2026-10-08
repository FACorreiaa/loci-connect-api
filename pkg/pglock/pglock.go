// Package pglock elects one worker across replicas with a Postgres
// session-level advisory lock.
package pglock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrHeld means another session holds the lock right now.
var ErrHeld = errors.New("pglock: lock held elsewhere")

// Locker takes one advisory lock on one pooled connection and holds that
// connection until unlocked, so the lock and its release happen on the same
// backend. Each background loop uses its own key; any constant works as long
// as nothing else in the database uses it.
type Locker struct {
	pool *pgxpool.Pool
	key  int64
}

// New returns a locker for key.
func New(pool *pgxpool.Pool, key int64) *Locker {
	return &Locker{pool: pool, key: key}
}

// TryLock returns an unlock func, or ErrHeld without waiting when another
// session has the lock.
func (l *Locker) TryLock(ctx context.Context) (func(), error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire lock connection: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, l.key).Scan(&got); err != nil {
		conn.Release()
		return nil, fmt.Errorf("try advisory lock: %w", err)
	}
	if !got {
		conn.Release()
		return nil, ErrHeld
	}
	return func() {
		// Unlock on a fresh context: the caller's may already be cancelled
		// (shutdown), and a lock left on a pooled connection would outlive it.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, l.key); err != nil {
			// Closing the connection ends the session, which releases it.
			_ = conn.Conn().Close(unlockCtx)
		}
		conn.Release()
	}, nil
}
