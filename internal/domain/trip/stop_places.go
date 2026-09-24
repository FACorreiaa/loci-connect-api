package trip

import (
	"context"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi/presenter"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// PlaceLookup is the one read the trip responses need from the POI store:
// the place behind a stop's poi_id, so the phone has coordinates (geofences,
// "Next · 1.2 km"), hours and photos without another round trip.
type PlaceLookup interface {
	GetPOIByID(ctx context.Context, poiID uuid.UUID) (*locitypes.POIDetailedInfo, error)
}

// maxHydratedStops bounds the lookups one response may cost. A trip with
// more stops than this gets its first stops hydrated and the rest bare.
const maxHydratedStops = 60

// WithPlaces attaches the store the stops are hydrated from. Nil is
// supported: stops then carry only their id and name, as before.
func (h *Handler) WithPlaces(places PlaceLookup) *Handler {
	h.places = places
	return h
}

// respond maps a trip and fills in each stop's place.
func (h *Handler) respond(ctx context.Context, t *Trip) *tripv1.TripDraft {
	p := tripToProto(t)
	h.hydrateStops(ctx, p)
	return p
}

// hydrateStops sets `poi` on every stop whose poi_id names a place the store
// still has. A missing place, a bad id or a store error leaves that stop as
// it was; the response never fails for it.
func (h *Handler) hydrateStops(ctx context.Context, draft *tripv1.TripDraft) {
	if h.places == nil || draft == nil {
		return
	}
	found := map[uuid.UUID]*locitypes.POIDetailedInfo{}
	looked := 0
	for _, day := range draft.GetDays() {
		for _, stop := range day.GetStops() {
			id, err := uuid.Parse(stop.GetPoiId())
			if err != nil || id == uuid.Nil {
				continue
			}
			poi, seen := found[id]
			if !seen {
				if looked >= maxHydratedStops {
					continue
				}
				looked++
				poi, err = h.places.GetPOIByID(ctx, id)
				if err != nil || poi == nil {
					found[id] = nil
					continue
				}
				found[id] = poi
			}
			if poi != nil {
				stop.Poi = presenter.ToPOIProto(poi)
			}
		}
	}
}
