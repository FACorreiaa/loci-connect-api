package localcontext

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/google/uuid"
)

// defaultWindowHours is the "a weekend" assumption when no window is given.
const defaultWindowHours = 48

// CityResolver turns a typed city name into coordinates.
//
// Declared here as a narrow interface rather than importing the city domain so
// this package keeps no dependency on it — the compare domain already imports
// localcontext, and a two-way dependency between domains is how import cycles
// start.
type CityResolver interface {
	FindCityByFuzzyName(ctx context.Context, cityName string) (*locitypes.CityDetail, error)
}

// POICounter reports how many worthwhile stops we know about for a city.
type POICounter interface {
	GetPOIsByCityID(ctx context.Context, cityID uuid.UUID) ([]locitypes.POIDetailedInfo, error)
}

// WithScoring attaches the dependencies GetGoScore needs. It is optional: a
// handler built without them still serves GetLocalContext, and GetGoScore
// answers from coordinates alone (no POI dimension) rather than failing.
func (h *Handler) WithScoring(cities CityResolver, pois POICounter) *Handler {
	h.cities = cities
	h.pois = pois
	return h
}

// GetGoScore answers "should I go this weekend?" for one destination.
//
// It is the same judgement CompareService puts on each of its columns — same
// scoring function, same weights — so the number a user sees here matches the
// one they saw when comparing. Only the input gathering differs.
func (h *Handler) GetGoScore(
	ctx context.Context,
	req *connect.Request[lcv1.GetGoScoreRequest],
) (*connect.Response[lcv1.GetGoScoreResponse], error) {
	lat, lon, cityName, cityID, err := h.resolveDestination(ctx, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	windowHours, days := windowFrom(req.Msg.Start.AsTime(), req.Msg.End.AsTime(), req.Msg.Start != nil && req.Msg.End != nil)

	// Weather. A provider hiccup must not fail the call: the scorer treats an
	// empty forecast as "unknown" and scores it neutrally.
	forecast, err := h.weather.Forecast(ctx, lat, lon, days)
	if err != nil {
		h.logger.WarnContext(ctx, "go-score: weather unavailable, scoring without it",
			slog.String("city", cityName), slog.Any("error", err))
		forecast = nil
	}

	// Travel time, when we know where the traveller starts.
	travelMins := 0
	if req.Msg.OriginLat != nil && req.Msg.OriginLon != nil {
		travelMins = geo.DriveMins(geo.HaversineKm(*req.Msg.OriginLat, *req.Msg.OriginLon, lat, lon))
	}

	// POI count, when we resolved a city row and were given a counter.
	poiCount := 0
	if h.pois != nil && cityID != uuid.Nil {
		pois, err := h.pois.GetPOIsByCityID(ctx, cityID)
		if err != nil {
			h.logger.WarnContext(ctx, "go-score: poi count unavailable",
				slog.String("city", cityName), slog.Any("error", err))
		} else {
			poiCount = len(pois)
		}
	}

	score := Score(ScoreInput{
		CityName:         cityName,
		Forecast:         forecast,
		WeatherEstimated: h.estimated,
		TravelMins:       travelMins,
		WindowHours:      windowHours,
		POICount:         poiCount,
	})

	return connect.NewResponse(&lcv1.GetGoScoreResponse{
		Score:    ToGoScoreProto(score),
		CityName: cityName,
	}), nil
}

// resolveDestination accepts either a city name or raw coordinates. Coordinates
// win when both are supplied, since they are unambiguous.
func (h *Handler) resolveDestination(
	ctx context.Context,
	msg *lcv1.GetGoScoreRequest,
) (lat, lon float64, name string, cityID uuid.UUID, err error) {
	if msg.Latitude != nil && msg.Longitude != nil {
		return *msg.Latitude, *msg.Longitude, msg.GetCityName(), uuid.Nil, nil
	}

	if msg.GetCityName() == "" {
		return 0, 0, "", uuid.Nil, errors.New("city_name or latitude/longitude is required")
	}
	if h.cities == nil {
		return 0, 0, "", uuid.Nil, errors.New("city lookup is unavailable; pass latitude and longitude")
	}

	city, err := h.cities.FindCityByFuzzyName(ctx, msg.GetCityName())
	if err != nil || city == nil {
		return 0, 0, "", uuid.Nil, errors.New("city not found: " + msg.GetCityName())
	}
	if city.CenterLatitude == nil || city.CenterLongitude == nil {
		return 0, 0, "", uuid.Nil, errors.New("city has no coordinates on file: " + city.Name)
	}
	return *city.CenterLatitude, *city.CenterLongitude, city.Name, city.ID, nil
}

// windowFrom derives the window length and the forecast horizon it needs.
func windowFrom(start, end time.Time, provided bool) (windowHours float64, days int) {
	windowHours = defaultWindowHours
	if provided {
		if h := end.Sub(start).Hours(); h > 0 {
			windowHours = h
		}
	}

	days = int(windowHours/24) + 1
	if days < 2 {
		days = 2
	}
	if days > 5 {
		days = 5
	}
	return windowHours, days
}
