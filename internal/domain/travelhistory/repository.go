package travelhistory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository persists and reads travel history.
type Repository interface {
	ListVisitedCities(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*VisitedCity, int, error)
	ListVisitedPOIs(ctx context.Context, userID uuid.UUID, cityID *uuid.UUID, limit, offset int) ([]*VisitedPOI, int, error)
	Summary(ctx context.Context, userID uuid.UUID, periodDays int32) (*Summary, error)
	GlobeData(ctx context.Context, userID uuid.UUID, limit int) ([]*VisitedCity, []*GlobeArc, error)
	RecordVisit(ctx context.Context, userID uuid.UUID, in VisitInput) (*VisitedCity, error)
	DeleteVisit(ctx context.Context, userID, id uuid.UUID) error
	// EnsureBackfilled runs the one-time pass over pre-existing signals for this
	// user, if it has not run already. Reports whether history is now populated.
	EnsureBackfilled(ctx context.Context, userID uuid.UUID) (bool, error)
	// CountVisitedCities backs the statistics domain, replacing its placeholder.
	CountVisitedCities(ctx context.Context, userID uuid.UUID) (int32, error)
}

type repository struct {
	db     *pgxpool.Pool
	logger *slog.Logger
	// now is injected so the period arithmetic in Summary is testable without
	// sleeping or freezing the process clock.
	now func() time.Time
}

// NewRepository builds the travel-history repository.
func NewRepository(db *pgxpool.Pool, logger *slog.Logger) Repository {
	return &repository{
		db:     db,
		logger: logger.With(slog.String("component", "travelhistory-repository")),
		now:    time.Now,
	}
}

const visitedCityColumns = `
	id, city_id, city_name, country, country_code, latitude, longitude,
	source, trip_id, first_visit_at, last_visit_at, visit_count`

func scanVisitedCity(row pgx.Row) (*VisitedCity, error) {
	var c VisitedCity
	var source string
	if err := row.Scan(
		&c.ID, &c.CityID, &c.CityName, &c.Country, &c.CountryCode,
		&c.Latitude, &c.Longitude, &source, &c.TripID,
		&c.FirstVisitAt, &c.LastVisitAt, &c.VisitCount,
	); err != nil {
		return nil, err
	}
	c.Source = Source(source)
	return &c, nil
}

