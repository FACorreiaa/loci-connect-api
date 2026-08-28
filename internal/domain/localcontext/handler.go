package localcontext

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext/localcontextconnect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements LocalContextService over a WeatherAdapter, plus an
// optional Gatherer supplying live alerts (public holidays today; hazards and
// air quality next).
type Handler struct {
	localcontextconnect.UnimplementedLocalContextServiceHandler
	weather   WeatherAdapter
	estimated bool // true when the weather source is a stub/placeholder
	logger    *slog.Logger

	// Optional, attached via WithScoring, and only used by GetGoScore. Nil is a
	// supported state: the score then answers from coordinates alone.
	cities CityResolver
	pois   POICounter

	// Optional, attached via WithSignals. Nil means no live alert sources are
	// configured, which is the same observable state as "nothing is happening
	// at this destination" — an empty alert list either way.
	signals *Gatherer

	// Optional, attached via WithFX. A nil adapter means rates are simply not
	// offered; the drive-cost arithmetic needs no provider and still works.
	fx             *FXAdapter
	fxBase         string
	litresPer100Km float64
	pricePerLitre  float64
	// Resolves coordinates to a country for GetFxRates. Shared with the
	// Gatherer so both use one geocoder and one cache.
	fxCountry CountryResolver
}

// WithSignals attaches the live alert sources (holidays, and later hazards and
// air quality). Optional, like WithScoring: without it the handler behaves
// exactly as it did before any source existed.
func (h *Handler) WithSignals(g *Gatherer) *Handler {
	h.signals = g
	return h
}

// NewHandler builds the handler. `estimated` should be true when `weather` is a
// stub so the client can label the forecast as a placeholder.
func NewHandler(weather WeatherAdapter, estimated bool, logger *slog.Logger) *Handler {
	return &Handler{weather: weather, estimated: estimated, logger: logger}
}

func (h *Handler) GetLocalContext(
	ctx context.Context,
	req *connect.Request[lcv1.GetLocalContextRequest],
) (*connect.Response[lcv1.LocalContext], error) {
	days := int(req.Msg.GetDays())
	if days <= 0 {
		days = 5
	}

	out := &lcv1.LocalContext{WeatherIsEstimated: h.estimated}

	fc, err := h.weather.Forecast(ctx, req.Msg.GetLatitude(), req.Msg.GetLongitude(), days)
	if err != nil {
		// Degrade gracefully: local context is a nicety, never fail the call (or
		// the trip view) because a third-party weather provider hiccuped.
		h.logger.WarnContext(ctx, "weather forecast failed; returning empty",
			slog.Any("error", err))
		return connect.NewResponse(out), nil
	}

	for _, d := range fc {
		out.Weather = append(out.Weather, &lcv1.WeatherDay{
			Date:       timestamppb.New(d.Date),
			HighC:      d.HighC,
			LowC:       d.LowC,
			Condition:  d.Condition,
			PrecipProb: d.PrecipProb,
		})
	}

	// Alerts cover the same span the forecast does, so a holiday shows up
	// alongside the day it falls on. Gather never returns an error — a failing
	// source is logged and skipped — so there is nothing to degrade here.
	if h.signals.Enabled() {
		start := time.Now().UTC()
		if len(fc) > 0 {
			start = fc[0].Date
		}
		end := start.AddDate(0, 0, days)
		out.Alerts = ToLocalAlertsProto(
			h.signals.Gather(ctx, req.Msg.GetLatitude(), req.Msg.GetLongitude(), start, end),
		)
	}

	return connect.NewResponse(out), nil
}
