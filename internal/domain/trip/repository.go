package trip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound is returned when a trip does not exist or is not owned by the caller.
	ErrNotFound = errors.New("trip not found")
	// ErrVersionConflict is returned when a SaveTrip base version is stale (a
	// concurrent edit landed first). Callers should reload and merge.
	ErrVersionConflict = errors.New("trip version conflict")
)

// TripConstraint mirrors the proto TripConstraint; persisted as JSONB.
type TripConstraint struct {
	BudgetLevel    *int32   `json:"budget_level,omitempty"`
	Pace           int32    `json:"pace,omitempty"`
	Mobility       string   `json:"mobility,omitempty"`
	Interests      []string `json:"interests,omitempty"`
	DayStartMinute *int32   `json:"day_start_minute,omitempty"`
	DayEndMinute   *int32   `json:"day_end_minute,omitempty"`
}

// RecommendationTrace preserves attribution when a recommendation becomes a trip stop.
type RecommendationTrace struct {
	RunID             string `json:"run_id"`
	ItemID            string `json:"item_id"`
	Rank              int32  `json:"rank"`
	AlgorithmVersion  string `json:"algorithm_version"`
	ExperimentVariant string `json:"experiment_variant"`
	Surface           int32  `json:"surface"`
	Channel           int32  `json:"channel"`
}

// TripStop is a place on a day's timeline.
type TripStop struct {
	ID                  uuid.UUID
	POIID               string
	OrderIndex          int32
	Name                string
	StartMinute         *int32
	DurationMinutes     *int32
	Notes               string
	BookingURL          *string
	RecommendationTrace *RecommendationTrace
}

// TripDay is one day of a trip.
type TripDay struct {
	ID        uuid.UUID
	DayNumber int32
	Date      *time.Time
	Stops     []TripStop

	// Which city this day is spent in. Empty means the trip's primary city,
	// which is how every single-city trip looks.
	CityID    *uuid.UUID
	CityName  string
	CityLat   *float64
	CityLon   *float64
	TravelDay bool
}

// TripLeg is travel between two consecutive cities in a multi-city trip.
//
// AfterDay is the day number at whose end the journey happens (0 = the outbound
// leg from home). It is a plain number rather than a day FK so a leg survives
// days being renumbered mid-edit.
type TripLeg struct {
	ID           uuid.UUID
	AfterDay     int32
	FromName     string
	ToName       string
	FromLat      *float64
	FromLon      *float64
	ToLat        *float64
	ToLon        *float64
	DistanceKm   float64
	DurationMins int32
	Mode         string
	BookingURL   *string
}

// Trip is the full editable trip aggregate.
type Trip struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	CityID          *uuid.UUID
	CityName        string
	Title           string
	Constraints     TripConstraint
	Days            []TripDay
	// Legs is travel between the trip's cities. Empty for a single-city trip.
	Legs            []TripLeg
	Version         int64
	SourceSessionID *string
	IsPublic        bool
	ShareCode       *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Repository persists trips. SaveTrip enforces optimistic concurrency and writes
// an immutable snapshot per version.
type Repository interface {
	GetTrip(ctx context.Context, id, userID uuid.UUID) (*Trip, error)
	ListTrips(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Trip, int, error)
	SaveTrip(ctx context.Context, t *Trip, baseVersion int64) (*Trip, error)
	SetShare(ctx context.Context, id, userID uuid.UUID, isPublic bool, shareCode string) (*Trip, error)
}

type repository struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewRepository(db *pgxpool.Pool, logger *slog.Logger) Repository {
	return &repository{db: db, logger: logger.With(slog.String("component", "trip-repository"))}
}

