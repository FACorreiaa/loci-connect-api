package poi

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

// embedBatchTimeout bounds the detached embedding pass. Generous, because it is
// off the request path, but finite so a wedged provider cannot leak goroutines.
const embedBatchTimeout = 2 * time.Minute

// UpsertPOIByIdentity resolves a POI to a stable, persisted row and returns its id.
//
// This is what stops search from minting a fresh uuid for the same place on every
// call. Generated POIs used to exist only in the response, so nothing could
// reference them: GetPOI failed, and reviews, saves, favourites and list items all
// have a foreign key to points_of_interest that no search result satisfied.
//
// Identity is (city, normalised name) — the key migration 0068 enforces with a
// unique index, which is what makes the ON CONFLICT below safe when two searches
// race.
//
// Coordinates are deliberately NOT part of identity. 0067 tried that and the live
// LLM defeated it: asked the same question twice, it returned the same museums
// with coordinates kilometres apart, so nearly every place still got a fresh row.
// A model's coordinates are not a stable property of a place; its name in a city
// is. The cost is that same-named venues in one city collapse together, which is
// the lesser problem for AI-sourced data.
//
// Fields are merged rather than overwritten: a later generation that omits the
// website should not erase one we already had.
// The bool reports whether this call CREATED the row. Postgres has no direct
// "did the upsert insert?" signal, so this uses the standard xmax trick: on an
// insert the row has no previous version and xmax is 0, whereas ON CONFLICT DO
// UPDATE leaves the id of the updating transaction there. Callers use it to embed
// each place exactly once instead of on every repeat search.
func (r *RepositoryImpl) UpsertPOIByIdentity(
	ctx context.Context,
	poi locitypes.POIDetailedInfo,
	cityID uuid.UUID,
) (uuid.UUID, bool, error) {
	if poi.Name == "" {
		return uuid.Nil, false, fmt.Errorf("POI name is required")
	}
	if poi.Latitude < -90 || poi.Latitude > 90 || poi.Longitude < -180 || poi.Longitude > 180 {
		return uuid.Nil, false, fmt.Errorf("invalid coordinates for %q: lat=%f, lon=%f", poi.Name, poi.Latitude, poi.Longitude)
	}
	if cityID == uuid.Nil {
		// Without a city we cannot distinguish same-named places, so the unique
		// index deliberately does not cover these. Refuse rather than create a
		// row that can never be deduped.
		return uuid.Nil, false, fmt.Errorf("city is required to give %q a stable identity", poi.Name)
	}

	const q = `
		INSERT INTO points_of_interest (
			name, description, location, city_id, poi_type, category, source, ai_summary,
			website, phone_number, address
		) VALUES (
			$1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326), $5, $6, $6, 'loci_ai', $7,
			NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, '')
		)
		ON CONFLICT (city_id, lower(btrim(name))) DO UPDATE SET
			-- Only fill gaps. COALESCE keeps whatever we already knew.
			description  = COALESCE(NULLIF(points_of_interest.description, ''), EXCLUDED.description),
			ai_summary   = COALESCE(NULLIF(points_of_interest.ai_summary, ''), EXCLUDED.ai_summary),
			website      = COALESCE(points_of_interest.website, EXCLUDED.website),
			phone_number = COALESCE(points_of_interest.phone_number, EXCLUDED.phone_number),
			address      = COALESCE(NULLIF(points_of_interest.address, ''), EXCLUDED.address),
			poi_type     = COALESCE(NULLIF(points_of_interest.poi_type, ''), EXCLUDED.poi_type),
			-- Location is not identity any more, so do not let a later generation
			-- drag the pin around; the first fix we recorded stands.
			location     = points_of_interest.location,
			category     = COALESCE(NULLIF(points_of_interest.category, ''), EXCLUDED.category),
			updated_at   = NOW()
		RETURNING id, (xmax = 0) AS inserted
	`

	var (
		id       uuid.UUID
		inserted bool
	)
	if err := r.pgpool.QueryRow(ctx, q,
		poi.Name,
		poi.DescriptionPOI,
		poi.Longitude,
		poi.Latitude,
		cityID,
		poi.Category,
		poi.DescriptionPOI,
		poi.Website,
		poi.PhoneNumber,
		poi.Address,
	).Scan(&id, &inserted); err != nil {
		return uuid.Nil, false, fmt.Errorf("upsert POI %q: %w", poi.Name, err)
	}
	return id, inserted, nil
}

// PersistResult summarises a batch persist.
type PersistResult struct {
	// Persisted counts POIs that now have a stable id.
	Persisted int
	// NewIDs are rows created by this call. They have no embedding yet, so they
	// are invisible to semantic search until one is generated.
	NewIDs []uuid.UUID
}