func (r *repository) ListVisitedCities(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*VisitedCity, int, error) {
	if limit <= 0 {
		limit = DefaultGlobeLimit
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.db.Query(ctx, `
		SELECT `+visitedCityColumns+`
		FROM user_visited_cities
		WHERE user_id = $1
		ORDER BY last_visit_at DESC, city_name ASC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list visited cities: %w", err)
	}
	defer rows.Close()

	cities := make([]*VisitedCity, 0, limit)
	for rows.Next() {
		c, scanErr := scanVisitedCity(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan visited city: %w", scanErr)
		}
		cities = append(cities, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate visited cities: %w", err)
	}

	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_visited_cities WHERE user_id = $1`, userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count visited cities: %w", err)
	}
	return cities, total, nil
}

func (r *repository) ListVisitedPOIs(ctx context.Context, userID uuid.UUID, cityID *uuid.UUID, limit, offset int) ([]*VisitedPOI, int, error) {
	if limit <= 0 {
		limit = DefaultGlobeLimit
	}
	if offset < 0 {
		offset = 0
	}

	// user_visited_pois stores city_name, not city_id, so a city filter resolves
	// the name first. Matching on the normalised name mirrors the dedupe rule in
	// the uq_user_visited_cities_name index.
	rows, err := r.db.Query(ctx, `
		SELECT p.id, p.poi_id, p.poi_name, p.city_name, p.latitude, p.longitude,
		       p.trip_id, p.source, p.visited_at
		FROM user_visited_pois p
		WHERE p.user_id = $1
		  AND ($2::uuid IS NULL OR LOWER(BTRIM(p.city_name)) = (
		        SELECT LOWER(BTRIM(c.city_name)) FROM user_visited_cities c
		        WHERE c.user_id = $1 AND c.city_id = $2::uuid LIMIT 1))
		ORDER BY p.visited_at DESC
		LIMIT $3 OFFSET $4`, userID, cityID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list visited pois: %w", err)
	}
	defer rows.Close()

	pois := make([]*VisitedPOI, 0, limit)
	for rows.Next() {
		var p VisitedPOI
		var source string
		if scanErr := rows.Scan(
			&p.ID, &p.POIID, &p.POIName, &p.CityName,
			&p.Latitude, &p.Longitude, &p.TripID, &source, &p.VisitedAt,
		); scanErr != nil {
			return nil, 0, fmt.Errorf("scan visited poi: %w", scanErr)
		}
		p.Source = Source(source)
		pois = append(pois, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate visited pois: %w", err)
	}

	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_visited_pois WHERE user_id = $1`, userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count visited pois: %w", err)
	}
	return pois, total, nil
}

func (r *repository) CountVisitedCities(ctx context.Context, userID uuid.UUID) (int32, error) {
	var n int32
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_visited_cities WHERE user_id = $1`, userID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count visited cities: %w", err)
	}
	return n, nil
}

func (r *repository) Summary(ctx context.Context, userID uuid.UUID, periodDays int32) (*Summary, error) {
	if periodDays <= 0 {
		periodDays = DefaultPeriodDays
	}
	windowStart := r.now().AddDate(0, 0, -int(periodDays))

	s := &Summary{PeriodDays: periodDays}

	// Current totals, plus the same totals as they stood at windowStart. The
	// "prev" figures count only what was already known before the window opened,
	// so `current - prev` is genuinely "added during this period" rather than a
	// decorative arrow.
	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(DISTINCT NULLIF(BTRIM(country), '')),
			COUNT(DISTINCT trip_id) FILTER (WHERE trip_id IS NOT NULL),
			MIN(first_visit_at),
			MAX(last_visit_at),
			COUNT(*) FILTER (WHERE first_visit_at < $2),
			COUNT(DISTINCT NULLIF(BTRIM(country), '')) FILTER (WHERE first_visit_at < $2)
		FROM user_visited_cities
		WHERE user_id = $1`, userID, windowStart,
	).Scan(
		&s.CitiesVisited, &s.CountriesVisited, &s.TripsCompleted,
		&s.FirstVisitAt, &s.LastVisitAt,
		&s.CitiesVisitedPrev, &s.CountriesVisitedPrev,
	)
	if err != nil {
		return nil, fmt.Errorf("summarise visited cities: %w", err)
	}

	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE visited_at < $2)
		FROM user_visited_pois WHERE user_id = $1`, userID, windowStart,
	).Scan(&s.POIsVisited, &s.POIsVisitedPrev); err != nil {
		return nil, fmt.Errorf("summarise visited pois: %w", err)
	}

	// DistanceKm is computed in Go rather than SQL so it uses the same haversine
	// the client draws arcs with — a mismatch there would show up as a leg
	// labelled with a distance that does not match its own curve.
	cities, _, err := r.ListVisitedCities(ctx, userID, DefaultGlobeLimit, 0)
	if err != nil {
		return nil, err
	}
	sort.Slice(cities, func(i, j int) bool {
		return cities[i].FirstVisitAt.Before(cities[j].FirstVisitAt)
	})
	s.DistanceKm = TotalDistanceKm(cities)

	return s, nil
}