func (r *repository) GetTrip(ctx context.Context, id, userID uuid.UUID) (*Trip, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, city_id, city_name, title, constraints, version,
		       source_session_id, is_public, share_code, created_at, updated_at
		FROM trips WHERE id = $1 AND user_id = $2`, id, userID)

	t, err := scanTrip(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get trip: %w", err)
	}

	if err := r.loadDays(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (r *repository) ListTrips(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Trip, int, error) {
	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM trips WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count trips: %w", err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, city_id, city_name, title, constraints, version,
		       source_session_id, is_public, share_code, created_at, updated_at
		FROM trips WHERE user_id = $1
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list trips: %w", err)
	}
	defer rows.Close()

	var trips []*Trip
	for rows.Next() {
		t, err := scanTrip(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan trip: %w", err)
		}
		trips = append(trips, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Hydrate days for each trip (list views show day/stop counts).
	for _, t := range trips {
		if err := r.loadDays(ctx, t); err != nil {
			return nil, 0, err
		}
	}
	return trips, total, nil
}

// SaveTrip upserts a trip in a single transaction: it validates the base version,
// bumps the stored version, replaces days+stops, and appends a snapshot.
func (r *repository) SaveTrip(ctx context.Context, t *Trip, baseVersion int64) (*Trip, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is a no-op after commit

	constraintsJSON, err := json.Marshal(t.Constraints)
	if err != nil {
		return nil, fmt.Errorf("marshal constraints: %w", err)
	}

	var newVersion int64
	if t.ID == uuid.Nil {
		// New trip: version starts at 1.
		newVersion = 1
		err = tx.QueryRow(ctx, `
			INSERT INTO trips (user_id, city_id, city_name, title, constraints, version, source_session_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, created_at, updated_at`,
			t.UserID, t.CityID, t.CityName, t.Title, constraintsJSON, newVersion, t.SourceSessionID).
			Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("insert trip: %w", err)
		}
	} else {
		// Existing trip: lock the row and enforce the base version.
		var storedVersion int64
		err = tx.QueryRow(ctx, `SELECT version FROM trips WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			t.ID, t.UserID).Scan(&storedVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("lock trip: %w", err)
		}
		if storedVersion != baseVersion {
			return nil, ErrVersionConflict
		}
		newVersion = storedVersion + 1
		_, err = tx.Exec(ctx, `
			UPDATE trips SET city_id = $1, city_name = $2, title = $3, constraints = $4,
			                 version = $5, updated_at = NOW()
			WHERE id = $6`,
			t.CityID, t.CityName, t.Title, constraintsJSON, newVersion, t.ID)
		if err != nil {
			return nil, fmt.Errorf("update trip: %w", err)
		}
	}
	t.Version = newVersion

	// Replace-all days + stops (simplest correct model for trip-sized data).
	if _, err := tx.Exec(ctx, `DELETE FROM trip_days WHERE trip_id = $1`, t.ID); err != nil {
		return nil, fmt.Errorf("clear days: %w", err)
	}
	for di := range t.Days {
		day := &t.Days[di]
		if err := tx.QueryRow(ctx, `
			INSERT INTO trip_days (trip_id, day_number, date, city_id, city_name, city_lat, city_lon, travel_day)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			t.ID, day.DayNumber, day.Date,
			day.CityID, day.CityName, day.CityLat, day.CityLon, day.TravelDay).Scan(&day.ID); err != nil {
			return nil, fmt.Errorf("insert day: %w", err)
		}
		for si := range day.Stops {
			s := &day.Stops[si]
			traceJSON, marshalErr := json.Marshal(s.RecommendationTrace)
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal recommendation trace: %w", marshalErr)
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO trip_stops (day_id, poi_id, order_index, name, start_minute, duration_minutes, notes, booking_url, recommendation_trace)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
				day.ID, s.POIID, s.OrderIndex, s.Name, s.StartMinute, s.DurationMinutes, s.Notes, s.BookingURL, traceJSON).
				Scan(&s.ID); err != nil {
				return nil, fmt.Errorf("insert stop: %w", err)
			}
		}
	}

	// Legs, replace-all like days for the same reason: trip-sized data, and a
	// partial update is not worth the bookkeeping.
	if _, err := tx.Exec(ctx, `DELETE FROM trip_legs WHERE trip_id = $1`, t.ID); err != nil {
		return nil, fmt.Errorf("clear legs: %w", err)
	}
	for li := range t.Legs {
		leg := &t.Legs[li]
		if err := tx.QueryRow(ctx, `
			INSERT INTO trip_legs (trip_id, after_day, from_name, to_name, from_lat, from_lon, to_lat, to_lon,
				distance_km, duration_mins, mode, booking_url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
			t.ID, leg.AfterDay, leg.FromName, leg.ToName, leg.FromLat, leg.FromLon, leg.ToLat, leg.ToLon,
			leg.DistanceKm, leg.DurationMins, leg.Mode, leg.BookingURL).Scan(&leg.ID); err != nil {
			return nil, fmt.Errorf("insert leg: %w", err)
		}
	}

	// Append an immutable snapshot for merge-safe reconciliation.
	snapshotJSON, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trip_snapshots (trip_id, version, data) VALUES ($1, $2, $3)`,
		t.ID, newVersion, snapshotJSON); err != nil {
		return nil, fmt.Errorf("insert snapshot: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return t, nil
}

func (r *repository) SetShare(ctx context.Context, id, userID uuid.UUID, isPublic bool, shareCode string) (*Trip, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE trips SET is_public = $1, share_code = COALESCE(share_code, $2), updated_at = NOW()
		WHERE id = $3 AND user_id = $4`, isPublic, shareCode, id, userID)
	if err != nil {
		return nil, fmt.Errorf("set share: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.GetTrip(ctx, id, userID)
}

// loadLegs populates t.Legs, ordered along the route.
func (r *repository) loadLegs(ctx context.Context, t *Trip) error {
	rows, err := r.db.Query(ctx, `
		SELECT id, after_day, from_name, to_name, from_lat, from_lon, to_lat, to_lon,
		       distance_km, duration_mins, mode, booking_url
		FROM trip_legs WHERE trip_id = $1 ORDER BY after_day, created_at`, t.ID)
	if err != nil {
		return fmt.Errorf("load legs: %w", err)
	}
	defer rows.Close()

	t.Legs = nil
	for rows.Next() {
		var l TripLeg
		if err := rows.Scan(&l.ID, &l.AfterDay, &l.FromName, &l.ToName,
			&l.FromLat, &l.FromLon, &l.ToLat, &l.ToLon,
			&l.DistanceKm, &l.DurationMins, &l.Mode, &l.BookingURL); err != nil {
			return fmt.Errorf("scan leg: %w", err)
		}
		t.Legs = append(t.Legs, l)
	}
	return rows.Err()
}

// loadDays populates t.Days and their stops, ordered, plus the legs between
// cities — a multi-city trip is not complete without them.
func (r *repository) loadDays(ctx context.Context, t *Trip) error {
	if err := r.loadLegs(ctx, t); err != nil {
		return err
	}
	dayRows, err := r.db.Query(ctx, `
		SELECT id, day_number, date, city_id, city_name, city_lat, city_lon, travel_day
		FROM trip_days WHERE trip_id = $1 ORDER BY day_number`, t.ID)
	if err != nil {
		return fmt.Errorf("load days: %w", err)
	}
	defer dayRows.Close()

	dayByID := map[uuid.UUID]*TripDay{}
	t.Days = nil
	for dayRows.Next() {
		var d TripDay
		if err := dayRows.Scan(&d.ID, &d.DayNumber, &d.Date,
			&d.CityID, &d.CityName, &d.CityLat, &d.CityLon, &d.TravelDay); err != nil {
			return fmt.Errorf("scan day: %w", err)
		}
		t.Days = append(t.Days, d)
	}
	if err := dayRows.Err(); err != nil {
		return err
	}
	for i := range t.Days {
		dayByID[t.Days[i].ID] = &t.Days[i]
	}
	if len(t.Days) == 0 {
		return nil
	}

	stopRows, err := r.db.Query(ctx, `
		SELECT s.id, s.day_id, s.poi_id, s.order_index, s.name, s.start_minute,
		       s.duration_minutes, s.notes, s.booking_url, s.recommendation_trace
		FROM trip_stops s
		JOIN trip_days d ON d.id = s.day_id
		WHERE d.trip_id = $1
		ORDER BY d.day_number, s.order_index`, t.ID)
	if err != nil {
		return fmt.Errorf("load stops: %w", err)
	}
	defer stopRows.Close()

	for stopRows.Next() {
		var (
			s         TripStop
			dayID     uuid.UUID
			traceJSON []byte
		)
		if err := stopRows.Scan(&s.ID, &dayID, &s.POIID, &s.OrderIndex, &s.Name,
			&s.StartMinute, &s.DurationMinutes, &s.Notes, &s.BookingURL, &traceJSON); err != nil {
			return fmt.Errorf("scan stop: %w", err)
		}
		if len(traceJSON) > 0 && string(traceJSON) != "null" {
			s.RecommendationTrace = &RecommendationTrace{}
			if err := json.Unmarshal(traceJSON, s.RecommendationTrace); err != nil {
				return fmt.Errorf("unmarshal recommendation trace: %w", err)
			}
		}
		if d := dayByID[dayID]; d != nil {
			d.Stops = append(d.Stops, s)
		}
	}
	return stopRows.Err()
}

// rowScanner abstracts pgx.Row and pgx.Rows for scanTrip.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrip(row rowScanner) (*Trip, error) {
	var (
		t               Trip
		constraintsJSON []byte
	)
	if err := row.Scan(&t.ID, &t.UserID, &t.CityID, &t.CityName, &t.Title, &constraintsJSON,
		&t.Version, &t.SourceSessionID, &t.IsPublic, &t.ShareCode, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	if len(constraintsJSON) > 0 {
		if err := json.Unmarshal(constraintsJSON, &t.Constraints); err != nil {
			return nil, fmt.Errorf("unmarshal constraints: %w", err)
		}
	}
	return &t, nil
}
