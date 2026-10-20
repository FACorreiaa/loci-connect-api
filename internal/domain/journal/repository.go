package journal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepository struct{ pool *pgxpool.Pool }

// NewRepository stores the journal in Postgres (migration 0123).
func NewRepository(pool *pgxpool.Pool) Repository { return &pgRepository{pool: pool} }

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (r *pgRepository) Trip(ctx context.Context, userID, tripID uuid.UUID) (Trip, error) {
	t := Trip{ID: tripID}
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(title, ''), COALESCE(city_name, '') FROM trips WHERE id = $1 AND user_id = $2`,
		tripID, userID).Scan(&t.Title, &t.CityName)
	if err != nil {
		return Trip{}, fmt.Errorf("journal trip: %w", notFound(err))
	}
	return t, nil
}

// Days reads the days with their notes, then the stops with when each was
// visited (a "done" mark, else the first logged visit to that place on this
// trip) and the traveller's verdict on the place.
func (r *pgRepository) Days(ctx context.Context, userID, tripID uuid.UUID) ([]Day, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.id, d.day_number, d.date, COALESCE(d.city_name, ''), COALESCE(dd.body, '')
		FROM trip_days d
		JOIN trips t ON t.id = d.trip_id AND t.user_id = $2
		LEFT JOIN trip_day_diaries dd ON dd.day_id = d.id AND dd.trip_id = d.trip_id
		WHERE d.trip_id = $1
		ORDER BY d.day_number`, tripID, userID)
	if err != nil {
		return nil, fmt.Errorf("journal days: %w", err)
	}
	days, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Day, error) {
		var d Day
		err := row.Scan(&d.ID, &d.Number, &d.Date, &d.CityName, &d.Diary)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan journal days: %w", err)
	}
	index := map[uuid.UUID]int{}
	for i := range days {
		index[days[i].ID] = i
	}

	rows, err = r.pool.Query(ctx, `
		SELECT s.day_id, s.id, s.poi_id, s.name, s.notes, s.start_minute,
		       COALESCE(CASE WHEN m.status = 2 THEN m.marked_at END, v.first_at),
		       COALESCE(rk.verdict, 0)
		FROM trip_stops s
		JOIN trip_days d ON d.id = s.day_id AND d.trip_id = $1
		LEFT JOIN trip_stop_marks m ON m.stop_id = s.id AND m.trip_id = $1
		LEFT JOIN LATERAL (
			SELECT MIN(v.visited_at) AS first_at FROM user_visited_pois v
			WHERE v.user_id = $2 AND v.trip_id = $1 AND v.poi_id = s.poi_id
		) v ON s.poi_id <> ''
		LEFT JOIN user_poi_rankings rk ON rk.user_id = $2 AND rk.poi_id = s.poi_id AND s.poi_id <> ''
		ORDER BY d.day_number, s.order_index`, tripID, userID)
	if err != nil {
		return nil, fmt.Errorf("journal stops: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			dayID uuid.UUID
			st    Stop
			start *int32
		)
		if err := rows.Scan(&dayID, &st.ID, &st.POIID, &st.Name, &st.Notes, &start, &st.VisitedAt, &st.Verdict); err != nil {
			return nil, fmt.Errorf("scan journal stop: %w", err)
		}
		st.StartMinute = start
		if i, ok := index[dayID]; ok {
			days[i].Stops = append(days[i].Stops, st)
		}
	}
	return days, rows.Err()
}

// Visits are the visits logged on the trip, with their local date in the
// traveller's own zone (UTC when it is unset or unknown).
func (r *pgRepository) Visits(ctx context.Context, userID, tripID uuid.UUID) ([]Visit, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT v.poi_id, COALESCE(NULLIF(v.poi_name, ''), rk.poi_name, ''), v.source, v.visited_at,
		       (v.visited_at AT TIME ZONE COALESCE(tz.name, 'UTC'))::date,
		       COALESCE(rk.verdict, 0)
		FROM user_visited_pois v
		JOIN users u ON u.id = v.user_id
		LEFT JOIN pg_timezone_names tz ON tz.name = u.timezone
		LEFT JOIN user_poi_rankings rk ON rk.user_id = v.user_id AND rk.poi_id = v.poi_id
		WHERE v.user_id = $1 AND v.trip_id = $2
		ORDER BY v.visited_at`, userID, tripID)
	if err != nil {
		return nil, fmt.Errorf("journal visits: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Visit, error) {
		var v Visit
		err := row.Scan(&v.POIID, &v.Name, &v.Source, &v.VisitedAt, &v.LocalDate, &v.Verdict)
		return v, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan journal visits: %w", err)
	}
	return out, nil
}

const photoCols = `id, user_id, trip_id, day_id, stop_id, object_key, content_type, size_bytes, caption, status, created_at`

func scanPhoto(row pgx.Row) (Photo, error) {
	var p Photo
	err := row.Scan(&p.ID, &p.UserID, &p.TripID, &p.DayID, &p.StopID, &p.Key, &p.ContentType, &p.Size, &p.Caption, &p.Status, &p.CreatedAt)
	return p, err
}

func (r *pgRepository) ReadyPhotos(ctx context.Context, tripID uuid.UUID) ([]Photo, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+photoCols+` FROM trip_photos
		WHERE trip_id = $1 AND status = 'ready' ORDER BY created_at`, tripID)
	if err != nil {
		return nil, fmt.Errorf("journal photos: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Photo, error) { return scanPhoto(row) })
	if err != nil {
		return nil, fmt.Errorf("scan journal photos: %w", err)
	}
	return out, nil
}

func (r *pgRepository) DayTrip(ctx context.Context, userID, dayID uuid.UUID) (uuid.UUID, error) {
	var tripID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT d.trip_id FROM trip_days d JOIN trips t ON t.id = d.trip_id
		WHERE d.id = $1 AND t.user_id = $2`, dayID, userID).Scan(&tripID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("journal day: %w", notFound(err))
	}
	return tripID, nil
}

