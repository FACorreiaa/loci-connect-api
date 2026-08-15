package travelhistory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// backfillVersion is bumped when the derivation rules change, so a user can be
// re-derived deliberately rather than by deleting rows.
const backfillVersion = 1

// Caps on how much history one lazy backfill will read. Beyond these we log and
// stop rather than silently truncating: a user with 6,000 confirmed visits
// should know their history is partial, not quietly get 5,000 of them.
const (
	maxBackfillEvents = 5000
	maxBackfillTrips  = 500
)

// EnsureBackfilled derives travel history from signals that predate this domain,
// once per user.
//
// It runs lazily on first read rather than as a migration script: that
// self-heals for users created before the feature, avoids a big-bang pass over
// every account at deploy time, and means a failure affects one user's first
// request instead of the whole rollout.
//
// Returns whether the user has been backfilled (true once the pass has
// completed, even if it found nothing — "we looked and there was nothing" is a
// different answer from "we have not looked", and the client renders them
// differently).
func (r *repository) EnsureBackfilled(ctx context.Context, userID uuid.UUID) (bool, error) {
	var done bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM travel_history_backfills
			WHERE user_id = $1 AND version >= $2
		)`, userID, backfillVersion).Scan(&done); err != nil {
		return false, fmt.Errorf("check backfill state: %w", err)
	}
	if done {
		return true, nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin backfill: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Take the marker row first. Two concurrent first-reads for the same user
	// would otherwise both run the derivation; the loser blocks here and finds
	// the work already done when it retries.
	tag, err := tx.Exec(ctx, `
		INSERT INTO travel_history_backfills (user_id, version, ran_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET version = $2, ran_at = NOW()
		WHERE travel_history_backfills.version < $2`, userID, backfillVersion)
	if err != nil {
		return false, fmt.Errorf("claim backfill: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Another request claimed it between our check and our insert.
		return true, nil
	}

	visits, err := r.collectVisitEvents(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	tripVisits, err := r.collectTripVisits(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	visits = append(visits, tripVisits...)

	written := 0
	for _, in := range visits {
		in.Normalise(r.now())
		if err := in.Validate(); err != nil {
			// Not an error: a signal we cannot place is simply not evidence of a
			// visit. Skipping is the honest outcome.
			continue
		}
		if _, err := upsertVisitedCity(ctx, tx, userID, in); err != nil {
			return false, err
		}
		if in.POIID != "" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO user_visited_pois (
					user_id, poi_id, poi_name, city_name, latitude, longitude,
					trip_id, source, visited_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (user_id, poi_id, visited_at) DO NOTHING`,
				userID, in.POIID, in.POIName, in.CityName,
				in.Latitude, in.Longitude, in.TripID, string(in.Source), in.VisitedAt,
			); err != nil {
				return false, fmt.Errorf("backfill visited poi: %w", err)
			}
		}
		written++
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit backfill: %w", err)
	}

	r.logger.Info("travel history backfilled",
		slog.String("user_id", userID.String()),
		slog.Int("candidates", len(visits)),
		slog.Int("written", written))
	return true, nil
}

