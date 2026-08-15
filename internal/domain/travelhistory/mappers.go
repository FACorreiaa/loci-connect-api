package travelhistory

import (
	"time"

	travelhistoryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func sourceToProto(s Source) travelhistoryv1.VisitSource {
	switch s {
	case SourceTrip:
		return travelhistoryv1.VisitSource_VISIT_SOURCE_TRIP
	case SourceVisitEvent:
		return travelhistoryv1.VisitSource_VISIT_SOURCE_VISIT_EVENT
	case SourceManual:
		return travelhistoryv1.VisitSource_VISIT_SOURCE_MANUAL
	case SourceBackfill:
		return travelhistoryv1.VisitSource_VISIT_SOURCE_BACKFILL
	default:
		return travelhistoryv1.VisitSource_VISIT_SOURCE_UNSPECIFIED
	}
}

func uuidPtrToString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func stringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// timestampOrNil keeps a zero time as a nil Timestamp rather than as the Unix
// epoch, so the client can tell "no date" from "1 January 1970".
func timestampOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func timestampPtrOrNil(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestampOrNil(*t)
}

func visitedCityToProto(c *VisitedCity) *travelhistoryv1.VisitedCity {
	if c == nil {
		return nil
	}
	return &travelhistoryv1.VisitedCity{
		Id:           c.ID.String(),
		CityId:       uuidPtrToString(c.CityID),
		CityName:     c.CityName,
		Country:      c.Country,
		CountryCode:  stringPtr(c.CountryCode),
		Latitude:     c.Latitude,
		Longitude:    c.Longitude,
		Source:       sourceToProto(c.Source),
		TripId:       uuidPtrToString(c.TripID),
		FirstVisitAt: timestampOrNil(c.FirstVisitAt),
		LastVisitAt:  timestampOrNil(c.LastVisitAt),
		VisitCount:   c.VisitCount,
	}
}

func visitedCitiesToProto(in []*VisitedCity) []*travelhistoryv1.VisitedCity {
	out := make([]*travelhistoryv1.VisitedCity, 0, len(in))
	for _, c := range in {
		if p := visitedCityToProto(c); p != nil {
			out = append(out, p)
		}
	}
	return out
}

func visitedPOIToProto(p *VisitedPOI) *travelhistoryv1.VisitedPOI {
	if p == nil {
		return nil
	}
	return &travelhistoryv1.VisitedPOI{
		Id:        p.ID.String(),
		PoiId:     p.POIID,
		PoiName:   p.POIName,
		CityName:  p.CityName,
		Latitude:  p.Latitude,
		Longitude: p.Longitude,
		TripId:    uuidPtrToString(p.TripID),
		Source:    sourceToProto(p.Source),
		VisitedAt: timestampOrNil(p.VisitedAt),
	}
}

func visitedPOIsToProto(in []*VisitedPOI) []*travelhistoryv1.VisitedPOI {
	out := make([]*travelhistoryv1.VisitedPOI, 0, len(in))
	for _, p := range in {
		if q := visitedPOIToProto(p); q != nil {
			out = append(out, q)
		}
	}
	return out
}

func arcToProto(a *GlobeArc) *travelhistoryv1.GlobeArc {
	if a == nil {
		return nil
	}
	return &travelhistoryv1.GlobeArc{
		FromName:   a.FromName,
		ToName:     a.ToName,
		FromLat:    a.FromLat,
		FromLon:    a.FromLon,
		ToLat:      a.ToLat,
		ToLon:      a.ToLon,
		DistanceKm: a.DistanceKm,
		TripId:     uuidPtrToString(a.TripID),
		Mode:       a.Mode,
		OccurredAt: timestampPtrOrNil(a.OccurredAt),
	}
}

func arcsToProto(in []*GlobeArc) []*travelhistoryv1.GlobeArc {
	out := make([]*travelhistoryv1.GlobeArc, 0, len(in))
	for _, a := range in {
		if p := arcToProto(a); p != nil {
			out = append(out, p)
		}
	}
	return out
}

func summaryToProto(s *Summary) *travelhistoryv1.TravelSummary {
	if s == nil {
		return nil
	}
	return &travelhistoryv1.TravelSummary{
		CitiesVisited:              s.CitiesVisited,
		CountriesVisited:           s.CountriesVisited,
		PoisVisited:                s.POIsVisited,
		DistanceKm:                 s.DistanceKm,
		TripsCompleted:             s.TripsCompleted,
		FirstVisitAt:               timestampPtrOrNil(s.FirstVisitAt),
		LastVisitAt:                timestampPtrOrNil(s.LastVisitAt),
		CitiesVisitedPrevPeriod:    s.CitiesVisitedPrev,
		CountriesVisitedPrevPeriod: s.CountriesVisitedPrev,
		PoisVisitedPrevPeriod:      s.POIsVisitedPrev,
		PeriodDays:                 s.PeriodDays,
	}
}

// parseOptionalUUID treats an empty string as "not supplied" rather than as an
// error, because the proto uses "" for absent ids.
func parseOptionalUUID(s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func visitInputFromProto(req *travelhistoryv1.RecordVisitRequest) (VisitInput, error) {
	cityID, err := parseOptionalUUID(req.GetCityId())
	if err != nil {
		return VisitInput{}, ErrInvalidInput
	}
	tripID, err := parseOptionalUUID(req.GetTripId())
	if err != nil {
		return VisitInput{}, ErrInvalidInput
	}

	in := VisitInput{
		CityID:    cityID,
		CityName:  req.GetCityName(),
		Latitude:  req.GetLatitude(),
		Longitude: req.GetLongitude(),
		// A visit recorded through the public RPC is the traveller telling us
		// directly; anything derived uses a different source.
		Source:  SourceManual,
		TripID:  tripID,
		POIID:   req.GetPoiId(),
		POIName: req.GetPoiName(),
	}
	if ts := req.GetVisitedAt(); ts != nil {
		in.VisitedAt = ts.AsTime()
	}
	return in, nil
}
