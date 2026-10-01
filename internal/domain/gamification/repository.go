package gamification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the points layer's storage.
type Repository interface {
	// Totals is the user's progress row; zero values when they have none.
	Totals(ctx context.Context, userID uuid.UUID) (Totals, error)
	// SetTimezone records the zone the device reported.
	SetTimezone(ctx context.Context, userID uuid.UUID, tz string) error
	// Insert writes one ledger row and the totals it implies, in one
	// transaction. inserted is false when the same award already existed or
	// the kind's daily cap was reached; then nothing changes. bumpStreak
	// applies NextStreak for localDate.
	Insert(ctx context.Context, e Event, dailyCap int, bumpStreak bool) (inserted bool, totals Totals, err error)
	// Counts are what badges are judged on.
	Counts(ctx context.Context, userID uuid.UUID, longestStreak int) (Counts, error)
	// Badges is when each earned badge was awarded.
	Badges(ctx context.Context, userID uuid.UUID) (map[string]time.Time, error)
	// GrantBadge records a badge; false when it was already earned.
	GrantBadge(ctx context.Context, userID uuid.UUID, badgeID string) (bool, error)
	// Today is how many awards of each kind the user has on localDate.
	Today(ctx context.Context, userID uuid.UUID, localDate time.Time) (map[Kind]int, error)
	// Visible keeps the ids whose leaderboard_visible switch is on (users
	// with no settings row count as visible: the default is on).
	Visible(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	// Scores is each id's value for metric over period (zero From: all time).
	Scores(ctx context.Context, ids []uuid.UUID, metric Metric, period Period) (map[uuid.UUID]int64, error)
	// Progresses is the totals of several users.
	Progresses(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Totals, error)
	// History is the ledger, newest first, older than before (zero: from the top).
	History(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]Event, error)
	// CompleteDay marks a day of the user's own trip walked. newlyDone is
	// false when it was already complete; tripDone reports whether every day
	// of the trip now is. ErrNotFound when the user does not own it.
	CompleteDay(ctx context.Context, userID uuid.UUID, tripID, dayID string) (newlyDone, tripDone bool, label string, err error)
	// POI is a place's stored position, for checking a visit.
	POI(ctx context.Context, poiID string) (lat, lon float64, name string, found bool, err error)
	// ClaimPushSlot reserves today's push of kind for userID; false when one
	// was already sent today.
	ClaimPushSlot(ctx context.Context, userID uuid.UUID, kind string, localDate time.Time) (bool, error)
}

type pgRepository struct {
	db *pgxpool.Pool
}

// NewRepository returns the Postgres repository.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepository{db: db}
}

func (r *pgRepository) Totals(ctx context.Context, userID uuid.UUID) (Totals, error) {
	t := Totals{Timezone: "UTC"}
	err := r.db.QueryRow(ctx, `
		SELECT total_points, current_streak, longest_streak, last_active_date, timezone
		FROM user_progress WHERE user_id = $1`, userID).
		Scan(&t.TotalPoints, &t.CurrentStreak, &t.LongestStreak, &t.LastActiveDate, &t.Timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, nil
	}
	if err != nil {
		return t, fmt.Errorf("read progress: %w", err)
	}
	return t, nil
}

