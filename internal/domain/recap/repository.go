package recap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the recaps table plus the reads a recap is built from.
type Repository interface {
	// Candidates are trips with no recap yet whose end (or last walked day)
	// falls on or after since. The caller decides which are due.
	Candidates(ctx context.Context, since, until time.Time, limit int) ([]Candidate, error)
	// TripCandidate is one of userID's trips; ErrNotFound when it is not theirs.
	TripCandidate(ctx context.Context, userID, tripID uuid.UUID) (Candidate, error)
	// Queue adds a pending trip recap; false when the trip already has one.
	Queue(ctx context.Context, c Candidate, start, end time.Time) (bool, error)
	// Requeue puts back in the queue ineligible trip recaps whose trip ended
	// on or after since and that gained a done stop, a visit or a finished
	// day after they were built. Returns how many.
	Requeue(ctx context.Context, since time.Time, limit int) (int, error)
	// Pending is pending recaps whose build_after has passed, oldest first.
	Pending(ctx context.Context, now time.Time, limit int) ([]Row, error)
	// SaveBuilt stores a build. A ready recap keeps its first ready_at, so the
	// refresh window runs from when it first became ready.
	SaveBuilt(ctx context.Context, id uuid.UUID, status Status, payload []byte, reason string) error
	// SaveFailure records a failed attempt; failed marks it given up.
	SaveFailure(ctx context.Context, id uuid.UUID, msg string, retryAt time.Time, failed bool) error
	// UpsertTrip stores a trip recap built on request. It never touches a
	// shared one, and marks the recap notified: someone who asked for it does
	// not need a push about it. Returns the stored row.
	UpsertTrip(ctx context.Context, userID, tripID uuid.UUID, start, end time.Time, status Status, payload []byte, reason string) (Row, error)
	ByID(ctx context.Context, userID, id uuid.UUID) (Row, error)
	ByTrip(ctx context.Context, userID, tripID uuid.UUID) (Row, error)
	// ByShareCode is a shared, ready recap; ErrNotFound otherwise. countView
	// adds one to its view count.
	ByShareCode(ctx context.Context, code string, countView bool) (Row, error)
	// List is userID's ready recaps, newest first, ready before before (zero:
	// from the top).
	List(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]Row, error)
	// Unnotified is ready recaps no one was told about, with what the push
	// needs.
	Unnotified(ctx context.Context, limit int) ([]Notice, error)
	// ClaimNotify marks a recap notified; false when another tick did first.
	ClaimNotify(ctx context.Context, id uuid.UUID) (bool, error)
	// Share sets the share code unless one is set, and returns the one in
	// force.
	Share(ctx context.Context, userID, id uuid.UUID, code string) (string, error)
	Unshare(ctx context.Context, userID, id uuid.UUID) error
	MarkViewed(ctx context.Context, userID, id uuid.UUID) error
	// LoadTripFacts reads everything a trip recap is built from.
	LoadTripFacts(ctx context.Context, userID, tripID uuid.UUID, start, end time.Time) (*TripFacts, error)

	// YearCandidates are travellers active in year with no year recap yet;
	// only, when not nil, limits them to those ids.
	YearCandidates(ctx context.Context, year int, only []uuid.UUID, limit int) ([]YearCandidate, error)
	// QueueYear adds a pending year recap; false when the year has one.
	QueueYear(ctx context.Context, userID uuid.UUID, year int) (bool, error)
	// UpsertYear stores a year recap built on request, like UpsertTrip.
	UpsertYear(ctx context.Context, userID uuid.UUID, year int, status Status, payload []byte, reason string) (Row, error)
	ByYear(ctx context.Context, userID uuid.UUID, year int) (Row, error)
	// Timezone is the traveller's zone; UTC when unknown.
	Timezone(ctx context.Context, userID uuid.UUID) (string, error)
	// LoadYearFacts reads everything a year in review is built from, up to
	// now.
	LoadYearFacts(ctx context.Context, userID uuid.UUID, year int, now time.Time) (*YearFacts, error)
}

