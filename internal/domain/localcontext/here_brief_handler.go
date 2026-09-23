package localcontext

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
)

// hereWeatherDays is today plus the next two.
const hereWeatherDays = 3

// herePlaceTimeout bounds the geocoder: only the headlines need the place.
var herePlaceTimeout = 2 * time.Second

// WithPlaces attaches the town-level geocoder for GetHereBrief. Optional:
// without it the brief has no place name and no news (the news queries need
// a town or region).
func (h *Handler) WithPlaces(p PlaceResolver) *Handler {
	h.places = p
	return h
}

// roundCoord keeps ~1 km of precision. Everything downstream — geocoder,
// weather, alerts, cache keys — sees only the rounded value, and none of it
// is logged.
func roundCoord(v float64) float64 {
	return math.Round(v*100) / 100
}

func (h *Handler) GetHereBrief(
	ctx context.Context,
	req *connect.Request[lcv1.GetHereBriefRequest],
) (*connect.Response[lcv1.HereBrief], error) {
	userID, err := newsUserID(ctx)
	if err != nil {
		return nil, err
	}
	lat, lon := roundCoord(req.Msg.GetLatitude()), roundCoord(req.Msg.GetLongitude())

	var (
		wg     sync.WaitGroup
		place  Place
		fc     []WeatherDay
		alerts []Alert
		news   HereNews
	)
	wg.Go(func() {
		days, err := h.weather.Forecast(ctx, lat, lon, hereWeatherDays)
		if err != nil {
			h.logger.WarnContext(ctx, "here brief: weather failed", slog.String("error", redactErr(err)))
			return
		}
		fc = days
	})
	if h.signals.Enabled() {
		wg.Go(func() {
			start := time.Now().UTC()
			alerts = h.signals.Gather(ctx, lat, lon, start, start.AddDate(0, 0, hereWeatherDays))
		})
	}
	// Only the headlines need the place, so the lookup runs beside the weather
	// rather than in front of it, under its own short deadline.
	wg.Go(func() {
		place = h.lookupPlace(ctx, lat, lon)
		if h.news == nil {
			return
		}
		n, err := h.news.Here(ctx, userID, place)
		if err != nil {
			h.logger.WarnContext(ctx, "here brief: news failed", slog.String("error", redactErr(err)))
			return
		}
		news = n
	})
	wg.Wait()

	return connect.NewResponse(&lcv1.HereBrief{
		Place: &lcv1.HerePlace{
			Locality: place.Locality, Region: place.Region,
			CountryCode: place.CountryCode, CountryName: place.CountryName,
		},
		Weather:            toWeatherDaysProto(fc),
		WeatherIsEstimated: h.estimated,
		Alerts:             ToLocalAlertsProto(alerts),
		Local:              toNewsItemsProto(news.Local),
		Disruption:         toNewsItemsProto(news.Disruption),
		WhatsOn:            toNewsItemsProto(news.WhatsOn),
		Stale:              news.Stale,
	}), nil
}

func (h *Handler) lookupPlace(ctx context.Context, lat, lon float64) Place {
	if h.places == nil {
		return Place{}
	}
	ctx, cancel := context.WithTimeout(ctx, herePlaceTimeout)
	defer cancel()
	p, err := h.places.Place(ctx, lat, lon)
	if err != nil {
		h.logger.WarnContext(ctx, "here brief: place lookup failed", slog.String("error", redactErr(err)))
		return Place{}
	}
	return p
}