func (r *pgRepository) StopDay(ctx context.Context, tripID, stopID uuid.UUID) (uuid.UUID, error) {
	var dayID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT s.day_id FROM trip_stops s JOIN trip_days d ON d.id = s.day_id
		WHERE s.id = $1 AND d.trip_id = $2`, stopID, tripID).Scan(&dayID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("journal stop: %w", notFound(err))
	}
	return dayID, nil
}

func (r *pgRepository) DayOnTrip(ctx context.Context, tripID, dayID uuid.UUID) error {
	var ok bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM trip_days WHERE id = $1 AND trip_id = $2)`, dayID, tripID).Scan(&ok); err != nil {
		return fmt.Errorf("journal day on trip: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (r *pgRepository) SetDiary(ctx context.Context, tripID, dayID uuid.UUID, body string) error {
	var err error
	if body == "" {
		_, err = r.pool.Exec(ctx, `DELETE FROM trip_day_diaries WHERE day_id = $1 AND trip_id = $2`, dayID, tripID)
	} else {
		_, err = r.pool.Exec(ctx, `
			INSERT INTO trip_day_diaries (day_id, trip_id, body) VALUES ($1, $2, $3)
			ON CONFLICT (day_id) DO UPDATE SET body = EXCLUDED.body, updated_at = NOW()
			WHERE trip_day_diaries.trip_id = EXCLUDED.trip_id`, dayID, tripID, body)
	}
	if err != nil {
		return fmt.Errorf("journal diary: %w", err)
	}
	return nil
}

func (r *pgRepository) InsertPhoto(ctx context.Context, p Photo) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO trip_photos (id, user_id, trip_id, day_id, stop_id, object_key, content_type, size_bytes, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending')`,
		p.ID, p.UserID, p.TripID, p.DayID, p.StopID, p.Key, p.ContentType, p.Size)
	if err != nil {
		return fmt.Errorf("insert photo: %w", err)
	}
	return nil
}

func (r *pgRepository) Photo(ctx context.Context, userID, photoID uuid.UUID) (Photo, error) {
	p, err := scanPhoto(r.pool.QueryRow(ctx, `SELECT `+photoCols+` FROM trip_photos WHERE id = $1 AND user_id = $2`, photoID, userID))
	if err != nil {
		return Photo{}, fmt.Errorf("journal photo: %w", notFound(err))
	}
	return p, nil
}

func (r *pgRepository) MarkReady(ctx context.Context, photoID uuid.UUID, caption string) (Photo, error) {
	p, err := scanPhoto(r.pool.QueryRow(ctx, `
		UPDATE trip_photos SET status = 'ready', caption = $2 WHERE id = $1 RETURNING `+photoCols, photoID, caption))
	if err != nil {
		return Photo{}, fmt.Errorf("confirm photo: %w", notFound(err))
	}
	return p, nil
}

func (r *pgRepository) DeletePhoto(ctx context.Context, userID, photoID uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM trip_photos WHERE id = $1 AND user_id = $2`, photoID, userID)
	if err != nil {
		return false, fmt.Errorf("delete photo: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *pgRepository) SweepPending(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM trip_photos WHERE status = 'pending' AND created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweep pending photos: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *pgRepository) Tombstones(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT object_key FROM trip_photo_tombstones ORDER BY deleted_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("photo tombstones: %w", err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scan photo tombstones: %w", err)
	}
	return keys, nil
}

func (r *pgRepository) ClearTombstones(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM trip_photo_tombstones WHERE object_key = ANY($1)`, keys); err != nil {
		return fmt.Errorf("clear photo tombstones: %w", err)
	}
	return nil
}
