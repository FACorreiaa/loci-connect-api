package trip

import (
	"context"
	"fmt"

	poipb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StopImageLookup returns the first stored picture of each POI it is asked
// about: the lowest position in poi_images, with the licence and attribution
// that picture must be shown with. A POI with no picture is absent from the
// map. It is one query however many POIs are asked for.
type StopImageLookup interface {
	FirstImages(ctx context.Context, poiIDs []uuid.UUID) (map[uuid.UUID]*poipb.POIImage, error)
}

// WithStopImages attaches the picture store. Nil is supported: stops then
// carry no image, as before, except the ones a City Pack response fills itself.
func (h *Handler) WithStopImages(images StopImageLookup) *Handler {
	h.images = images
	return h
}

// fillStopImages sets TripStop.image on every stop, across all the given
// drafts, whose poi_id names a POI with a stored picture. It is enrichment:
// a lookup error is logged and the drafts go out without pictures.
//
// The lookup runs once for every draft in the response, so a ListTrips page
// costs one query for its pictures, not one per stop or per trip.
func (h *Handler) fillStopImages(ctx context.Context, drafts ...*tripv1.TripDraft) {
	if h.images == nil {
		return
	}
	seen := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for _, d := range drafts {
		for _, day := range d.GetDays() {
			for _, stop := range day.GetStops() {
				if stop.GetImage() != nil {
					continue
				}
				id, err := uuid.Parse(stop.GetPoiId())
				if err != nil || id == uuid.Nil || seen[id] {
					continue
				}
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	found, err := h.images.FirstImages(ctx, ids)
	if err != nil {
		h.logWarn("load stop images", err)
		return
	}
	for _, d := range drafts {
		for _, day := range d.GetDays() {
			for _, stop := range day.GetStops() {
				if stop.GetImage() != nil {
					continue
				}
				id, err := uuid.Parse(stop.GetPoiId())
				if err != nil {
					continue
				}
				img := found[id]
				if img == nil {
					continue
				}
				// Each stop gets its own copy: two stops at one place must not
				// share a message a later redaction could edit for both.
				stop.Image = clonePOIImage(img)
				// Components that already render a POI's image credits pick
				// the picture up from the hydrated place too, as City Packs do.
				if p := stop.GetPoi(); p != nil && len(p.GetImageCredits()) == 0 {
					p.Images = []string{img.GetUrl()}
					p.ImageCredits = []*poipb.POIImage{clonePOIImage(img)}
				}
			}
		}
	}
}

func clonePOIImage(img *poipb.POIImage) *poipb.POIImage {
	return &poipb.POIImage{
		Url:           img.GetUrl(),
		Source:        img.GetSource(),
		Licence:       img.GetLicence(),
		Attribution:   img.GetAttribution(),
		SourcePageUrl: img.GetSourcePageUrl(),
	}
}

// PGStopImages reads poi_images.
type PGStopImages struct {
	db *pgxpool.Pool
}

// NewStopImageStore builds the Postgres picture lookup.
func NewStopImageStore(db *pgxpool.Pool) *PGStopImages {
	return &PGStopImages{db: db}
}

var _ StopImageLookup = (*PGStopImages)(nil)

// FirstImages picks each POI's first picture with DISTINCT ON, in the same
// order City Pack stops use (position, then fetch time), so a stop shows the
// same photo in a pack and in the trip claimed from it.
func (s *PGStopImages) FirstImages(ctx context.Context, poiIDs []uuid.UUID) (map[uuid.UUID]*poipb.POIImage, error) {
	out := make(map[uuid.UUID]*poipb.POIImage, len(poiIDs))
	if len(poiIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (poi_id)
		       poi_id, url, source, licence, attribution, source_page_url
		FROM poi_images
		WHERE poi_id = ANY($1)
		ORDER BY poi_id, position, fetched_at`, poiIDs)
	if err != nil {
		return nil, fmt.Errorf("load stop images: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id  uuid.UUID
			img poipb.POIImage
		)
		if err := rows.Scan(&id, &img.Url, &img.Source, &img.Licence, &img.Attribution, &img.SourcePageUrl); err != nil {
			return nil, fmt.Errorf("scan stop image: %w", err)
		}
		// poi_images refuses blank credits; this guards the proto's
		// requirement anyway, since an uncredited picture must not be shown.
		if img.Url == "" || img.Licence == "" || img.Attribution == "" {
			continue
		}
		out[id] = &img
	}
	return out, rows.Err()
}
