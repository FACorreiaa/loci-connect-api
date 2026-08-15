package trip

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/packing"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
)

// WeatherSource supplies the forecast that makes packing suggestions specific to
// a trip rather than generic advice.
//
// Declared here as a narrow interface so this package does not depend on the
// localcontext handler, only on the shape it needs.
type WeatherSource interface {
	Forecast(ctx context.Context, lat, lon float64, days int) ([]localcontext.WeatherDay, error)
}

// WithPacking attaches the forecast source that SuggestPacking needs. It is
// optional: without it the RPC still works and simply produces no weather-driven
// items, which is better than guessing them.
func (h *Handler) WithPacking(weather WeatherSource, weatherEstimated bool) *Handler {
	h.weather = weather
	h.weatherEstimated = weatherEstimated
	return h
}

// SuggestPacking derives a packing list from a saved trip.
//
// It reads the trip the user actually saved — how long it is, which cities on
// which days, how much driving, what they said they cared about — then fetches
// each city's forecast for its own days. That is the part a notes app cannot do,
// and the reason every suggestion carries its justification.
func (h *Handler) SuggestPacking(
	ctx context.Context,
	req *connect.Request[tripv1.SuggestPackingRequest],
) (*connect.Response[tripv1.SuggestPackingResponse], error) {
	userID, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	tripID, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	t, err := h.repo.GetTrip(ctx, tripID, userID)
	if err != nil {
		return nil, toConnectErr(err)
	}

	in, usedForecast := h.packingInput(ctx, t)
	suggestions := packing.Suggest(in)

	out := &tripv1.SuggestPackingResponse{
		UsedForecast: usedForecast,
		// Only worth flagging when the forecast actually influenced the result.
		WeatherIsEstimated: usedForecast && h.weatherEstimated,
	}
	for _, s := range suggestions {
		out.Suggestions = append(out.Suggestions, &tripv1.PackingSuggestion{
			Text:      s.Text,
			Category:  categoryToProto(s.Category),
			Reason:    s.Reason,
			Essential: s.Essential,
		})
	}
	return connect.NewResponse(out), nil
}

// packingInput assembles what the generator needs from the stored trip.
func (h *Handler) packingInput(ctx context.Context, t *Trip) (packing.Input, bool) {
	in := packing.Input{
		TotalDays: len(t.Days),
		Interests: t.Constraints.Interests,
	}
	in.Mobility = t.Constraints.Mobility
	if t.Constraints.BudgetLevel != nil {
		in.BudgetLevel = int(*t.Constraints.BudgetLevel)
	}

	for _, leg := range t.Legs {
		in.TotalDriveMins += int(leg.DurationMins)
	}

	// Group days by city so each city's forecast covers its own stretch of the
	// trip rather than the whole thing.
	type cityAgg struct {
		name     string
		days     int
		lat, lon float64
		hasCoord bool
	}
	var order []string
	agg := map[string]*cityAgg{}

	for _, d := range t.Days {
		if d.TravelDay {
			in.TravelDays++
		}
		name := d.CityName
		if name == "" {
			// Pre-multi-city trips have no per-day city; they are all the trip's
			// primary city.
			name = t.CityName
		}
		if name == "" {
			continue
		}
		if _, ok := agg[name]; !ok {
			agg[name] = &cityAgg{name: name}
			order = append(order, name)
		}
		a := agg[name]
		a.days++
		if !a.hasCoord && d.CityLat != nil && d.CityLon != nil {
			a.lat, a.lon, a.hasCoord = *d.CityLat, *d.CityLon, true
		}
	}

	usedForecast := false
	for _, name := range order {
		a := agg[name]
		window := packing.CityWindow{Name: a.name, Days: a.days}

		if h.weather != nil && a.hasCoord {
			// Ask for this city's own days, capped at what a forecast can usefully
			// cover.
			days := a.days
			if days < 1 {
				days = 1
			}
			if days > 5 {
				days = 5
			}
			if fc, err := h.weather.Forecast(ctx, a.lat, a.lon, days); err == nil && len(fc) > 0 {
				window.Forecast = fc
				usedForecast = true
			} else if err != nil {
				// A forecast failure must not fail the packing list; the generator
				// simply omits weather-driven items.
				h.logf().WarnContext(ctx, "packing: forecast unavailable for city",
					slog.String("city", a.name), slog.Any("error", err))
			}
		}

		in.Cities = append(in.Cities, window)
	}

	return in, usedForecast
}

func categoryToProto(c packing.Category) tripv1.PackingCategory {
	switch c {
	case packing.CategoryEssentials:
		return tripv1.PackingCategory_PACKING_CATEGORY_ESSENTIALS
	case packing.CategoryClothing:
		return tripv1.PackingCategory_PACKING_CATEGORY_CLOTHING
	case packing.CategoryWeather:
		return tripv1.PackingCategory_PACKING_CATEGORY_WEATHER
	case packing.CategoryTech:
		return tripv1.PackingCategory_PACKING_CATEGORY_TECH
	case packing.CategoryHealth:
		return tripv1.PackingCategory_PACKING_CATEGORY_HEALTH
	case packing.CategoryTravel:
		return tripv1.PackingCategory_PACKING_CATEGORY_TRAVEL
	case packing.CategoryActivity:
		return tripv1.PackingCategory_PACKING_CATEGORY_ACTIVITY
	default:
		return tripv1.PackingCategory_PACKING_CATEGORY_UNSPECIFIED
	}
}

// logf returns the handler's logger, falling back to the default so a handler
// constructed without one cannot panic on a warning path.
func (h *Handler) logf() *slog.Logger {
	if h.log != nil {
		return h.log
	}
	return slog.Default()
}

// WithLogger attaches a logger. Optional; the handler falls back to the default.
func (h *Handler) WithLogger(l *slog.Logger) *Handler {
	h.log = l
	return h
}
