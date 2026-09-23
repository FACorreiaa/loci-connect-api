package runs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store interface {
	Reserve(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	Release(ctx context.Context, runID uuid.UUID) error
	Attach(ctx context.Context, runID, sessionID uuid.UUID, domain, city string) error
	Finish(ctx context.Context, runID uuid.UUID, status Status, errorCode string) (Run, bool, error)
	Statuses(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID) ([]Run, error)
	FindBySession(ctx context.Context, userID, sessionID uuid.UUID) (Run, bool, error)
	ClaimNotification(ctx context.Context, runID uuid.UUID) (bool, error)
}

type PostgresStore struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, now: time.Now}
}

const runColumns = `id, user_id, COALESCE(session_id, '00000000-0000-0000-0000-000000000000'::uuid),
	domain, city_name, status, error_code, started_at, finished_at`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	var status string
	err := row.Scan(&r.ID, &r.UserID, &r.SessionID, &r.Domain, &r.CityName, &status, &r.ErrorCode, &r.StartedAt, &r.FinishedAt)
	r.Status = Status(status)
	return r, err
}

// Reserve counts and inserts under a per-user transaction lock, so two
// starts racing at MaxConcurrent-1 cannot both get in.
func (s *PostgresStore) Reserve(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, userID); err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: lock: %w", err)
	}
	var running int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM generation_runs
		WHERE user_id = $1 AND status = 'running' AND started_at > NOW() - make_interval(secs => $2)`,
		userID, StaleAfter.Seconds()).Scan(&running); err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: count: %w", err)
	}
	if running >= MaxConcurrent {
		return uuid.Nil, ErrAtCapacity
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO generation_runs (user_id) VALUES ($1) RETURNING id`, userID).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("reserve run: commit: %w", err)
	}
	return id, nil
}

// Release gives back a reservation the handler never used (quota refused,
// bad request). An attached row belongs to a real generation and is kept.
func (s *PostgresStore) Release(ctx context.Context, runID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM generation_runs WHERE id = $1 AND session_id IS NULL`, runID)
	if err != nil {
		return fmt.Errorf("release run: %w", err)
	}
	return nil
}

func (s *PostgresStore) Attach(ctx context.Context, runID, sessionID uuid.UUID, domain, city string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE generation_runs SET session_id = $2, domain = $3, city_name = $4
		WHERE id = $1`, runID, sessionID, domain, city)
	if err != nil {
		return fmt.Errorf("attach run: %w", err)
	}
	return nil
}

// Finish only moves a row that is both still "running" and not yet stale —
// a row readers already see as failed/deadline_exceeded (via Run.effective)
// must not be resurrected into a real completion out from under them.
func (s *PostgresStore) Finish(ctx context.Context, runID uuid.UUID, status Status, errorCode string) (Run, bool, error) {
	run, err := scanRun(s.pool.QueryRow(ctx, `
		UPDATE generation_runs SET status = $2, error_code = $3, finished_at = NOW()
		WHERE id = $1 AND status = 'running' AND started_at > NOW() - make_interval(secs => $4)
		RETURNING `+runColumns, runID, string(status), errorCode, StaleAfter.Seconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("finish run: %w", err)
	}
	return run, true, nil
}

// Statuses reports the newest run of each session (a session has one run per turn).
func (s *PostgresStore) Statuses(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (session_id) `+runColumns+` FROM generation_runs
		WHERE user_id = $1 AND session_id = ANY($2)
		ORDER BY session_id, started_at DESC`, userID, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("run statuses: %w", err)
	}
	defer rows.Close()
	now := s.now()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("run statuses: scan: %w", err)
		}
		out = append(out, r.effective(now))
	}
	return out, rows.Err()
}

// FindBySession returns the session's newest run, i.e. its current turn.
func (s *PostgresStore) FindBySession(ctx context.Context, userID, sessionID uuid.UUID) (Run, bool, error) {
	r, err := scanRun(s.pool.QueryRow(ctx, `
		SELECT `+runColumns+` FROM generation_runs WHERE user_id = $1 AND session_id = $2
		ORDER BY started_at DESC LIMIT 1`, userID, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("find run: %w", err)
	}
	return r.effective(s.now()), true, nil
}

func (s *PostgresStore) ClaimNotification(ctx context.Context, runID uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE generation_runs SET notified_at = NOW() WHERE id = $1 AND notified_at IS NULL`, runID)
	if err != nil {
		return false, fmt.Errorf("claim notification: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
