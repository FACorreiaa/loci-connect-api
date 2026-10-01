package trip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlanRepository writes a trip's plan: its dates, where it sleeps and its
// flights. These live outside SaveTrip, the way sharing does. A client built
// before them sends a TripDraft without them, and SaveTrip replaces child rows
// from what it is sent, so writing them there would let any old client wipe
// them.
type PlanRepository interface {
	// SavePlan stores t's dates, day dates, stays and flights under the same
	// optimistic lock SaveTrip uses, and returns the trip as stored.
	SavePlan(ctx context.Context, t *Trip, baseVersion int64) (*Trip, error)
}

type planRepository struct {
	db    *pgxpool.Pool
	trips *repository
}

// NewPlanRepository is the Postgres PlanRepository.
func NewPlanRepository(db *pgxpool.Pool, logger *slog.Logger) PlanRepository {
	return &planRepository{
		db:    db,
		trips: &repository{db: db, logger: logger.With(slog.String("component", "trip-plan-repository"))},
	}
}

func (r *planRepository) SavePlan(ctx context.Context, t *Trip, baseVersion int64) (*Trip, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is a no-op after commit

	var stored int64
	err = tx.QueryRow(ctx, `SELECT version FROM trips WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		t.ID, t.UserID).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock trip: %w", err)
	}
	if stored != baseVersion {
		return nil, ErrVersionConflict
	}
	t.Version = stored + 1

	if _, err := tx.Exec(ctx, `
		UPDATE trips SET start_date = $1, end_date = $2, version = $3, updated_at = NOW() WHERE id = $4`,
		t.StartDate, t.EndDate, t.Version, t.ID); err != nil {
		return nil, fmt.Errorf("update trip dates: %w", err)
	}
	// Only day dates move. Ids, stops and order stay as SaveTrip left them,
	// so nothing a client holds (AddToTrip's day choice, iOS reminders keyed
	// by stop) goes stale.
	for _, d := range t.Days {
		if _, err := tx.Exec(ctx, `UPDATE trip_days SET date = $1 WHERE id = $2 AND trip_id = $3`,
			d.Date, d.ID, t.ID); err != nil {
			return nil, fmt.Errorf("date day %d: %w", d.DayNumber, err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM trip_stays WHERE trip_id = $1`, t.ID); err != nil {
		return nil, fmt.Errorf("clear stays: %w", err)
	}
	for _, s := range t.Stays {
		id := s.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trip_stays (id, trip_id, city_name, poi_id, name, star_rating, check_in, check_out, booking_url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			id, t.ID, s.CityName, s.POIID, s.Name, s.StarRating, s.CheckIn, s.CheckOut, s.BookingURL); err != nil {
			return nil, fmt.Errorf("insert stay: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM trip_flights WHERE trip_id = $1`, t.ID); err != nil {
		return nil, fmt.Errorf("clear flights: %w", err)
	}
	for _, f := range t.Flights {
		links := []byte("[]")
		if f.Links != nil {
			if links, err = json.Marshal(f.Links); err != nil {
				return nil, fmt.Errorf("marshal flight links: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trip_flights (id, trip_id, origin_name, origin_iata, destination_name, destination_iata,
				depart_date, return_date, passengers, cabin, links, carrier, flight_no, price_text, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			f.ID, t.ID, f.Origin.Name, f.Origin.IATA, f.Destination.Name, f.Destination.IATA,
			f.DepartDate, f.ReturnDate, f.Passengers, int32(f.Cabin), links,
			f.Carrier, f.FlightNo, f.PriceText, f.Notes); err != nil {
			return nil, fmt.Errorf("insert flight: %w", err)
		}
	}

	if err := insertSnapshot(ctx, tx, t); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return r.trips.GetTrip(ctx, t.ID, t.UserID)
}
