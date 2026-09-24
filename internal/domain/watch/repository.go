package watch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

const watchColumns = `id, user_id, session_id, title, schedule_human, interval_minutes,
	spec, enabled, next_run_at, last_run_at, created_at`

// PostgresRepository stores watches in chat_watches.
type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

var _ Repository = (*PostgresRepository)(nil)

func scanWatch(row pgx.Row) (Watch, error) {
	var w Watch
	err := row.Scan(&w.ID, &w.UserID, &w.SessionID, &w.Title, &w.ScheduleHuman,
		&w.IntervalMinutes, &w.Spec, &w.Enabled, &w.NextRunAt, &w.LastRunAt, &w.CreatedAt)
	return w, err
}

func (r *PostgresRepository) Create(ctx context.Context, w Watch) (Watch, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO chat_watches (user_id, session_id, title, schedule_human,
			interval_minutes, spec, next_run_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+watchColumns,
		w.UserID, w.SessionID, w.Title, w.ScheduleHuman, w.IntervalMinutes, w.Spec, w.NextRunAt)
	created, err := scanWatch(row)
	if err != nil {
		return Watch{}, fmt.Errorf("create watch: %w", err)
	}
	return created, nil
}

func (r *PostgresRepository) CountByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM chat_watches WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count watches: %w", err)
	}
	return n, nil
}

func (r *PostgresRepository) List(ctx context.Context, userID uuid.UUID, sessionID *uuid.UUID) ([]Watch, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+watchColumns+`
		FROM chat_watches
		WHERE user_id = $1 AND ($2::uuid IS NULL OR session_id = $2)
		ORDER BY next_run_at, created_at`, userID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list watches: %w", err)
	}
	defer rows.Close()
	out := []Watch{}
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, fmt.Errorf("scan watch: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *PostgresRepository) Delete(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM chat_watches WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete watch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]Watch, error) {
	var due []Watch
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		// SKIP LOCKED: an overlapping claimer takes different rows instead of
		// waiting for these and then running them a second time.
		rows, err := tx.Query(ctx, `
			SELECT `+watchColumns+`
			FROM chat_watches
			WHERE enabled AND next_run_at <= $1
			ORDER BY next_run_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED`, now, limit)
		if err != nil {
			return fmt.Errorf("select due watches: %w", err)
		}
		for rows.Next() {
			w, err := scanWatch(rows)
			if err != nil {
				rows.Close()
				return fmt.Errorf("scan due watch: %w", err)
			}
			due = append(due, w)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, w := range due {
			if _, err := tx.Exec(ctx, `
				UPDATE chat_watches
				SET next_run_at = $2, last_run_at = $3, updated_at = NOW()
				WHERE id = $1`, w.ID, nextSlot(w.NextRunAt, w.Interval(), now), now); err != nil {
				return fmt.Errorf("advance watch %s: %w", w.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return due, nil
}

func (r *PostgresRepository) Disable(ctx context.Context, id uuid.UUID) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE chat_watches SET enabled = FALSE, updated_at = NOW() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("disable watch: %w", err)
	}
	return nil
}

// ErrLockHeld means another replica is running the watch loop this minute.
var ErrLockHeld = errors.New("watch: runner lock held elsewhere")

// runnerLockKey is the pg advisory lock the runner takes. Any constant works
// as long as nothing else in the database uses it; this one spells
// "lociwtch" in ASCII.
const runnerLockKey int64 = 0x6c6f636977746368

// PgLocker takes a session-level advisory lock on one pooled connection and
// holds that connection until unlocked, so the lock and its release happen
// on the same backend.
type PgLocker struct {
	pool *pgxpool.Pool
	key  int64
}

func NewPgLocker(pool *pgxpool.Pool) *PgLocker {
	return &PgLocker{pool: pool, key: runnerLockKey}
}

// TryLock returns ErrLockHeld without waiting when another session has it.
func (l *PgLocker) TryLock(ctx context.Context) (func(), error) {
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
		return nil, ErrLockHeld
	}
	return func() {
		// Unlock on a fresh context: the tick's may already be cancelled
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