func (r *repository) GlobeData(ctx context.Context, userID uuid.UUID, limit int) ([]*VisitedCity, []*GlobeArc, error) {
	if limit <= 0 {
		limit = DefaultGlobeLimit
	}

	cities, _, err := r.ListVisitedCities(ctx, userID, limit, 0)
	if err != nil {
		return nil, nil, err
	}

	// Arcs come from real trip legs only. A leg missing any endpoint coordinate
	// is skipped rather than straightened onto a city centroid: half a real arc
	// is a fabricated one.
	rows, err := r.db.Query(ctx, `
		SELECT l.from_name, l.to_name, l.from_lat, l.from_lon, l.to_lat, l.to_lon,
		       l.distance_km, l.trip_id, l.mode, d.date
		FROM trip_legs l
		JOIN trips t ON t.id = l.trip_id
		LEFT JOIN trip_days d ON d.trip_id = l.trip_id AND d.day_number = l.after_day
		WHERE t.user_id = $1
		  AND l.from_lat IS NOT NULL AND l.from_lon IS NOT NULL
		  AND l.to_lat IS NOT NULL AND l.to_lon IS NOT NULL
		ORDER BY COALESCE(d.date, t.created_at) DESC, l.after_day ASC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("list globe arcs: %w", err)
	}
	defer rows.Close()

	arcs := make([]*GlobeArc, 0, limit)
	for rows.Next() {
		var a GlobeArc
		var tripID uuid.UUID
		if scanErr := rows.Scan(
			&a.FromName, &a.ToName, &a.FromLat, &a.FromLon, &a.ToLat, &a.ToLon,
			&a.DistanceKm, &tripID, &a.Mode, &a.OccurredAt,
		); scanErr != nil {
			return nil, nil, fmt.Errorf("scan globe arc: %w", scanErr)
		}
		a.TripID = &tripID
		// Legs written before distance was populated report 0; recompute rather
		// than render a leg claiming to be zero kilometres long.
		if a.DistanceKm <= 0 {
			a.DistanceKm = HaversineKm(a.FromLat, a.FromLon, a.ToLat, a.ToLon)
		}
		arcs = append(arcs, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate globe arcs: %w", err)
	}

	return cities, arcs, nil
}

func (r *repository) RecordVisit(ctx context.Context, userID uuid.UUID, in VisitInput) (*VisitedCity, error) {
	in.Normalise(r.now())
	if err := in.Validate(); err != nil {
		return nil, err
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin record visit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	city, err := upsertVisitedCity(ctx, tx, userID, in)
	if err != nil {
		return nil, err
	}

	if in.POIID != "" {
		// ON CONFLICT DO NOTHING: replaying the same event (same instant, same
		// POI) must not inflate the count. A genuine second visit has a
		// different timestamp.
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_visited_pois (
				user_id, poi_id, poi_name, city_name, latitude, longitude,
				trip_id, source, visited_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (user_id, poi_id, visited_at) DO NOTHING`,
			userID, in.POIID, in.POIName, in.CityName,
			in.Latitude, in.Longitude, in.TripID, string(in.Source), in.VisitedAt,
		); err != nil {
			return nil, fmt.Errorf("record visited poi: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit record visit: %w", err)
	}
	return city, nil
}

// upsertVisitedCity merges a visit into the existing row for that city, widening
// the first/last window and incrementing the count. Split out because the
// backfill needs the same merge semantics inside its own transaction.
func upsertVisitedCity(ctx context.Context, tx pgx.Tx, userID uuid.UUID, in VisitInput) (*VisitedCity, error) {
	// Two partial unique indexes cover this table (resolved vs unresolved city),
	// and ON CONFLICT can only name one. Resolve which one applies first.
	conflict := `(user_id, LOWER(BTRIM(city_name)), LOWER(BTRIM(country))) WHERE city_id IS NULL`
	if in.CityID != nil {
		conflict = `(user_id, city_id) WHERE city_id IS NOT NULL`
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO user_visited_cities (
			user_id, city_id, city_name, country, country_code, latitude, longitude,
			source, trip_id, first_visit_at, last_visit_at, visit_count
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, 1)
		ON CONFLICT `+conflict+` DO UPDATE SET
			first_visit_at = LEAST(user_visited_cities.first_visit_at, EXCLUDED.first_visit_at),
			last_visit_at  = GREATEST(user_visited_cities.last_visit_at, EXCLUDED.last_visit_at),
			visit_count    = user_visited_cities.visit_count + 1,
			-- Only ever fill blanks. A later, less-informed write must not erase
			-- a country or trip we already resolved.
			country      = COALESCE(NULLIF(user_visited_cities.country, ''), EXCLUDED.country),
			country_code = COALESCE(user_visited_cities.country_code, EXCLUDED.country_code),
			city_id      = COALESCE(user_visited_cities.city_id, EXCLUDED.city_id),
			trip_id      = COALESCE(user_visited_cities.trip_id, EXCLUDED.trip_id),
			updated_at   = NOW()
		RETURNING `+visitedCityColumns,
		userID, in.CityID, in.CityName, in.Country, in.CountryCode,
		in.Latitude, in.Longitude, string(in.Source), in.TripID, in.VisitedAt,
	)

	city, err := scanVisitedCity(row)
	if err != nil {
		return nil, fmt.Errorf("upsert visited city: %w", err)
	}
	return city, nil
}

func (r *repository) DeleteVisit(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM user_visited_cities WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete visit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
