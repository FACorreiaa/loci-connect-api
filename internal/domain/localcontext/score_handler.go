package localcontext

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/apierr"
	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/google/uuid"
)

// defaultWindowHours is the "a weekend" assumption when no window is given.
const defaultWindowHours = 48

// CityResolver turns a typed city name into coordinates.
//
// One method wide on purpose. It used to be a bare fuzzy-name lookup against
// stored rows, which meant GetGoScore could only answer for cities some earlier
// conversation had already created — the same defect that made /compare reject
// Porto. The city domain now owns that cascade, including geocoding a city we
// hold no row for, so this asks for the whole answer rather than a table lookup.
//
// The city package does not import this one, so naming its types here does not
// create a cycle.
type CityResolver interface {
	Resolve(ctx context.Context, q cityrepo.ResolveQuery) (*cityrepo.Resolved, error)
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
		return nil, goScoreError(err)
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

	// Live disruptions for the same window. Gather degrades internally — a
	// failing source is logged and skipped — so an outage costs the score its
	// alert dimension rather than costing the user their answer.
	var alerts []Alert
	if h.signals.Enabled() {
		start, end := scoreWindow(req.Msg, windowHours)
		alerts = h.signals.Gather(ctx, lat, lon, start, end)
	}

	score := Score(ScoreInput{
		CityName:         cityName,
		Forecast:         forecast,
		WeatherEstimated: h.estimated,
		TravelMins:       travelMins,
		WindowHours:      windowHours,
		POICount:         poiCount,
		Alerts:           alerts,
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

	// Both of these tell the caller to change what it sent, so they carry
	// ErrBadRequest rather than arriving at the mapper untyped and defaulting
	// to Internal.
	if msg.GetCityName() == "" {
		return 0, 0, "", uuid.Nil, fmt.Errorf("%w: city_name or latitude/longitude is required", locitypes.ErrBadRequest)
	}
	if h.cities == nil {
		return 0, 0, "", uuid.Nil, fmt.Errorf("%w: city lookup is unavailable; pass latitude and longitude", locitypes.ErrBadRequest)
	}

	resolved, err := h.cities.Resolve(ctx, cityrepo.ResolveQuery{Name: msg.GetCityName()})
	if err != nil {
		// %w so the sentinel survives: whether this was an unknown place or our
		// geocoder failing decides the status code the caller gets.
		return 0, 0, "", uuid.Nil, fmt.Errorf("we could not place %q on the map: %w", msg.GetCityName(), err)
	}
	return resolved.Lat, resolved.Lon, resolved.City.Name, resolved.City.ID, nil
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

// scoreWindow resolves the concrete dates the alerts should cover.
//
// The request's window is optional, and windowFrom already defaults a missing
// one to 48 hours — but a duration is not enough to ask "which holidays fall in
// it". When no start is given we assume the window begins now, which is what
// "should I go this weekend?" means when asked without dates.
func scoreWindow(msg *lcv1.GetGoScoreRequest, windowHours float64) (start, end time.Time) {
	if msg.Start != nil {
		start = msg.Start.AsTime()
	} else {
		start = time.Now().UTC()
	}
	if msg.End != nil {
		end = msg.End.AsTime()
	}
	if end.IsZero() || !end.After(start) {
		end = start.Add(time.Duration(windowHours) * time.Hour)
	}
	return start, end
}

// goScoreError distinguishes a place we cannot find from a provider we cannot
// reach. Both used to return InvalidArgument, so an outage was reported as the
// caller's mistake and never showed up as a failure of ours.
func goScoreError(err error) error {
	switch {
	case errors.Is(err, cityrepo.ErrGeocoderUnavailable):
		return connect.NewError(connect.CodeUnavailable, err)
	case errors.Is(err, cityrepo.ErrCityUnresolvable):
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return apierr.ToConnect(err)
}