// PersistGeneratedPOIs gives a batch of generated POIs stable identities, in place.
//
// A POI that cannot be persisted keeps its generated id and is still returned:
// showing a place we cannot yet save is better than dropping it from the results.
// Its id simply will not resolve, which the caller can detect from Persisted.
func (r *RepositoryImpl) PersistGeneratedPOIs(
	ctx context.Context,
	pois []locitypes.POIDetailedInfo,
	cityID uuid.UUID,
) PersistResult {
	var out PersistResult
	for i := range pois {
		id, inserted, err := r.UpsertPOIByIdentity(ctx, pois[i], cityID)
		if err != nil {
			r.logger.WarnContext(ctx, "could not give generated POI a stable id; it will not be saveable",
				slog.String("name", pois[i].Name), slog.Any("error", err))
			continue
		}
		pois[i].ID = id
		pois[i].CityID = cityID
		out.Persisted++
		if inserted {
			out.NewIDs = append(out.NewIDs, id)
		}
	}
	return out
}

// persistGeneratedPOIs resolves a freshly generated batch to stable rows.
//
// It needs a city row to key identity against, so it resolves (or creates) the
// city first: an LLM result carries only a city *name*. A failure here is logged
// and swallowed — search still returns the places, they just cannot be saved yet,
// which is strictly better than failing the search.
func (s *ServiceImpl) persistGeneratedPOIs(
	ctx context.Context,
	pois []locitypes.POIDetailedInfo,
	cityName string,
) {
	if len(pois) == 0 || s.cityRepo == nil {
		return
	}

	cityID, err := s.resolveOrCreateCity(ctx, cityName, pois)
	if err != nil {
		s.logger.WarnContext(ctx, "no city for generated POIs; they will not be saveable",
			slog.String("city", cityName), slog.Any("error", err))
		return
	}

	res := s.poiRepository.PersistGeneratedPOIs(ctx, pois, cityID)
	s.logger.InfoContext(ctx, "gave generated POIs stable identities",
		slog.String("city", cityName),
		slog.Int("persisted", res.Persisted),
		slog.Int("created", len(res.NewIDs)),
		slog.Int("total", len(pois)))

	s.embedNewPOIsInBackground(res.NewIDs)
}

// embedNewPOIsInBackground gives freshly created POIs their embeddings.
//
// Without an embedding a persisted POI is invisible to search: the semantic
// queries filter on `embedding IS NOT NULL`, so every search fell through to the
// LLM again even after the place was in the database. Embedding them is what turns
// the first search for a city into a cache for the next one.
//
// It runs detached and on its own timeout because the request context dies with
// the RPC, and a slow embedding provider must not delay results the user already
// has. Failures are logged and left to the existing backfill
// (GetPOIsWithoutEmbeddings) to pick up.
func (s *ServiceImpl) embedNewPOIsInBackground(ids []uuid.UUID) {
	if len(ids) == 0 || s.embeddingService == nil {
		return
	}

	go func(ids []uuid.UUID) {
		ctx, cancel := context.WithTimeout(context.Background(), embedBatchTimeout)
		defer cancel()

		embedded := 0
		for _, id := range ids {
			if err := s.GenerateEmbeddingForPOI(ctx, id); err != nil {
				s.logger.WarnContext(ctx, "could not embed new POI; it stays invisible to search until backfilled",
					slog.String("poi_id", id.String()), slog.Any("error", err))
				continue
			}
			embedded++
		}
		s.logger.InfoContext(ctx, "embedded newly discovered POIs",
			slog.Int("embedded", embedded), slog.Int("candidates", len(ids)))
	}(ids)
}

// resolveOrCreateCity finds the city by name, creating it when the LLM has
// surfaced somewhere we have no row for yet. Coordinates come from the generated
// POIs themselves, averaged, which is close enough to a city centre for the
// distance and identity work that uses it.
func (s *ServiceImpl) resolveOrCreateCity(
	ctx context.Context,
	cityName string,
	pois []locitypes.POIDetailedInfo,
) (uuid.UUID, error) {
	if cityName == "" {
		return uuid.Nil, fmt.Errorf("city name is empty")
	}

	if city, err := s.cityRepo.FindCityByFuzzyName(ctx, cityName); err == nil && city != nil {
		return city.ID, nil
	}

	var latSum, lonSum float64
	var n int
	for _, p := range pois {
		if p.Latitude != 0 || p.Longitude != 0 {
			latSum += p.Latitude
			lonSum += p.Longitude
			n++
		}
	}
	if n == 0 {
		return uuid.Nil, fmt.Errorf("no coordinates to place city %q", cityName)
	}
	lat, lon := latSum/float64(n), lonSum/float64(n)

	id, err := s.cityRepo.SaveCity(ctx, locitypes.CityDetail{
		Name:            cityName,
		Country:         "Unknown",
		CenterLatitude:  &lat,
		CenterLongitude: &lon,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create city %q: %w", cityName, err)
	}
	return id, nil
}
