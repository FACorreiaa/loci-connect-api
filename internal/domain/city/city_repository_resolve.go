package city

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolation is the PostgreSQL SQLSTATE for a unique constraint breach.
const uniqueViolation = "23505"

// FindCityCandidates returns the plausible matches for a typed name, best first.
//
// It replaces FindCityByFuzzyName for resolution — and does not replace it
// elsewhere, because three other callers depend on that method's exact
// behaviour. Two things make this one different, and both are bug fixes:
//
// It returns a *list*, so the caller can apply accent folding and country
// preference in Go rather than accepting whatever one row SQL picked.
//
// And it orders coordinate-bearing rows first. The cities table accumulates
// stub rows with a NULL center_location (the chat stream writes them when the
// model names a city but returns no city_data). With no ORDER BY, which row won
// was whatever the planner emitted first, so a city with both a stub and a good
// row resolved *intermittently* — the worst possible failure, because it looks
// like flakiness rather than a bug.
func (r *RepositoryImpl) FindCityCandidates(ctx context.Context, name string, limit int) ([]locitypes.CityDetail, error) {
	if limit <= 0 {
		limit = 10
	}

	query := `
        SELECT
            id, name, country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE LOWER(name) = LOWER($1) OR similarity(name, $1) > 0.3
        ORDER BY
            (center_location IS NOT NULL) DESC,
            (LOWER(name) = LOWER($1)) DESC,
            similarity(name, $1) DESC,
            name ASC
        LIMIT $2
    `

	rows, err := r.pgpool.Query(ctx, query, name, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query city candidates for '%s': %w", name, err)
	}

	cities, err := pgx.CollectRows(rows, pgx.RowToStructByName[locitypes.CityDetail])
	if err != nil {
		return nil, fmt.Errorf("failed to collect city candidates for '%s': %w", name, err)
	}
	return cities, nil
}

// EnrichCity fills in what a city row is missing, without overwriting what it has.
//
// This is how a stub row is healed in place. Inserting a corrected sibling
// instead would leave two rows for one city, and every POI and itinerary already
// pointing at the stub would stay pointed at the useless one.
func (r *RepositoryImpl) EnrichCity(
	ctx context.Context,
	cityID uuid.UUID,
	lat, lon float64,
	country, stateProvince string,
) error {
	const full = `
        UPDATE cities SET
            center_location = COALESCE(
                center_location,
                ST_SetSRID(ST_MakePoint($2::DOUBLE PRECISION, $3::DOUBLE PRECISION), 4326)
            ),
            country        = CASE WHEN country = 'Unknown' AND $4 <> '' THEN $4 ELSE country END,
            state_province = CASE
                                WHEN (state_province IS NULL OR state_province IN ('Unknown', ''))
                                     AND $5 <> ''
                                THEN $5
                                ELSE state_province
                             END,
            updated_at = NOW()
        WHERE id = $1
    `

	_, err := r.pgpool.Exec(ctx, full, cityID, lon, lat, country, stateProvince)
	if err == nil {
		return nil
	}

	// Promoting country from 'Unknown' to 'Portugal' can collide with an
	// already-canonical row, because uniqueness is (name, state_province,
	// country). The coordinates are the part that actually unblocks compare, so
	// take them and leave the naming alone rather than failing the resolve.
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		return fmt.Errorf("failed to enrich city %s: %w", cityID, err)
	}

	r.logger.WarnContext(ctx, "city enrichment collided with an existing row; keeping coordinates only",
		slog.String("city_id", cityID.String()),
		slog.String("country", country),
		slog.String("state_province", stateProvince))

	const coordsOnly = `
        UPDATE cities SET
            center_location = COALESCE(
                center_location,
                ST_SetSRID(ST_MakePoint($2::DOUBLE PRECISION, $3::DOUBLE PRECISION), 4326)
            ),
            updated_at = NOW()
        WHERE id = $1
    `
	if _, err := r.pgpool.Exec(ctx, coordsOnly, cityID, lon, lat); err != nil {
		return fmt.Errorf("failed to enrich city %s with coordinates: %w", cityID, err)
	}
	return nil
}

// FindCityNear returns the city whose centre is within radiusKm of a point.
//
// Bounded on purpose, unlike GetCity, which orders every row by distance and
// returns the nearest with no ceiling — that one will happily answer "Lisbon"
// for a point in Japan. Used to attach a city_id (and therefore POIs) to a
// request that supplied raw coordinates.
func (r *RepositoryImpl) FindCityNear(ctx context.Context, lat, lon, radiusKm float64) (*locitypes.CityDetail, error) {
	if radiusKm <= 0 {
		return nil, nil
	}

	query := `
        SELECT
            id, name, country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE center_location IS NOT NULL
          AND ST_DWithin(
                center_location::geography,
                ST_SetSRID(ST_MakePoint($1::DOUBLE PRECISION, $2::DOUBLE PRECISION), 4326)::geography,
                $3::DOUBLE PRECISION
              )
        ORDER BY center_location::geography <-> ST_SetSRID(
                    ST_MakePoint($1::DOUBLE PRECISION, $2::DOUBLE PRECISION), 4326
                 )::geography
        LIMIT 1
    `

	rows, err := r.pgpool.Query(ctx, query, lon, lat, radiusKm*1000)
	if err != nil {
		return nil, fmt.Errorf("failed to query city near (%f, %f): %w", lat, lon, err)
	}

	city, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[locitypes.CityDetail])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No city within the radius is a valid answer: the point is simply
			// somewhere we hold no row for.
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find city near (%f, %f): %w", lat, lon, err)
	}
	return city, nil
}
