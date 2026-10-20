package tripbrief

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository reads who is due a brief and records what was sent.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// dueSQL finds travellers whose trip covers their local today.
//
// Local time comes from users.timezone, validated against pg_timezone_names
// so one bad zone ("Mars/Base") falls back to UTC instead of failing the
// whole query. Only travellers who turned trip reminders on and have a
// device to reach are considered. With overlapping trips the most recently
// started one is today's. A weather check needs the morning brief first: it
// compares against the forecast that brief saw.
const dueSQL = `
WITH l AS (
	SELECT ns.user_id,
	       ($1::timestamptz AT TIME ZONE COALESCE(tz.name, 'UTC')) AS local_ts
	FROM notification_settings ns
	JOIN users u ON u.id = ns.user_id
	LEFT JOIN pg_timezone_names tz ON tz.name = u.timezone
	WHERE ns.trip_reminders
	  AND EXISTS (SELECT 1 FROM push_devices d WHERE d.user_id = ns.user_id)
)
SELECT DISTINCT ON (l.user_id)
	l.user_id, t.id, day.id,
	COALESCE(NULLIF(day.city_name, ''), t.city_name),
	COALESCE(day.city_lat, anyday.city_lat), COALESCE(day.city_lon, anyday.city_lon),
	l.local_ts::date, EXTRACT(HOUR FROM l.local_ts)::int,
	(l.local_ts::date - t.start_date) + 1
FROM l
JOIN trips t ON t.user_id = l.user_id
	AND t.start_date <= l.local_ts::date AND t.end_date >= l.local_ts::date
LEFT JOIN trip_days day ON day.trip_id = t.id
	AND day.day_number = (l.local_ts::date - t.start_date) + 1
LEFT JOIN LATERAL (
	SELECT d2.city_lat, d2.city_lon FROM trip_days d2
	WHERE d2.trip_id = t.id AND d2.city_lat IS NOT NULL AND d2.city_lon IS NOT NULL
	ORDER BY d2.day_number LIMIT 1
) anyday ON TRUE
WHERE EXTRACT(HOUR FROM l.local_ts) >= $2 AND EXTRACT(HOUR FROM l.local_ts) < $3
  AND NOT EXISTS (
	SELECT 1 FROM trip_brief_log b
	WHERE b.user_id = l.user_id AND b.local_date = l.local_ts::date AND b.kind = $4)
  AND ($4 <> 'weather' OR EXISTS (
	SELECT 1 FROM trip_brief_log m
	WHERE m.user_id = l.user_id AND m.local_date = l.local_ts::date AND m.kind = 'morning'))
ORDER BY l.user_id, t.start_date DESC, t.created_at DESC
LIMIT $5`

// Due lists up to limit travellers due a brief of kind at now, whose local
// hour is in [fromHour, toHour).
func (r *Repository) Due(ctx context.Context, now time.Time, kind string, fromHour, toHour, limit int) ([]Candidate, error) {
	rows, err := r.db.Query(ctx, dueSQL, now, fromHour, toHour, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("due briefs: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.UserID, &c.TripID, &c.DayID, &c.City, &c.Lat, &c.Lon,
			&c.LocalDate, &c.LocalHour, &c.DayNumber); err != nil {
			return nil, fmt.Errorf("scan due brief: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Claim records that c's brief of kind is being sent today. false means it
// already was (another replica, or an earlier tick).
func (r *Repository) Claim(ctx context.Context, c Candidate, kind string, precip *float32) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO trip_brief_log (user_id, trip_id, local_date, kind, precip_prob)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING`, c.UserID, c.TripID, c.LocalDate, kind, precip)
	if err != nil {
		return false, fmt.Errorf("claim brief: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MorningPrecip is the rain chance the morning brief saw, when it saw one.
func (r *Repository) MorningPrecip(ctx context.Context, userID uuid.UUID, localDate time.Time) (float32, bool, error) {
	var p *float32
	err := r.db.QueryRow(ctx, `
		SELECT precip_prob FROM trip_brief_log
		WHERE user_id = $1 AND local_date = $2 AND kind = 'morning'`, userID, localDate).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && p == nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("morning precip: %w", err)
	}
	return *p, true, nil
}

// Stops are a day's stops in order, with their place's stored opening hours
// when the stop names a known place.
func (r *Repository) Stops(ctx context.Context, dayID uuid.UUID) ([]Stop, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.name, s.start_minute, COALESCE(p.opening_hours::text, '')
		FROM trip_stops s
		LEFT JOIN points_of_interest p ON p.id = CASE
			WHEN s.poi_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN s.poi_id::uuid END
		WHERE s.day_id = $1
		ORDER BY s.order_index`, dayID)
	if err != nil {
		return nil, fmt.Errorf("day stops: %w", err)
	}
	defer rows.Close()
	var out []Stop
	for rows.Next() {
		var s Stop
		if err := rows.Scan(&s.Name, &s.StartMinute, &s.OpeningHours); err != nil {
			return nil, fmt.Errorf("scan stop: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
