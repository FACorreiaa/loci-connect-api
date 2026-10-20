package linkimport

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Querier is the part of pgxpool.Pool the lookups use.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// FindCity is the existing city with this name. Several countries can share
// a name; the one Loci knows most places in wins.
func FindCity(db Querier) func(ctx context.Context, name string) (uuid.UUID, bool, error) {
	return func(ctx context.Context, name string) (uuid.UUID, bool, error) {
		var id uuid.UUID
		err := db.QueryRow(ctx, `
			SELECT c.id FROM cities c
			WHERE LOWER(c.name) = LOWER($1)
			ORDER BY (SELECT count(*) FROM points_of_interest p WHERE p.city_id = c.id) DESC, c.id
			LIMIT 1`, name).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		if err != nil {
			return uuid.Nil, false, fmt.Errorf("find city %q: %w", name, err)
		}
		return id, true, nil
	}
}

// likeEscaper makes a name literal inside ILIKE.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ByName is the city's places whose name contains, or is close to, this one.
func ByName(db Querier) func(ctx context.Context, cityID uuid.UUID, name string) ([]locitypes.POIDetailedInfo, error) {
	return func(ctx context.Context, cityID uuid.UUID, name string) ([]locitypes.POIDetailedInfo, error) {
		rows, err := db.Query(ctx, `
			SELECT id, name, COALESCE(address, ''), COALESCE(category, poi_type, ''),
			       ST_Y(location), ST_X(location)
			FROM points_of_interest
			WHERE city_id = $1 AND (name % $2 OR name ILIKE $3)
			ORDER BY similarity(name, $2) DESC
			LIMIT 15`, cityID, name, "%"+likeEscaper.Replace(name)+"%")
		if err != nil {
			return nil, fmt.Errorf("places named %q: %w", name, err)
		}
		defer rows.Close()
		var out []locitypes.POIDetailedInfo
		for rows.Next() {
			var p locitypes.POIDetailedInfo
			if err := rows.Scan(&p.ID, &p.Name, &p.Address, &p.Category, &p.Latitude, &p.Longitude); err != nil {
				return nil, fmt.Errorf("places named %q: %w", name, err)
			}
			out = append(out, p)
		}
		return out, rows.Err()
	}
}