func (r *pgRepository) SetTimezone(ctx context.Context, userID uuid.UUID, tz string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_progress (user_id, timezone) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET timezone = EXCLUDED.timezone, updated_at = NOW()`, userID, tz)
	if err != nil {
		return fmt.Errorf("set timezone: %w", err)
	}
	return nil
}

func (r *pgRepository) Insert(ctx context.Context, e Event, dailyCap int, bumpStreak bool) (bool, Totals, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, Totals{}, fmt.Errorf("begin award: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the user's progress row first, so two awards for the same user
	// serialise and the daily cap below cannot be raced past.
	t := Totals{Timezone: "UTC"}
	if _, err := tx.Exec(ctx, `INSERT INTO user_progress (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, e.UserID); err != nil {
		return false, Totals{}, fmt.Errorf("ensure progress: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT total_points, current_streak, longest_streak, last_active_date, timezone
		FROM user_progress WHERE user_id = $1 FOR UPDATE`, e.UserID).
		Scan(&t.TotalPoints, &t.CurrentStreak, &t.LongestStreak, &t.LastActiveDate, &t.Timezone); err != nil {
		return false, Totals{}, fmt.Errorf("lock progress: %w", err)
	}

	if dailyCap > 0 {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM points_events WHERE user_id = $1 AND kind = $2 AND local_date = $3`,
			e.UserID, e.Kind, e.LocalDate).Scan(&n); err != nil {
			return false, Totals{}, fmt.Errorf("count today: %w", err)
		}
		if n >= dailyCap {
			return false, t, nil
		}
	}

	ct, err := tx.Exec(ctx, `
		INSERT INTO points_events (user_id, kind, ref_key, points, label, local_date)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, kind, ref_key) DO NOTHING`,
		e.UserID, e.Kind, e.RefKey, e.Points, e.Label, e.LocalDate)
	if err != nil {
		return false, Totals{}, fmt.Errorf("insert award: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return false, t, nil
	}

	t.TotalPoints += int64(e.Points)
	if bumpStreak {
		t.CurrentStreak = NextStreak(t.CurrentStreak, t.LastActiveDate, e.LocalDate)
		if t.CurrentStreak > t.LongestStreak {
			t.LongestStreak = t.CurrentStreak
		}
		d := e.LocalDate
		t.LastActiveDate = &d
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_progress SET total_points = $2, current_streak = $3, longest_streak = $4,
			last_active_date = $5, updated_at = NOW()
		WHERE user_id = $1`,
		e.UserID, t.TotalPoints, t.CurrentStreak, t.LongestStreak, t.LastActiveDate); err != nil {
		return false, Totals{}, fmt.Errorf("update progress: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, Totals{}, fmt.Errorf("commit award: %w", err)
	}
	return true, t, nil
}

func (r *pgRepository) Counts(ctx context.Context, userID uuid.UUID, longestStreak int) (Counts, error) {
	c := Counts{LongestStreak: longestStreak}
	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE kind = 4),
			COUNT(DISTINCT split_part(ref_key, ':', 2)) FILTER (WHERE kind = 3),
			COUNT(*) FILTER (WHERE kind = 5),
			COUNT(*) FILTER (WHERE kind = 8)
		FROM points_events WHERE user_id = $1`, userID).
		Scan(&c.Cities, &c.Places, &c.ScoutClaims, &c.TripsDone)
	if err != nil {
		return c, fmt.Errorf("count achievements: %w", err)
	}
	return c, nil
}

func (r *pgRepository) Badges(ctx context.Context, userID uuid.UUID) (map[string]time.Time, error) {
	rows, err := r.db.Query(ctx, `SELECT badge_id, awarded_at FROM user_badges WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("list badges: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var (
			id string
			at time.Time
		)
		if err := rows.Scan(&id, &at); err != nil {
			return nil, fmt.Errorf("scan badge: %w", err)
		}
		out[id] = at
	}
	return out, rows.Err()
}

func (r *pgRepository) GrantBadge(ctx context.Context, userID uuid.UUID, badgeID string) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		INSERT INTO user_badges (user_id, badge_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, badgeID)
	if err != nil {
		return false, fmt.Errorf("grant badge: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

func (r *pgRepository) Today(ctx context.Context, userID uuid.UUID, localDate time.Time) (map[Kind]int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT kind, COUNT(*) FROM points_events WHERE user_id = $1 AND local_date = $2 GROUP BY kind`,
		userID, localDate)
	if err != nil {
		return nil, fmt.Errorf("read today: %w", err)
	}
	defer rows.Close()
	out := map[Kind]int{}
	for rows.Next() {
		var (
			k Kind
			n int
		)
		if err := rows.Scan(&k, &n); err != nil {
			return nil, fmt.Errorf("scan today: %w", err)
		}
		out[k] = n
	}
	return out, rows.Err()
}

func (r *pgRepository) Visible(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, COALESCE(ns.leaderboard_visible, TRUE)
		FROM unnest($1::uuid[]) AS u(id)
		LEFT JOIN notification_settings ns ON ns.user_id = u.id`, ids)
	if err != nil {
		return nil, fmt.Errorf("read visibility: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var (
			id      uuid.UUID
			visible bool
		)
		if err := rows.Scan(&id, &visible); err != nil {
			return nil, fmt.Errorf("scan visibility: %w", err)
		}
		out[id] = visible
	}
	return out, rows.Err()
}

func (r *pgRepository) Scores(ctx context.Context, ids []uuid.UUID, metric Metric, period Period) (map[uuid.UUID]int64, error) {
	var value string
	switch metric {
	case MetricCities:
		value = "COUNT(*) FILTER (WHERE e.kind = 4)"
	case MetricPlaces:
		value = "COUNT(DISTINCT split_part(e.ref_key, ':', 2)) FILTER (WHERE e.kind = 3)"
	default:
		value = "COALESCE(SUM(e.points), 0)"
	}
	var from, to *time.Time
	if !period.From.IsZero() {
		from, to = &period.From, &period.To
	}
	// value is one of three constants above, never input.
	query := `
		SELECT u.id, ` + value + `
		FROM unnest($1::uuid[]) AS u(id)
		LEFT JOIN points_events e ON e.user_id = u.id
			AND ($2::date IS NULL OR e.local_date BETWEEN $2::date AND $3::date)
		GROUP BY u.id`
	rows, err := r.db.Query(ctx, query, ids, from, to)
	if err != nil {
		return nil, fmt.Errorf("score leaderboard: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]int64{}
	for rows.Next() {
		var (
			id uuid.UUID
			v  int64
		)
		if err := rows.Scan(&id, &v); err != nil {
			return nil, fmt.Errorf("scan score: %w", err)
		}
		out[id] = v
	}
	return out, rows.Err()
}

func (r *pgRepository) Progresses(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Totals, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id, total_points, current_streak, longest_streak, last_active_date, timezone
		FROM user_progress WHERE user_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("read progresses: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]Totals{}
	for rows.Next() {
		var (
			id uuid.UUID
			t  Totals
		)
		if err := rows.Scan(&id, &t.TotalPoints, &t.CurrentStreak, &t.LongestStreak, &t.LastActiveDate, &t.Timezone); err != nil {
			return nil, fmt.Errorf("scan progress: %w", err)
		}
		out[id] = t
	}
	return out, rows.Err()
}

func (r *pgRepository) History(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]Event, error) {
	var cursor *time.Time
	if !before.IsZero() {
		cursor = &before
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, kind, ref_key, points, label, local_date, created_at
		FROM points_events
		WHERE user_id = $1 AND ($2::timestamptz IS NULL OR created_at < $2)
		ORDER BY created_at DESC
		LIMIT $3`, userID, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e := Event{UserID: userID}
		if err := rows.Scan(&e.ID, &e.Kind, &e.RefKey, &e.Points, &e.Label, &e.LocalDate, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *pgRepository) CompleteDay(ctx context.Context, userID uuid.UUID, tripID, dayID string) (bool, bool, string, error) {
	tid, err1 := uuid.Parse(tripID)
	did, err2 := uuid.Parse(dayID)
	if err1 != nil || err2 != nil {
		return false, false, "", ErrNotFound
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, false, "", fmt.Errorf("begin complete day: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		completedAt *time.Time
		title       string
		dayNumber   int
	)
	err = tx.QueryRow(ctx, `
		SELECT d.completed_at, t.title, d.day_number
		FROM trip_days d JOIN trips t ON t.id = d.trip_id
		WHERE d.id = $1 AND d.trip_id = $2 AND t.user_id = $3
		FOR UPDATE OF d`, did, tid, userID).Scan(&completedAt, &title, &dayNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, "", ErrNotFound
	}
	if err != nil {
		return false, false, "", fmt.Errorf("read trip day: %w", err)
	}
	newly := completedAt == nil
	if newly {
		if _, err := tx.Exec(ctx, `UPDATE trip_days SET completed_at = NOW() WHERE id = $1`, did); err != nil {
			return false, false, "", fmt.Errorf("complete day: %w", err)
		}
	}
	var tripDone bool
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(bool_and(completed_at IS NOT NULL), FALSE) FROM trip_days WHERE trip_id = $1`, tid).
		Scan(&tripDone); err != nil {
		return false, false, "", fmt.Errorf("check trip done: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, "", fmt.Errorf("commit complete day: %w", err)
	}
	return newly, tripDone, fmt.Sprintf("%s, day %d", title, dayNumber), nil
}

func (r *pgRepository) POI(ctx context.Context, poiID string) (float64, float64, string, bool, error) {
	var (
		lat, lon *float64
		name     string
	)
	err := r.db.QueryRow(ctx, `
		SELECT ST_Y(location), ST_X(location), name FROM points_of_interest WHERE id::text = $1`, poiID).
		Scan(&lat, &lon, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, "", false, nil
	}
	if err != nil {
		return 0, 0, "", false, fmt.Errorf("read poi: %w", err)
	}
	if lat == nil || lon == nil {
		return 0, 0, name, false, nil
	}
	return *lat, *lon, name, true, nil
}

func (r *pgRepository) ClaimPushSlot(ctx context.Context, userID uuid.UUID, kind string, localDate time.Time) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		INSERT INTO progress_push_log (user_id, kind, local_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		userID, kind, localDate)
	if err != nil {
		return false, fmt.Errorf("claim push slot: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}
