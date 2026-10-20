package recap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The year queries read with UTC bounds a day wider than the year on each
// side; yearScope then keeps what falls in the year in the traveller's zone.
type yearScope struct {
	year     int
	loc      *time.Location
	from, to time.Time // local midnight of 1 Jan and of 1 Jan the year after
	today    time.Time // local date; nothing after it counts
}

func newYearScope(year int, tz string, now time.Time) yearScope {
	loc := Location(tz)
	return yearScope{
		year:  year,
		loc:   loc,
		from:  time.Date(year, time.January, 1, 0, 0, 0, 0, loc),
		to:    time.Date(year+1, time.January, 1, 0, 0, 0, 0, loc),
		today: LocalDate(now, loc),
	}
}

// in reports whether t is in the year, locally, and not after today.
func (s yearScope) in(t time.Time) bool {
	d := LocalDate(t, s.loc)
	return d.Year() == s.year && !d.After(s.today)
}

// inDate is in for a DATE column's value.
func (s yearScope) inDate(d time.Time) bool {
	d = dateOnly(d)
	return d.Year() == s.year && !d.After(s.today)
}

func (p *pgRepository) YearCandidates(ctx context.Context, year int, only []uuid.UUID, limit int) ([]YearCandidate, error) {
	from := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	to := time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	rows, err := p.pool.Query(ctx, `
		SELECT u.id, COALESCE(up.timezone, 'UTC')
		FROM users u
		LEFT JOIN user_progress up ON up.user_id = u.id
		WHERE NOT EXISTS (SELECT 1 FROM recaps r WHERE r.kind = 2 AND r.user_id = u.id AND r.year = $1)
		  AND ($5::uuid[] IS NULL OR u.id = ANY($5))
		  AND (EXISTS (SELECT 1 FROM user_visited_pois v WHERE v.user_id = u.id AND v.visited_at >= $2 AND v.visited_at < $3)
		       OR EXISTS (SELECT 1 FROM trips t WHERE t.user_id = u.id AND t.start_date BETWEEN $2::date AND $3::date))
		ORDER BY u.id
		LIMIT $4`, year, from, to, limit, only)
	if err != nil {
		return nil, fmt.Errorf("year recap candidates: %w", err)
	}
	defer rows.Close()
	var out []YearCandidate
	for rows.Next() {
		var c YearCandidate
		if err := rows.Scan(&c.UserID, &c.Timezone); err != nil {
			return nil, fmt.Errorf("scan year recap candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *pgRepository) QueueYear(ctx context.Context, userID uuid.UUID, year int) (bool, error) {
	start, end := yearPeriod(year)
	tag, err := p.pool.Exec(ctx, `
		INSERT INTO recaps (user_id, kind, year, period_start, period_end)
		VALUES ($1, 2, $2, $3, $4)
		ON CONFLICT (user_id, year) WHERE kind = 2 DO NOTHING`,
		userID, year, start, end)
	if err != nil {
		return false, fmt.Errorf("queue year recap: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (p *pgRepository) UpsertYear(ctx context.Context, userID uuid.UUID, year int, status Status, payload []byte, reason string) (Row, error) {
	start, end := yearPeriod(year)
	r, err := scanRow(p.pool.QueryRow(ctx, `
		INSERT INTO recaps (user_id, kind, year, period_start, period_end, status, payload,
		                    ineligible_reason, builder_version, ready_at, notified_at)
		VALUES ($1, 2, $2, $3, $4, $5::smallint, $6, $7, $8, CASE WHEN $5::smallint = 2 THEN NOW() END, NOW())
		ON CONFLICT (user_id, year) WHERE kind = 2 DO UPDATE SET
			status = EXCLUDED.status, payload = EXCLUDED.payload,
			ineligible_reason = EXCLUDED.ineligible_reason, builder_version = EXCLUDED.builder_version,
			ready_at = CASE WHEN EXCLUDED.status = 2 THEN COALESCE(recaps.ready_at, NOW()) ELSE recaps.ready_at END,
			notified_at = COALESCE(recaps.notified_at, NOW()),
			attempts = 0, last_error = '', updated_at = NOW()
		WHERE recaps.share_code IS NULL
		RETURNING `+rowColumns,
		userID, year, start, end, status, payload, reason, BuilderVersion))
	if errors.Is(err, ErrNotFound) {
		// Shared, so frozen: return it as it is.
		return p.ByYear(ctx, userID, year)
	}
	if err != nil {
		return Row{}, fmt.Errorf("upsert year recap: %w", err)
	}
	return r, nil
}

func (p *pgRepository) ByYear(ctx context.Context, userID uuid.UUID, year int) (Row, error) {
	return scanRow(p.pool.QueryRow(ctx, `SELECT `+rowColumns+` FROM recaps
		WHERE kind = 2 AND user_id = $1 AND year = $2`, userID, year))
}

func (p *pgRepository) Timezone(ctx context.Context, userID uuid.UUID) (string, error) {
	var tz string
	err := p.pool.QueryRow(ctx, `SELECT COALESCE((SELECT timezone FROM user_progress WHERE user_id = $1), 'UTC')`, userID).Scan(&tz)
	if err != nil {
		return "", fmt.Errorf("recap timezone: %w", err)
	}
	return tz, nil
}

func (p *pgRepository) LoadYearFacts(ctx context.Context, userID uuid.UUID, year int, now time.Time) (*YearFacts, error) {
	f := &YearFacts{UserID: userID, Year: year}
	err := p.pool.QueryRow(ctx, `
		SELECT COALESCE(up.timezone, 'UTC'),
		       COALESCE(NULLIF(u.display_name, ''), u.username::text, ''),
		       COALESCE(u.avatar_url, u.profile_image_url, '')
		FROM users u
		LEFT JOIN user_progress up ON up.user_id = u.id
		WHERE u.id = $1`, userID).Scan(&f.Timezone, &f.Owner.DisplayName, &f.Owner.AvatarURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("year recap owner: %w", err)
	}
	f.Owner.ID = userID
	s := newYearScope(year, f.Timezone, now)

	meta, err := p.loadCityMeta(ctx, f.UserID, s)
	if err != nil {
		return nil, err
	}
	agg := newCityAgg(meta)
	if err := p.loadYearTrips(ctx, f, s, agg); err != nil {
		return nil, err
	}
	if err := p.loadYearPlaces(ctx, f, s, agg); err != nil {
		return nil, err
	}
	f.Cities = agg.cities()
	if err := p.loadYearTaste(ctx, f, s); err != nil {
		return nil, err
	}
	if err := p.loadYearProgress(ctx, f, s); err != nil {
		return nil, err
	}
	if err := p.loadContributions(ctx, f, s); err != nil {
		return nil, err
	}
	if err := p.loadPercentile(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// cityMeta is what the traveller's globe and the cities table know about a
// city, keyed by lower-cased name.
type cityMeta struct {
	id                uuid.UUID
	country, code     string
	lat, lon          float64
	hasCoords         bool
	firstCountryVisit bool
}

// A city can have more than one row on the globe (one per source), and not
// every row knows its country. Rows are read most informative first and
// merged, and a country's first visit only counts rows that name a country:
// rows with none would otherwise share one partition and all look new.
func (p *pgRepository) loadCityMeta(ctx context.Context, userID uuid.UUID, s yearScope) (map[string]cityMeta, error) {
	rows, err := p.pool.Query(ctx, `
		WITH v AS (
			SELECT v.*, COALESCE(NULLIF(v.country_code, ''), NULLIF(lower(v.country), '')) AS country_key
			FROM user_visited_cities v WHERE v.user_id = $1
		)
		SELECT lower(v.city_name), v.city_id, COALESCE(NULLIF(c.country, ''), v.country, ''),
		       COALESCE(v.country_code, ''),
		       COALESCE(ST_Y(c.center_location), v.latitude), COALESCE(ST_X(c.center_location), v.longitude),
		       CASE WHEN v.country_key IS NOT NULL
		            THEN MIN(v.first_visit_at) OVER (PARTITION BY v.country_key) END
		FROM v
		LEFT JOIN cities c ON c.id = v.city_id
		ORDER BY lower(v.city_name), (v.country_key IS NOT NULL) DESC, v.last_visit_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("year recap city meta: %w", err)
	}
	defer rows.Close()
	out := map[string]cityMeta{}
	for rows.Next() {
		var (
			key        string
			id         *uuid.UUID
			m          cityMeta
			lat, lon   *float64
			firstVisit *time.Time
		)
		if err := rows.Scan(&key, &id, &m.country, &m.code, &lat, &lon, &firstVisit); err != nil {
			return nil, fmt.Errorf("scan year recap city meta: %w", err)
		}
		if id != nil {
			m.id = *id
		}
		if lat != nil && lon != nil {
			m.lat, m.lon, m.hasCoords = *lat, *lon, true
		}
		m.firstCountryVisit = firstVisit != nil && !firstVisit.Before(s.from)
		cur, dup := out[key]
		if !dup {
			out[key] = m
			continue
		}
		// Fill what the better row did not know.
		if cur.country == "" {
			cur.country = m.country
		}
		if cur.code == "" {
			cur.code, cur.firstCountryVisit = m.code, m.firstCountryVisit
		}
		if cur.id == uuid.Nil {
			cur.id = m.id
		}
		if !cur.hasCoords && m.hasCoords {
			cur.lat, cur.lon, cur.hasCoords = m.lat, m.lon, true
		}
		out[key] = cur
	}
	return out, rows.Err()
}

// cityAgg collects days, places and trips per city.
type cityAgg struct {
	meta  map[string]cityMeta
	order []string
	by    map[string]*cityTally
}

type cityTally struct {
	name     string
	id       *uuid.UUID
	lat, lon float64
	coords   bool
	days     map[time.Time]bool
	places   map[string]bool
	trips    map[uuid.UUID]bool
}

func newCityAgg(meta map[string]cityMeta) *cityAgg {
	return &cityAgg{meta: meta, by: map[string]*cityTally{}}
}

func (a *cityAgg) tally(name string) *cityTally {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	key := strings.ToLower(name)
	t, ok := a.by[key]
	if !ok {
		t = &cityTally{name: name, days: map[time.Time]bool{}, places: map[string]bool{}, trips: map[uuid.UUID]bool{}}
		a.by[key] = t
		a.order = append(a.order, key)
	}
	return t
}

func (a *cityAgg) day(name string, d time.Time, tripID, id *uuid.UUID, lat, lon *float64) {
	t := a.tally(name)
	if t == nil {
		return
	}
	t.days[d] = true
	if tripID != nil {
		t.trips[*tripID] = true
	}
	if t.id == nil && id != nil {
		t.id = id
	}
	if !t.coords && lat != nil && lon != nil {
		t.lat, t.lon, t.coords = *lat, *lon, true
	}
}

func (a *cityAgg) place(name, key string, d time.Time, tripID *uuid.UUID) {
	t := a.tally(name)
	if t == nil {
		return
	}
	t.days[d] = true
	t.places[key] = true
	if tripID != nil {
		t.trips[*tripID] = true
	}
}

func (a *cityAgg) cities() []YearCity {
	out := make([]YearCity, 0, len(a.order))
	for _, key := range a.order {
		t := a.by[key]
		m := a.meta[key]
		c := YearCity{
			ID:                t.id,
			Name:              t.name,
			Country:           m.country,
			CountryCode:       strings.ToUpper(strings.TrimSpace(m.code)),
			Lat:               m.lat,
			Lon:               m.lon,
			HasCoords:         m.hasCoords,
			Days:              len(t.days),
			Places:            len(t.places),
			Trips:             len(t.trips),
			FirstCountryVisit: m.firstCountryVisit,
		}
		if c.ID == nil && m.id != uuid.Nil {
			id := m.id
			c.ID = &id
		}
		if !c.HasCoords && t.coords {
			c.Lat, c.Lon, c.HasCoords = t.lat, t.lon, true
		}
		out = append(out, c)
	}
	return out
}

func (p *pgRepository) loadYearTrips(ctx context.Context, f *YearFacts, s yearScope, agg *cityAgg) error {
	wideFrom, wideTo := s.from.AddDate(0, 0, -1), s.to.AddDate(0, 0, 1)
	rows, err := p.pool.Query(ctx, `
		SELECT t.id, t.city_name, t.start_date, t.end_date, MIN(d.date), COUNT(d.id)
		FROM trips t
		LEFT JOIN trip_days d ON d.trip_id = t.id
		WHERE t.user_id = $1
		GROUP BY t.id
		HAVING COALESCE(t.start_date::timestamptz, MIN(d.date)) >= $2
		   AND COALESCE(t.start_date::timestamptz, MIN(d.date)) < $3`, f.UserID, wideFrom, wideTo)
	if err != nil {
		return fmt.Errorf("year recap trips: %w", err)
	}
	for rows.Next() {
		var (
			tr         YearTrip
			start, end *time.Time
			firstDay   *time.Time
			days       int64
		)
		if err := rows.Scan(&tr.ID, &tr.CityName, &start, &end, &firstDay, &days); err != nil {
			rows.Close()
			return fmt.Errorf("scan year recap trip: %w", err)
		}
		switch {
		case start != nil:
			if !s.inDate(*start) {
				continue
			}
			tr.Start = dateOnly(*start)
		case firstDay != nil:
			if !s.in(*firstDay) {
				continue
			}
			tr.Start = LocalDate(*firstDay, s.loc)
		default:
			continue
		}
		tr.Days = int(days)
		if start != nil && end != nil {
			if n := int(dateOnly(*end).Sub(dateOnly(*start)).Hours()/24) + 1; n > tr.Days {
				tr.Days = n
			}
		}
		tr.Days = max(tr.Days, 1)
		tr.CountryCode = strings.ToUpper(agg.meta[strings.ToLower(strings.TrimSpace(tr.CityName))].code)
		f.Trips = append(f.Trips, tr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Dated trip days in the year say which cities the traveller was in.
	rows, err = p.pool.Query(ctx, `
		SELECT d.trip_id, d.date, COALESCE(NULLIF(d.city_name, ''), t.city_name), d.city_id, d.city_lat, d.city_lon
		FROM trip_days d
		JOIN trips t ON t.id = d.trip_id
		WHERE t.user_id = $1 AND d.date >= $2 AND d.date < $3`, f.UserID, wideFrom, wideTo)
	if err != nil {
		return fmt.Errorf("year recap trip days: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			tripID   uuid.UUID
			date     time.Time
			city     string
			cityID   *uuid.UUID
			lat, lon *float64
		)
		if err := rows.Scan(&tripID, &date, &city, &cityID, &lat, &lon); err != nil {
			return fmt.Errorf("scan year recap trip day: %w", err)
		}
		if !s.in(date) {
			continue
		}
		agg.day(city, LocalDate(date, s.loc), &tripID, cityID, lat, lon)
	}
	return rows.Err()
}

func (p *pgRepository) loadYearPlaces(ctx context.Context, f *YearFacts, s yearScope, agg *cityAgg) error {
	wideFrom, wideTo := s.from.AddDate(0, 0, -1), s.to.AddDate(0, 0, 1)
	rows, err := p.pool.Query(ctx, `
		SELECT v.poi_id, COALESCE(NULLIF(v.poi_name, ''), p.name, ''), COALESCE(p.category, ''), v.city_name,
		       COALESCE(v.latitude, ST_Y(p.location)), COALESCE(v.longitude, ST_X(p.location)), v.visited_at, v.trip_id
		FROM user_visited_pois v
		LEFT JOIN `+poiOn("v.poi_id")+`
		WHERE v.user_id = $1 AND v.visited_at >= $2 AND v.visited_at < $3
		UNION ALL
		SELECT s.poi_id, s.name, COALESCE(p.category, ''), COALESCE(NULLIF(d.city_name, ''), t.city_name),
		       ST_Y(p.location), ST_X(p.location), m.marked_at, t.id
		FROM trip_stop_marks m
		JOIN trip_stops s ON s.id = m.stop_id
		JOIN trip_days d ON d.id = s.day_id
		JOIN trips t ON t.id = m.trip_id
		LEFT JOIN `+poiOn("s.poi_id")+`
		WHERE t.user_id = $1 AND m.status = 2 AND m.marked_at >= $2 AND m.marked_at < $3`,
		f.UserID, wideFrom, wideTo)
	if err != nil {
		return fmt.Errorf("year recap places: %w", err)
	}
	defer rows.Close()
	index := map[string]int{}
	for rows.Next() {
		var (
			pl       Place
			lat, lon *float64
			tripID   *uuid.UUID
		)
		if err := rows.Scan(&pl.POIID, &pl.Name, &pl.Category, &pl.CityName, &lat, &lon, &pl.At, &tripID); err != nil {
			return fmt.Errorf("scan year recap place: %w", err)
		}
		if !s.in(pl.At) {
			continue
		}
		if lat != nil && lon != nil {
			pl.Lat, pl.Lon, pl.HasCoords = *lat, *lon, true
		}
		key := placeKey(pl)
		agg.place(pl.CityName, key, LocalDate(pl.At, s.loc), tripID)
		mergePlace(&f.Places, index, key, pl)
	}
	return rows.Err()
}

func (p *pgRepository) loadYearTaste(ctx context.Context, f *YearFacts, s yearScope) error {
	end := s.to
	if cut := time.Date(s.today.Year(), s.today.Month(), s.today.Day(), 0, 0, 0, 0, s.loc).AddDate(0, 0, 1); cut.Before(end) {
		end = cut
	}
	rows, err := p.pool.Query(ctx, `
		SELECT e.trait_key, t.label,
		       COALESCE(SUM(e.weight) FILTER (WHERE e.occurred_at < $2), 0),
		       COALESCE(SUM(e.weight) FILTER (WHERE e.occurred_at >= $2), 0),
		       COUNT(*) FILTER (WHERE e.occurred_at >= $2)
		FROM taste_trait_evidence e
		JOIN user_taste_traits t ON t.user_id = e.user_id AND t.trait_key = e.trait_key
		WHERE e.user_id = $1 AND e.occurred_at < $3
		GROUP BY e.trait_key, t.label`, f.UserID, s.from, end)
	if err != nil {
		return fmt.Errorf("year recap taste: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t TraitShift
		var n int64
		if err := rows.Scan(&t.Key, &t.Label, &t.Before, &t.Added, &n); err != nil {
			return fmt.Errorf("scan year recap trait: %w", err)
		}
		t.Evidence = int(n)
		f.Taste = append(f.Taste, t)
	}
	return rows.Err()
}

func (p *pgRepository) loadYearProgress(ctx context.Context, f *YearFacts, s yearScope) error {
	start, end := yearPeriod(s.year)
	if s.today.Before(end) {
		end = s.today
	}
	rows, err := p.pool.Query(ctx, `
		SELECT local_date, SUM(points) FROM points_events
		WHERE user_id = $1 AND local_date BETWEEN $2::date AND $3::date
		GROUP BY local_date`, f.UserID, start, end)
	if err != nil {
		return fmt.Errorf("year recap points: %w", err)
	}
	for rows.Next() {
		var d time.Time
		var pts int64
		if err := rows.Scan(&d, &pts); err != nil {
			rows.Close()
			return fmt.Errorf("scan year recap points: %w", err)
		}
		f.Points += pts
		if pts > 0 {
			f.ActiveDates = append(f.ActiveDates, dateOnly(d))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = p.pool.Query(ctx, `
		SELECT badge_id FROM user_badges
		WHERE user_id = $1 AND awarded_at >= $2 AND awarded_at < $3
		ORDER BY awarded_at`, f.UserID, s.from, s.to)
	if err != nil {
		return fmt.Errorf("year recap badges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan year recap badge: %w", err)
		}
		f.Badges = append(f.Badges, id)
	}
	return rows.Err()
}

func (p *pgRepository) loadContributions(ctx context.Context, f *YearFacts, s yearScope) error {
	var reviews, lists, posts, subs int64
	err := p.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM reviews WHERE user_id = $1 AND created_at >= $2 AND created_at < $3),
			(SELECT COUNT(*) FROM lists WHERE user_id = $1 AND is_public AND NOT is_itinerary
			   AND created_at >= $2 AND created_at < $3),
			(SELECT COUNT(*) FROM board_posts WHERE author_id = $1 AND deleted_at IS NULL AND NOT is_ai_generated
			   AND created_at >= $2 AND created_at < $3),
			(SELECT COUNT(*) FROM place_submissions WHERE user_id = $1 AND created_at >= $2 AND created_at < $3)
			+ (SELECT COUNT(*) FROM place_submission_confirmations WHERE user_id = $1 AND created_at >= $2 AND created_at < $3)`,
		f.UserID, s.from, s.to).Scan(&reviews, &lists, &posts, &subs)
	if err != nil {
		return fmt.Errorf("year recap contributions: %w", err)
	}
	f.Contributions = Contributions{Reviews: int(reviews), Lists: int(lists), Posts: int(posts), Submissions: int(subs)}
	return nil
}

// loadPercentile ranks distinct places visited in the (UTC) year against
// every traveller with at least one. Everyone is measured the same way, so
// the traveller's own count here can differ slightly from the places card.
func (p *pgRepository) loadPercentile(ctx context.Context, f *YearFacts) error {
	from := time.Date(f.Year, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 0, 0)
	var active, mine, ahead int64
	err := p.pool.QueryRow(ctx, `
		WITH c AS (
			SELECT user_id, COUNT(DISTINCT poi_id) AS n FROM user_visited_pois
			WHERE visited_at >= $1 AND visited_at < $2
			GROUP BY user_id
		), me AS (SELECT COALESCE((SELECT n FROM c WHERE user_id = $3), 0) AS n)
		SELECT (SELECT COUNT(*) FROM c), (SELECT n FROM me),
		       (SELECT COUNT(*) FROM c WHERE n > (SELECT n FROM me))`,
		from, to, f.UserID).Scan(&active, &mine, &ahead)
	if err != nil {
		return fmt.Errorf("year recap percentile: %w", err)
	}
	if active < minPercentileActive || mine == 0 {
		return nil
	}
	f.Percentile = &Percentile{Places: int(mine), Rank: int(ahead) + 1, Active: int(active)}
	return nil
}
