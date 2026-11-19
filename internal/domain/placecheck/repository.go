package placecheck

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the Postgres Store.
type Repository struct {
	db         *pgxpool.Pool
	movePlaces bool
}

// NewRepository builds the repository. It records what the map said and
// leaves the places themselves alone; see MovingPlaces.
func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// MovingPlaces makes Save also move each matched place to where the map says
// it is.
//
// Off unless asked for, because it cannot be taken back: the position the
// place had is overwritten, not kept, so a wrong match made under one
// version of the match rule stays wrong after the rule is fixed. The map's
// position is in the verification row either way. Switch this on once the
// matches have been read on real plans and found right.
func (r *Repository) MovingPlaces() *Repository {
	r.movePlaces = true
	return r
}

const getSQL = `
SELECT poi_id, status = 'matched', source, external_id, wikidata_id, matched_name, kind,
       COALESCE(lat, 0), COALESCE(lon, 0), hours_raw, confidence, checked_at, expires_at
FROM place_verifications
WHERE poi_id = ANY($1)`

// Get returns the verifications held for the given places.
func (r *Repository) Get(ctx context.Context, poiIDs []string) (map[string]Verification, error) {
	out := make(map[string]Verification, len(poiIDs))
	if len(poiIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, getSQL, poiIDs)
	if err != nil {
		return nil, fmt.Errorf("query place_verifications: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v Verification
		if err := rows.Scan(&v.POIID, &v.Matched, &v.Source, &v.ExternalID, &v.WikidataID, &v.MatchedName, &v.Kind,
			&v.Lat, &v.Lon, &v.HoursRaw, &v.Confidence, &v.CheckedAt, &v.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan place_verifications: %w", err)
		}
		v.CheckedAt, v.ExpiresAt = v.CheckedAt.UTC(), v.ExpiresAt.UTC()
		out[v.POIID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read place_verifications: %w", err)
	}
	return out, nil
}

const saveSQL = `
INSERT INTO place_verifications
	(poi_id, status, source, external_id, wikidata_id, matched_name, kind, lat, lon, hours_raw, confidence, checked_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (poi_id) DO UPDATE SET
	status = EXCLUDED.status, source = EXCLUDED.source, external_id = EXCLUDED.external_id,
	wikidata_id = EXCLUDED.wikidata_id, matched_name = EXCLUDED.matched_name, kind = EXCLUDED.kind,
	lat = EXCLUDED.lat, lon = EXCLUDED.lon, hours_raw = EXCLUDED.hours_raw,
	confidence = EXCLUDED.confidence, checked_at = EXCLUDED.checked_at, expires_at = EXCLUDED.expires_at`

// movePOISQL puts a place where the map says it is. A place's identity is
// its city and name, never its position, so moving it is safe; the position
// the model wrote for it can be kilometres off.
const movePOISQL = `
UPDATE points_of_interest
SET location = ST_SetSRID(ST_MakePoint($2, $3), 4326)
WHERE id = $1`

// Save writes the verifications in one transaction and, when MovingPlaces was
// asked for, moves each matched place to its position on the map.
func (r *Repository) Save(ctx context.Context, vs []Verification) error {
	if len(vs) == 0 {
		return nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batch := &pgx.Batch{}
	for _, v := range vs {
		status := "unmatched"
		var lat, lon *float64
		if v.Matched {
			status = "matched"
			lat, lon = &v.Lat, &v.Lon
		}
		batch.Queue(saveSQL, v.POIID, status, v.Source, v.ExternalID, v.WikidataID, v.MatchedName, v.Kind,
			lat, lon, v.HoursRaw, v.Confidence, v.CheckedAt, v.ExpiresAt)
		if !v.Matched || !r.movePlaces {
			continue
		}
		// poi_id is TEXT here and a UUID on the place itself; an id that is
		// not one names no place to move.
		if id, err := uuid.Parse(v.POIID); err == nil {
			batch.Queue(movePOISQL, id, v.Lon, v.Lat)
		}
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("write place_verifications: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
