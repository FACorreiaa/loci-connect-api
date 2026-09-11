package poi

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// POIImage is one picture of one place, with the credit it cannot be shown
// without. Defined in internal/types so it can travel with the POI it belongs
// to, all the way to the presenter; aliased here because this package is where
// it is fetched and stored.
type POIImage = locitypes.POIImage

// POIsWithoutImages returns places that have no picture yet, oldest first.
//
// Oldest first because a place discovered long ago has been shown to more
// people without one. Limit bounds the batch; the caller runs this repeatedly.
func (r *RepositoryImpl) POIsWithoutImages(ctx context.Context, cityID uuid.UUID, limit int) ([]locitypes.POIDetailedInfo, error) {
	if limit <= 0 {
		return nil, nil
	}

	// The city filter is optional: uuid.Nil means every city, which is what the
	// nightly backfill wants and what a targeted re-fetch does not.
	var cityArg any
	if cityID != uuid.Nil {
		cityArg = cityID
	}

	rows, err := r.pgpool.Query(ctx, `
		SELECT p.id, p.name, COALESCE(p.category, ''), p.city_id
		FROM points_of_interest p
		WHERE ($1::uuid IS NULL OR p.city_id = $1)
		  AND NOT EXISTS (SELECT 1 FROM poi_images pi WHERE pi.poi_id = p.id)
		ORDER BY p.created_at
		LIMIT $2`, cityArg, limit)
	if err != nil {
		return nil, fmt.Errorf("list POIs without images: %w", err)
	}
	defer rows.Close()

	var out []locitypes.POIDetailedInfo
	for rows.Next() {
		var poi locitypes.POIDetailedInfo
		if err := rows.Scan(&poi.ID, &poi.Name, &poi.Category, &poi.CityID); err != nil {
			return nil, fmt.Errorf("scan POI without images: %w", err)
		}
		out = append(out, poi)
	}
	return out, rows.Err()
}

// SavePOIImages attaches pictures to a place.
//
// Idempotent by (poi_id, url): re-running the backfill over a place whose
// images have not changed writes nothing rather than duplicating them.
func (r *RepositoryImpl) SavePOIImages(ctx context.Context, images []POIImage) error {
	if len(images) == 0 {
		return nil
	}

	batchQuery := `
		INSERT INTO poi_images (poi_id, url, source, licence, attribution, source_page_url, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (poi_id, url) DO NOTHING`

	for _, img := range images {
		if _, err := r.pgpool.Exec(ctx, batchQuery,
			img.POIID, img.URL, img.Source, img.Licence, img.Attribution,
			img.SourcePageURL, img.Position,
		); err != nil {
			return fmt.Errorf("save image for POI %s: %w", img.POIID, err)
		}
	}
	return nil
}

// ImagesForPOIs returns pictures for many places at once, keyed by place.
//
// One query per turn rather than one per place: an itinerary carries a dozen
// POIs, and a dozen round trips to attach thumbnails would be the slowest part
// of answering. Places with no picture are simply absent from the map.
func (r *RepositoryImpl) ImagesForPOIs(ctx context.Context, poiIDs []uuid.UUID) (map[uuid.UUID][]POIImage, error) {
	if len(poiIDs) == 0 {
		return nil, nil
	}

	rows, err := r.pgpool.Query(ctx, `
		SELECT poi_id, url, source, licence, attribution, source_page_url, position
		FROM poi_images
		WHERE poi_id = ANY($1::uuid[])
		ORDER BY poi_id, position, fetched_at`, poiIDs)
	if err != nil {
		return nil, fmt.Errorf("read images for %d POIs: %w", len(poiIDs), err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID][]POIImage)
	for rows.Next() {
		var img POIImage
		if err := rows.Scan(&img.POIID, &img.URL, &img.Source, &img.Licence,
			&img.Attribution, &img.SourcePageURL, &img.Position); err != nil {
			return nil, fmt.Errorf("scan POI image: %w", err)
		}
		out[img.POIID] = append(out[img.POIID], img)
	}
	return out, rows.Err()
}

// ImagesForPOI returns a place's pictures with their credits, in display order.
//
// The read on points_of_interest returns URLs alone, which is enough to render
// a thumbnail but not enough to render it legally. Anything that shows an image
// to a person needs this.
func (r *RepositoryImpl) ImagesForPOI(ctx context.Context, poiID uuid.UUID) ([]POIImage, error) {
	rows, err := r.pgpool.Query(ctx, `
		SELECT poi_id, url, source, licence, attribution, source_page_url, position
		FROM poi_images
		WHERE poi_id = $1
		ORDER BY position, fetched_at`, poiID)
	if err != nil {
		return nil, fmt.Errorf("read images for POI %s: %w", poiID, err)
	}
	defer rows.Close()

	var out []POIImage
	for rows.Next() {
		var img POIImage
		if err := rows.Scan(&img.POIID, &img.URL, &img.Source, &img.Licence,
			&img.Attribution, &img.SourcePageURL, &img.Position); err != nil {
			return nil, fmt.Errorf("scan POI image: %w", err)
		}
		out = append(out, img)
	}
	return out, rows.Err()
}
