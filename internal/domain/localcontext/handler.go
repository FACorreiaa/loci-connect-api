package localcontext

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext/localcontextconnect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements LocalContextService over a WeatherAdapter. Alerts
// (closures/holidays/strikes) are stubbed empty until real providers are wired.
type Handler struct {
	localcontextconnect.UnimplementedLocalContextServiceHandler
	weather   WeatherAdapter
	estimated bool // true when the weather source is a stub/placeholder
	logger    *slog.Logger

	// Optional, attached via WithScoring, and only used by GetGoScore. Nil is a
	// supported state: the score then answers from coordinates alone.
	cities CityResolver
	pois   POICounter
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
	return connect.NewResponse(out), nil
}