// Notice is a ready recap waiting for its push.
type Notice struct {
	RecapID  uuid.UUID
	UserID   uuid.UUID
	Kind     Kind
	TripID   uuid.UUID
	Year     int
	Timezone string
	Viewed   bool
	Payload  []byte
}

type pgRepository struct {
	pool *pgxpool.Pool
}

// NewRepository returns the Postgres repository.
func NewRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

const rowColumns = `id, user_id, kind, trip_id, year, period_start, period_end, status,
	builder_version, payload, ineligible_reason, attempts, ready_at, notified_at, viewed_at,
	share_code, shared_at`

func scanRow(row pgx.Row) (Row, error) {
	var (
		r      Row
		tripID *uuid.UUID
		year   *int16
		code   *string
	)
	err := row.Scan(&r.ID, &r.UserID, &r.Kind, &tripID, &year, &r.PeriodStart, &r.PeriodEnd, &r.Status,
		&r.BuilderVersion, &r.Payload, &r.IneligibleReason, &r.Attempts, &r.ReadyAt, &r.NotifiedAt, &r.ViewedAt,
		&code, &r.SharedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	if err != nil {
		return Row{}, err
	}
	if tripID != nil {
		r.TripID = *tripID
	}
	if year != nil {
		r.Year = int(*year)
	}
	if code != nil {
		r.ShareCode = *code
	}
	return r, nil
}

const candidateSelect = `
	SELECT t.id, t.user_id, COALESCE(up.timezone, 'UTC'), t.start_date, t.end_date,
	       d.first_day, d.last_day, COALESCE(d.days, 0), COALESCE(d.done, 0), d.last_completed
	FROM trips t
	LEFT JOIN user_progress up ON up.user_id = t.user_id
	LEFT JOIN LATERAL (
		SELECT MIN(date) AS first_day, MAX(date) AS last_day, COUNT(*) AS days,
		       COUNT(*) FILTER (WHERE completed_at IS NOT NULL) AS done,
		       MAX(completed_at) AS last_completed
		FROM trip_days WHERE trip_id = t.id
	) d ON TRUE`

func scanCandidate(row pgx.Row) (Candidate, error) {
	var c Candidate
	var days, done int64
	err := row.Scan(&c.TripID, &c.UserID, &c.Timezone, &c.StartDate, &c.EndDate,
		&c.FirstDay, &c.LastDay, &days, &done, &c.LastCompletedAt)
	c.Days, c.DaysDone = int(days), int(done)
	return c, err
}

func (p *pgRepository) Candidates(ctx context.Context, since, until time.Time, limit int) ([]Candidate, error) {
	rows, err := p.pool.Query(ctx, candidateSelect+`
	WHERE NOT EXISTS (SELECT 1 FROM recaps r WHERE r.kind = 1 AND r.trip_id = t.id)
	  AND (t.end_date BETWEEN $1::date AND $2::date
	       OR (t.end_date IS NULL AND d.last_day BETWEEN $1 AND $2)
	       OR d.last_completed >= $1)
	ORDER BY t.id
	LIMIT $3`, since, until, limit)
	if err != nil {
		return nil, fmt.Errorf("recap candidates: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan recap candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *pgRepository) TripCandidate(ctx context.Context, userID, tripID uuid.UUID) (Candidate, error) {
	c, err := scanCandidate(p.pool.QueryRow(ctx, candidateSelect+`
	WHERE t.id = $1 AND t.user_id = $2`, tripID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Candidate{}, ErrNotFound
	}
	if err != nil {
		return Candidate{}, fmt.Errorf("recap trip: %w", err)
	}
	return c, nil
}

func (p *pgRepository) Queue(ctx context.Context, c Candidate, start, end time.Time) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		INSERT INTO recaps (user_id, kind, trip_id, period_start, period_end)
		VALUES ($1, 1, $2, $3, $4)
		ON CONFLICT (trip_id) WHERE kind = 1 DO NOTHING`,
		c.UserID, c.TripID, start, end)
	if err != nil {
		return false, fmt.Errorf("queue recap: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (p *pgRepository) Requeue(ctx context.Context, since time.Time, limit int) (int, error) {
	// updated_at is when the recap was last built, so anything newer is
	// activity the build has not seen. Visits count from when they synced.
	tag, err := p.pool.Exec(ctx, `
		UPDATE recaps SET status = 1, build_after = NOW(), attempts = 0, last_error = '', updated_at = NOW()
		WHERE status = 3 AND id IN (
			SELECT r.id FROM recaps r
			WHERE r.kind = 1 AND r.status = 3 AND r.share_code IS NULL AND r.period_end >= $1::date
			  AND (EXISTS (SELECT 1 FROM trip_stop_marks m
			               WHERE m.trip_id = r.trip_id AND m.status = 2 AND m.marked_at > r.updated_at)
			    OR EXISTS (SELECT 1 FROM user_visited_pois v
			               WHERE v.trip_id = r.trip_id AND v.user_id = r.user_id AND v.created_at > r.updated_at)
			    OR EXISTS (SELECT 1 FROM trip_days d
			               WHERE d.trip_id = r.trip_id AND d.completed_at > r.updated_at))
			ORDER BY r.period_end DESC
			LIMIT $2)`, since, limit)
	if err != nil {
		return 0, fmt.Errorf("requeue recaps: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (p *pgRepository) Pending(ctx context.Context, now time.Time, limit int) ([]Row, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+rowColumns+` FROM recaps
		WHERE status = 1 AND build_after <= $1
		ORDER BY build_after LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("pending recaps: %w", err)
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *pgRepository) SaveBuilt(ctx context.Context, id uuid.UUID, status Status, payload []byte, reason string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE recaps SET status = $2::smallint, payload = $3, ineligible_reason = $4, builder_version = $5,
		       ready_at = CASE WHEN $2::smallint = 2 THEN COALESCE(ready_at, NOW()) ELSE ready_at END,
		       last_error = '', updated_at = NOW()
		WHERE id = $1 AND share_code IS NULL`,
		id, status, payload, reason, BuilderVersion)
	if err != nil {
		return fmt.Errorf("save recap: %w", err)
	}
	return nil
}

func (p *pgRepository) SaveFailure(ctx context.Context, id uuid.UUID, msg string, retryAt time.Time, failed bool) error {
	status := StatusPending
	if failed {
		status = StatusFailed
	}
	_, err := p.pool.Exec(ctx, `
		UPDATE recaps SET attempts = attempts + 1, last_error = $2, build_after = $3, status = $4::smallint, updated_at = NOW()
		WHERE id = $1`, id, truncate(msg, 500), retryAt, status)
	if err != nil {
		return fmt.Errorf("save recap failure: %w", err)
	}
	return nil
}

func (p *pgRepository) UpsertTrip(ctx context.Context, userID, tripID uuid.UUID, start, end time.Time, status Status, payload []byte, reason string) (Row, error) {
	r, err := scanRow(p.pool.QueryRow(ctx, `
		INSERT INTO recaps (user_id, kind, trip_id, period_start, period_end, status, payload,
		                    ineligible_reason, builder_version, ready_at, notified_at)
		VALUES ($1, 1, $2, $3, $4, $5::smallint, $6, $7, $8, CASE WHEN $5::smallint = 2 THEN NOW() END, NOW())
		ON CONFLICT (trip_id) WHERE kind = 1 DO UPDATE SET
			period_start = EXCLUDED.period_start, period_end = EXCLUDED.period_end,
			status = EXCLUDED.status, payload = EXCLUDED.payload,
			ineligible_reason = EXCLUDED.ineligible_reason, builder_version = EXCLUDED.builder_version,
			ready_at = CASE WHEN EXCLUDED.status = 2 THEN COALESCE(recaps.ready_at, NOW()) ELSE recaps.ready_at END,
			notified_at = COALESCE(recaps.notified_at, NOW()),
			attempts = 0, last_error = '', updated_at = NOW()
		WHERE recaps.share_code IS NULL AND recaps.user_id = EXCLUDED.user_id
		RETURNING `+rowColumns,
		userID, tripID, start, end, status, payload, reason, BuilderVersion))
	if errors.Is(err, ErrNotFound) {
		// The row exists but is shared (frozen): return it as it is.
		return p.ByTrip(ctx, userID, tripID)
	}
	if err != nil {
		return Row{}, fmt.Errorf("upsert recap: %w", err)
	}
	return r, nil
}

func (p *pgRepository) ByID(ctx context.Context, userID, id uuid.UUID) (Row, error) {
	return scanRow(p.pool.QueryRow(ctx, `SELECT `+rowColumns+` FROM recaps WHERE id = $1 AND user_id = $2`, id, userID))
}

func (p *pgRepository) ByTrip(ctx context.Context, userID, tripID uuid.UUID) (Row, error) {
	return scanRow(p.pool.QueryRow(ctx, `SELECT `+rowColumns+` FROM recaps
		WHERE kind = 1 AND trip_id = $1 AND user_id = $2`, tripID, userID))
}

func (p *pgRepository) ByShareCode(ctx context.Context, code string, countView bool) (Row, error) {
	if !countView {
		return scanRow(p.pool.QueryRow(ctx, `SELECT `+rowColumns+` FROM recaps
			WHERE share_code = $1 AND status = 2`, code))
	}
	return scanRow(p.pool.QueryRow(ctx, `
		UPDATE recaps SET view_count = view_count + 1
		WHERE share_code = $1 AND status = 2
		RETURNING `+rowColumns, code))
}

func (p *pgRepository) List(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]Row, error) {
	if before.IsZero() {
		before = time.Now().Add(24 * time.Hour)
	}
	rows, err := p.pool.Query(ctx, `SELECT `+rowColumns+` FROM recaps
		WHERE user_id = $1 AND status = 2 AND ready_at < $2
		ORDER BY ready_at DESC LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list recaps: %w", err)
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *pgRepository) Unnotified(ctx context.Context, limit int) ([]Notice, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT r.id, r.user_id, r.kind, COALESCE(r.trip_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(r.year, 0),
		       COALESCE(up.timezone, 'UTC'), r.viewed_at IS NOT NULL, r.payload
		FROM recaps r
		LEFT JOIN user_progress up ON up.user_id = r.user_id
		WHERE r.status = 2 AND r.notified_at IS NULL
		ORDER BY r.ready_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("unnotified recaps: %w", err)
	}
	defer rows.Close()
	var out []Notice
	for rows.Next() {
		var n Notice
		var year int16
		if err := rows.Scan(&n.RecapID, &n.UserID, &n.Kind, &n.TripID, &year, &n.Timezone, &n.Viewed, &n.Payload); err != nil {
			return nil, err
		}
		n.Year = int(year)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *pgRepository) ClaimNotify(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := p.pool.Exec(ctx, `UPDATE recaps SET notified_at = NOW() WHERE id = $1 AND notified_at IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("claim recap push: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (p *pgRepository) Share(ctx context.Context, userID, id uuid.UUID, code string) (string, error) {
	var got string
	err := p.pool.QueryRow(ctx, `
		UPDATE recaps SET share_code = COALESCE(share_code, $3),
		       shared_at = COALESCE(shared_at, NOW()), updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND status = 2
		RETURNING share_code`, id, userID, code).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("share recap: %w", err)
	}
	return got, nil
}

func (p *pgRepository) Unshare(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE recaps SET share_code = NULL, shared_at = NULL, updated_at = NOW()
		WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("unshare recap: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *pgRepository) MarkViewed(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE recaps SET viewed_at = COALESCE(viewed_at, NOW())
		WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("mark recap viewed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// poiJoin matches a TEXT poi id to points_of_interest without casting ids
// that are not UUIDs (CASE fixes the evaluation order; AND does not).
const poiJoin = `points_of_interest p ON p.id = CASE
	WHEN %s ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN %s::uuid END`

func poiOn(col string) string { return fmt.Sprintf(poiJoin, col, col) }

func (p *pgRepository) LoadTripFacts(ctx context.Context, userID, tripID uuid.UUID, start, end time.Time) (*TripFacts, error) {
	f := &TripFacts{TripID: tripID, UserID: userID, PeriodStart: start, PeriodEnd: end}

	var (
		tripCityName string
		tripCityID   *uuid.UUID
	)
	err := p.pool.QueryRow(ctx, `
		SELECT t.title, t.city_name, t.city_id, COALESCE(up.timezone, 'UTC'),
		       COALESCE(NULLIF(u.display_name, ''), u.username::text, ''),
		       COALESCE(u.avatar_url, u.profile_image_url, '')
		FROM trips t
		JOIN users u ON u.id = t.user_id
		LEFT JOIN user_progress up ON up.user_id = t.user_id
		WHERE t.id = $1 AND t.user_id = $2`, tripID, userID).
		Scan(&f.Title, &tripCityName, &tripCityID, &f.Timezone, &f.Owner.DisplayName, &f.Owner.AvatarURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("recap trip facts: %w", err)
	}
	f.Owner.ID = userID
	loc := Location(f.Timezone)
	from := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	to := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)

	mainCityID, err := p.loadCities(ctx, f, tripCityName, tripCityID)
	if err != nil {
		return nil, err
	}
	if err := p.loadDays(ctx, f); err != nil {
		return nil, err
	}
	if err := p.loadPlaces(ctx, f); err != nil {
		return nil, err
	}
	if err := p.pool.QueryRow(ctx, `SELECT COALESCE(SUM(distance_km), 0) FROM trip_legs WHERE trip_id = $1`, tripID).
		Scan(&f.LegsKm); err != nil {
		return nil, fmt.Errorf("recap legs: %w", err)
	}
	err = p.pool.QueryRow(ctx, `SELECT destination_iata FROM trip_flights WHERE trip_id = $1
		ORDER BY depart_date, created_at LIMIT 1`, tripID).Scan(&f.ArrivalIATA)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("recap flights: %w", err)
	}
	if err := p.loadTaste(ctx, f); err != nil {
		return nil, err
	}
	if err := p.loadProgress(ctx, f, from, to); err != nil {
		return nil, err
	}
	if mainCityID != nil {
		if err := p.loadField(ctx, f, *mainCityID); err != nil {
			return nil, err
		}
	}
	if err := p.loadFriends(ctx, f); err != nil {
		return nil, err
	}
	if err := p.loadHighlights(ctx, f, from, to); err != nil {
		return nil, err
	}
	return f, nil
}

func (p *pgRepository) loadCities(ctx context.Context, f *TripFacts, tripCityName string, tripCityID *uuid.UUID) (*uuid.UUID, error) {
	// Coordinates: the cities table, else the traveller's own globe, else the
	// trip's days.
	rows, err := p.pool.Query(ctx, `
		WITH tc AS (
			SELECT order_index, city_name, city_id FROM trip_cities WHERE trip_id = $1
			UNION ALL
			SELECT 0, $3::text, $4::uuid
			WHERE NOT EXISTS (SELECT 1 FROM trip_cities WHERE trip_id = $1) AND $3::text <> ''
		)
		SELECT tc.city_name, tc.city_id,
		       COALESCE(NULLIF(c.country, ''), uvc.country, ''),
		       COALESCE(uvc.country_code, ''),
		       COALESCE(ST_Y(c.center_location), uvc.latitude, dd.lat),
		       COALESCE(ST_X(c.center_location), uvc.longitude, dd.lon)
		FROM tc
		LEFT JOIN cities c ON c.id = tc.city_id
		LEFT JOIN LATERAL (
			SELECT country, country_code, latitude, longitude FROM user_visited_cities
			WHERE user_id = $2 AND lower(city_name) = lower(tc.city_name)
			ORDER BY last_visit_at DESC LIMIT 1
		) uvc ON TRUE
		LEFT JOIN LATERAL (
			SELECT AVG(city_lat) AS lat, AVG(city_lon) AS lon FROM trip_days
			WHERE trip_id = $1 AND lower(city_name) = lower(tc.city_name) AND city_lat IS NOT NULL
		) dd ON TRUE
		ORDER BY tc.order_index`, f.TripID, f.UserID, tripCityName, tripCityID)
	if err != nil {
		return nil, fmt.Errorf("recap cities: %w", err)
	}
	defer rows.Close()
	var main *uuid.UUID
	for rows.Next() {
		var (
			c        City
			id       *uuid.UUID
			lat, lon *float64
		)
		if err := rows.Scan(&c.Name, &id, &c.Country, &c.CountryCode, &lat, &lon); err != nil {
			return nil, fmt.Errorf("scan recap city: %w", err)
		}
		if lat != nil && lon != nil {
			c.Lat, c.Lon, c.HasCoords = *lat, *lon, true
		}
		if len(f.Cities) == 0 {
			main = id
		}
		f.Cities = append(f.Cities, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if main == nil {
		main = tripCityID
	}
	return main, nil
}

func (p *pgRepository) loadDays(ctx context.Context, f *TripFacts) error {
	rows, err := p.pool.Query(ctx, `
		SELECT d.day_number, d.date, d.city_name, d.completed_at IS NOT NULL,
		       COUNT(s.id), COUNT(m.stop_id) FILTER (WHERE m.status = 2)
		FROM trip_days d
		LEFT JOIN trip_stops s ON s.day_id = d.id
		LEFT JOIN trip_stop_marks m ON m.stop_id = s.id
		WHERE d.trip_id = $1
		GROUP BY d.id
		ORDER BY d.day_number`, f.TripID)
	if err != nil {
		return fmt.Errorf("recap days: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d Day
		var total, done int64
		if err := rows.Scan(&d.Number, &d.Date, &d.CityName, &d.Completed, &total, &done); err != nil {
			return fmt.Errorf("scan recap day: %w", err)
		}
		d.StopsTotal, d.StopsDone = int(total), int(done)
		f.StopsPlanned += d.StopsTotal
		f.StopsDone += d.StopsDone
		f.Days = append(f.Days, d)
	}
	return rows.Err()
}

func (p *pgRepository) loadPlaces(ctx context.Context, f *TripFacts) error {
	rows, err := p.pool.Query(ctx, `
		SELECT v.poi_id, COALESCE(NULLIF(v.poi_name, ''), p.name, ''), COALESCE(p.neighborhood, ''),
		       COALESCE(p.category, ''), v.city_name,
		       COALESCE(v.latitude, ST_Y(p.location)), COALESCE(v.longitude, ST_X(p.location)), v.visited_at
		FROM user_visited_pois v
		LEFT JOIN `+poiOn("v.poi_id")+`
		WHERE v.user_id = $1 AND v.trip_id = $2
		UNION ALL
		SELECT s.poi_id, s.name, COALESCE(p.neighborhood, ''), COALESCE(p.category, ''), d.city_name,
		       ST_Y(p.location), ST_X(p.location), m.marked_at
		FROM trip_stop_marks m
		JOIN trip_stops s ON s.id = m.stop_id
		JOIN trip_days d ON d.id = s.day_id
		LEFT JOIN `+poiOn("s.poi_id")+`
		WHERE m.trip_id = $2 AND m.status = 2`, f.UserID, f.TripID)
	if err != nil {
		return fmt.Errorf("recap places: %w", err)
	}
	defer rows.Close()
	index := map[string]int{}
	for rows.Next() {
		var (
			pl       Place
			lat, lon *float64
		)
		if err := rows.Scan(&pl.POIID, &pl.Name, &pl.Neighborhood, &pl.Category, &pl.CityName, &lat, &lon, &pl.At); err != nil {
			return fmt.Errorf("scan recap place: %w", err)
		}
		if lat != nil && lon != nil {
			pl.Lat, pl.Lon, pl.HasCoords = *lat, *lon, true
		}
		mergePlace(&f.Places, index, placeKey(pl), pl)
	}
	return rows.Err()
}

// placeKey identifies a place across visits and stop marks.
func placeKey(pl Place) string {
	if pl.POIID != "" {
		return pl.POIID
	}
	return "name:" + strings.ToLower(strings.TrimSpace(pl.Name))
}

// mergePlace adds pl, or folds it into the place already under key: one
// place visited twice, or visited and marked, keeps the first time and fills
// in what the other row knew.
func mergePlace(places *[]Place, index map[string]int, key string, pl Place) {
	i, ok := index[key]
	if !ok {
		index[key] = len(*places)
		*places = append(*places, pl)
		return
	}
	cur := &(*places)[i]
	if pl.At.Before(cur.At) {
		cur.At = pl.At
	}
	if !cur.HasCoords && pl.HasCoords {
		cur.Lat, cur.Lon, cur.HasCoords = pl.Lat, pl.Lon, true
	}
	if cur.Neighborhood == "" {
		cur.Neighborhood = pl.Neighborhood
	}
}

func (p *pgRepository) loadTaste(ctx context.Context, f *TripFacts) error {
	rows, err := p.pool.Query(ctx, `
		SELECT e.trait_key, t.label, SUM(e.weight), COUNT(*),
		       COUNT(*) FILTER (WHERE fb.event IN ('saved', 'favorited')),
		       COUNT(*) FILTER (WHERE fb.event = 'visited'),
		       t.score, t.confidence, t.evidence_count
		FROM taste_trait_evidence e
		JOIN preference_feedback fb ON fb.id = e.feedback_id
		JOIN user_taste_traits t ON t.user_id = e.user_id AND t.trait_key = e.trait_key
		WHERE e.user_id = $1 AND fb.trip_id = $2
		GROUP BY e.trait_key, t.label, t.score, t.confidence, t.evidence_count`, f.UserID, f.TripID)
	if err != nil {
		return fmt.Errorf("recap trip taste: %w", err)
	}
	for rows.Next() {
		var t Trait
		var n, saves, visits int64
		if err := rows.Scan(&t.Key, &t.Label, &t.Weight, &n, &saves, &visits, &t.Score, &t.Confidence, &t.TotalEvidence); err != nil {
			rows.Close()
			return fmt.Errorf("scan recap trait: %w", err)
		}
		t.Evidence, t.Saves, t.Visits = int(n), int(saves), int(visits)
		f.TripTaste = append(f.TripTaste, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = p.pool.Query(ctx, `
		SELECT trait_key, label, score, confidence, evidence_count FROM user_taste_traits
		WHERE user_id = $1 AND score > 0
		ORDER BY score * confidence DESC LIMIT 6`, f.UserID)
	if err != nil {
		return fmt.Errorf("recap taste: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t Trait
		if err := rows.Scan(&t.Key, &t.Label, &t.Score, &t.Confidence, &t.TotalEvidence); err != nil {
			return fmt.Errorf("scan recap trait: %w", err)
		}
		f.OverallTaste = append(f.OverallTaste, t)
	}
	return rows.Err()
}

func (p *pgRepository) loadProgress(ctx context.Context, f *TripFacts, from, to time.Time) error {
	rows, err := p.pool.Query(ctx, `
		SELECT local_date, SUM(points) FROM points_events
		WHERE user_id = $1 AND local_date BETWEEN $2::date AND $3::date
		GROUP BY local_date`, f.UserID, f.PeriodStart, f.PeriodEnd)
	if err != nil {
		return fmt.Errorf("recap points: %w", err)
	}
	for rows.Next() {
		var d time.Time
		var pts int64
		if err := rows.Scan(&d, &pts); err != nil {
			rows.Close()
			return fmt.Errorf("scan recap points: %w", err)
		}
		f.Points += pts
		if pts > 0 {
			f.ActiveDates = append(f.ActiveDates, d)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = p.pool.Query(ctx, `
		SELECT badge_id FROM user_badges
		WHERE user_id = $1 AND awarded_at >= $2 AND awarded_at < $3
		ORDER BY awarded_at`, f.UserID, from, to)
	if err != nil {
		return fmt.Errorf("recap badges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan recap badge: %w", err)
		}
		f.Badges = append(f.Badges, id)
	}
	return rows.Err()
}

func (p *pgRepository) loadField(ctx context.Context, f *TripFacts, cityID uuid.UUID) error {
	var fd Field
	var kept, days int64
	err := p.pool.QueryRow(ctx, `
		SELECT COALESCE(t.score, 0), COALESCE(s.kept, 0), COALESCE(s.days, 0)
		FROM (SELECT 1) one
		LEFT JOIN field_city_totals t ON t.user_id = $1 AND t.city_id = $2
		LEFT JOIN LATERAL (
			SELECT SUM(places_kept) AS kept, SUM(days_finished) AS days
			FROM field_season_scores WHERE user_id = $1 AND city_id = $2
		) s ON TRUE`, f.UserID, cityID).Scan(&fd.Score, &kept, &days)
	if err != nil {
		return fmt.Errorf("recap field: %w", err)
	}
	if fd.Score <= 0 {
		return nil
	}
	fd.CityID = cityID
	fd.PlacesKept, fd.DaysFinished = int(kept), int(days)
	if len(f.Cities) > 0 {
		fd.CityName = f.Cities[0].Name
	}
	f.Field = &fd
	return nil
}

// Trip visibility values from the trip proto: FRIENDS and PUBLIC are the ones
// a friend may see; PRIVATE and LINK are not.
const (
	visibilityFriends = 2
	visibilityPublic  = 4
)

func (p *pgRepository) loadFriends(ctx context.Context, f *TripFacts) error {
	if len(f.Cities) == 0 || f.Cities[0].Name == "" {
		return nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT u.id, COALESCE(NULLIF(u.display_name, ''), u.username::text, ''),
		       COALESCE(u.avatar_url, u.profile_image_url, '')
		FROM friendships fr
		JOIN users u ON u.id = fr.friend_id
		WHERE fr.user_id = $1
		  AND EXISTS (
			SELECT 1 FROM trips t2
			WHERE t2.user_id = fr.friend_id AND t2.visibility IN ($3, $4)
			  AND (lower(t2.city_name) = lower($2)
			       OR EXISTS (SELECT 1 FROM trip_cities c WHERE c.trip_id = t2.id AND lower(c.city_name) = lower($2)))
		  )
		ORDER BY fr.created_at
		LIMIT 20`, f.UserID, f.Cities[0].Name, visibilityFriends, visibilityPublic)
	if err != nil {
		return fmt.Errorf("recap friends: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var fr Friend
		if err := rows.Scan(&fr.ID, &fr.DisplayName, &fr.AvatarURL); err != nil {
			return fmt.Errorf("scan recap friend: %w", err)
		}
		f.Friends = append(f.Friends, fr)
	}
	return rows.Err()
}

func (p *pgRepository) loadHighlights(ctx context.Context, f *TripFacts, from, to time.Time) error {
	cities := make([]string, 0, len(f.Cities))
	for _, c := range f.Cities {
		cities = append(cities, strings.ToLower(c.Name))
	}
	rows, err := p.pool.Query(ctx, `
		SELECT fav.item_id, fav.item_name, COALESCE(fav.category, ''), COALESCE(r.rating, 0), 1
		FROM user_favorites fav
		LEFT JOIN reviews r ON r.user_id = fav.user_id AND r.poi_id::text = fav.item_id
		WHERE fav.user_id = $1 AND fav.added_at >= $2 AND fav.added_at < $3
		  AND fav.content_type <> 'itinerary' AND lower(COALESCE(fav.city_name, '')) = ANY($4)
		UNION ALL
		SELECT r.poi_id::text, p.name, COALESCE(p.category, ''), r.rating, 2
		FROM reviews r
		JOIN points_of_interest p ON p.id = r.poi_id
		WHERE r.user_id = $1 AND r.created_at >= $2 AND r.created_at < $3
		ORDER BY 5, 4 DESC
		LIMIT 10`, f.UserID, from, to, cities)
	if err != nil {
		return fmt.Errorf("recap highlights: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h HighlightFact
		var reason int
		if err := rows.Scan(&h.POIID, &h.Name, &h.Category, &h.Rating, &reason); err != nil {
			return fmt.Errorf("scan recap highlight: %w", err)
		}
		h.Reason = HighlightReason(reason)
		f.Highlights = append(f.Highlights, h)
	}
	return rows.Err()
}