// collectVisitEvents derives visits from confirmed-visit and rating events.
//
// event_type is stored lower-cased with the enum prefix stripped — see the
// INSERT in internal/domain/recommendation/handler.go, which writes
// enumValue(eventType.String(), "RECOMMENDATION_EVENT_TYPE_"). So
// RECOMMENDATION_EVENT_TYPE_VISIT_CONFIRMED lands as 'visit_confirmed'.
//
// 'rated' is included because learningEvent() in that same file already treats
// RATED as evidence of a visit (both map to the "visited" preference signal).
// Excluding it here would make this domain disagree with the rest of the system
// about what counts as having been somewhere.
func (r *repository) collectVisitEvents(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]VisitInput, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.poi_id, e.trip_id, e.occurred_at,
		       p.name, ST_Y(p.location), ST_X(p.location),
		       p.city_id, c.name, c.country
		FROM recommendation_events e
		LEFT JOIN points_of_interest p ON p.id::text = e.poi_id
		LEFT JOIN cities c ON c.id = p.city_id
		WHERE e.user_id = $1
		  AND e.event_type IN ('visit_confirmed', 'rated')
		  AND e.poi_id IS NOT NULL AND e.poi_id <> ''
		ORDER BY e.occurred_at ASC
		LIMIT $2`, userID, maxBackfillEvents+1)
	if err != nil {
		return nil, fmt.Errorf("read visit events: %w", err)
	}
	defer rows.Close()

	out := make([]VisitInput, 0, 64)
	for rows.Next() {
		var (
			poiID      string
			tripID     *uuid.UUID
			occurredAt time.Time
			poiName    *string
			lat, lon   *float64
			cityID     *uuid.UUID
			cityName   *string
			country    *string
		)
		if err := rows.Scan(&poiID, &tripID, &occurredAt, &poiName, &lat, &lon,
			&cityID, &cityName, &country); err != nil {
			return nil, fmt.Errorf("scan visit event: %w", err)
		}
		// No coordinates means we cannot place it. Record nothing.
		if lat == nil || lon == nil {
			continue
		}
		in := VisitInput{
			CityID:    cityID,
			Latitude:  *lat,
			Longitude: *lon,
			Source:    SourceBackfill,
			TripID:    tripID,
			VisitedAt: occurredAt,
			POIID:     poiID,
		}
		if poiName != nil {
			in.POIName = *poiName
		}
		if cityName != nil {
			in.CityName = *cityName
		}
		if country != nil {
			in.Country = *country
		}
		if in.CityName == "" {
			// A stop we can place but cannot attribute to a city is a POI visit,
			// not a city visit. Skip rather than invent a city name.
			continue
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate visit events: %w", err)
	}
	if len(out) > maxBackfillEvents {
		r.logger.Warn("travel history backfill truncated visit events",
			slog.String("user_id", userID.String()),
			slog.Int("cap", maxBackfillEvents))
		out = out[:maxBackfillEvents]
	}
	return out, nil
}

// collectTripVisits derives visits from saved trips.
//
// The load-bearing predicate is `d.date IS NOT NULL AND d.date < NOW()`. A trip
// day carries an OPTIONAL date (nil for a relative "Day N" plan), and a Trip has
// no start/end date at all. For an undated trip we therefore infer nothing — a
// plan is not a visit, and a future trip is certainly not one.
//
// That restraint is the entire reason this domain exists rather than another
// placeholder: it is better to under-report where someone has been than to
// report somewhere they have not.
func (r *repository) collectTripVisits(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]VisitInput, error) {
	rows, err := tx.Query(ctx, `
		SELECT t.id, d.date,
		       COALESCE(NULLIF(d.city_name, ''), t.city_name) AS city_name,
		       COALESCE(d.city_id, t.city_id)                 AS city_id,
		       d.city_lat, d.city_lon,
		       ST_Y(c.center_location), ST_X(c.center_location),
		       c.country
		FROM trip_days d
		JOIN trips t ON t.id = d.trip_id
		LEFT JOIN cities c ON c.id = COALESCE(d.city_id, t.city_id)
		WHERE t.user_id = $1
		  AND d.date IS NOT NULL
		  AND d.date < NOW()
		ORDER BY d.date ASC
		LIMIT $2`, userID, maxBackfillTrips*20)
	if err != nil {
		return nil, fmt.Errorf("read trip days: %w", err)
	}
	defer rows.Close()

	out := make([]VisitInput, 0, 64)
	seenTrips := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var (
			tripID           uuid.UUID
			date             time.Time
			cityName         *string
			cityID           *uuid.UUID
			dayLat, dayLon   *float64
			cityLat, cityLon *float64
			country          *string
		)
		if err := rows.Scan(&tripID, &date, &cityName, &cityID,
			&dayLat, &dayLon, &cityLat, &cityLon, &country); err != nil {
			return nil, fmt.Errorf("scan trip day: %w", err)
		}
		seenTrips[tripID] = struct{}{}
		if len(seenTrips) > maxBackfillTrips {
			r.logger.Warn("travel history backfill truncated trips",
				slog.String("user_id", userID.String()),
				slog.Int("cap", maxBackfillTrips))
			break
		}
		if cityName == nil || *cityName == "" {
			continue
		}
		// Prefer the day's own coordinates; fall back to the city centroid.
		lat, lon := dayLat, dayLon
		if lat == nil || lon == nil {
			lat, lon = cityLat, cityLon
		}
		if lat == nil || lon == nil {
			continue
		}
		in := VisitInput{
			CityID:    cityID,
			CityName:  *cityName,
			Latitude:  *lat,
			Longitude: *lon,
			Source:    SourceBackfill,
			TripID:    &tripID,
			VisitedAt: date,
		}
		if country != nil {
			in.Country = *country
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trip days: %w", err)
	}
	return out, nil
}
