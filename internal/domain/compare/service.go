package compare

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi/presenter"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	comparev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/compare/v1"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxTopPOIs = 8

// ErrTooFewResolvable means the comparison could not be built because fewer than
// two candidate cities produced a column. It is a distinct sentinel so the
// handler can wrap it around whatever actually went wrong underneath.
var ErrTooFewResolvable = errors.New("need at least 2 resolvable candidate cities")

// PlanChecker resolves subscription plan for freemium gates.
type PlanChecker interface {
	EffectivePlan(ctx context.Context, userID uuid.UUID) (string, error)
}

// CityResolver turns a typed city name into coordinates, creating the row when
// the city is one nobody has generated content for yet.
//
// Declared here, consumer-side and one method wide, for the same reason
// PlanChecker is: compare needs "name in, coordinates out" and nothing else, and
// depending on the whole city repository is what made this package untestable.
type CityResolver interface {
	Resolve(ctx context.Context, q cityrepo.ResolveQuery) (*cityrepo.Resolved, error)
}

// POIFinder reports the places we know about in a city.
//
// Narrowed from the full POI service for the same reason as CityResolver above:
// compare reads one method of it, and depending on all twenty-odd meant no test
// could construct a Service without a mock of everything POI search can do.
type POIFinder interface {
	GetPOIsByCityID(ctx context.Context, cityID uuid.UUID) ([]locitypes.POIDetailedInfo, error)
}

// Service compares weekend city candidates.
type Service struct {
	cities     CityResolver
	pois       POIFinder
	weather    localcontext.WeatherAdapter
	transport  localcontext.StubTransportWithDrive
	booking    localcontext.BookingComDeepLink
	dining     localcontext.OpenTableDeepLink
	weatherEst bool
	logger     *slog.Logger
	plans      PlanChecker

	// Optional, attached via WithSignals. Nil is supported and means the
	// columns score without a disruption dimension, exactly as before.
	signals *localcontext.Gatherer
}

// WithSignals attaches live alert sources so a compared city is penalised for
// the same disruptions GetGoScore sees.
//
// It is a builder rather than a constructor argument because NewService already
// takes nine, and because this is genuinely optional: the compare page worked
// without it and must keep working when no sources are configured.
func (s *Service) WithSignals(g *localcontext.Gatherer) *Service {
	s.signals = g
	return s
}

func NewService(
	cities CityResolver,
	pois POIFinder,
	weather localcontext.WeatherAdapter,
	weatherEst bool,
	transport localcontext.StubTransportWithDrive,
	booking localcontext.BookingComDeepLink,
	dining localcontext.OpenTableDeepLink,
	plans PlanChecker,
	logger *slog.Logger,
) *Service {
	return &Service{
		cities:     cities,
		pois:       pois,
		weather:    weather,
		weatherEst: weatherEst,
		transport:  transport,
		booking:    booking,
		dining:     dining,
		plans:      plans,
		logger:     logger,
	}
}

type CompareInput struct {
	OriginCity       string
	OriginLat        float64
	OriginLon        float64
	Candidates       []string
	Start            time.Time
	End              time.Time
	UserID           uuid.UUID
	Allow3Candidates bool
	AllowDualCity    bool
}

func (s *Service) CompareWeekend(ctx context.Context, in CompareInput) (*comparev1.CompareWeekendResponse, error) {
	originLat, originLon, originName, err := s.resolveOrigin(ctx, in)
	if err != nil {
		return nil, err
	}

	windowHours := in.End.Sub(in.Start).Hours()
	if windowHours <= 0 {
		windowHours = 48
	}

	resp := &comparev1.CompareWeekendResponse{
		OriginCity: originName,
		OriginLat:  originLat,
		OriginLon:  originLon,
	}

	var scores []columnScore
	var resolved []resolvedCity
	// Failures are collected rather than only logged. When too few columns
	// survive, the reason the first one failed is what decides whether this was
	// the user's input or our geocoder being down — and reporting the latter as
	// a bad argument is exactly how the original bug stayed invisible.
	var failures []error

	for _, name := range in.Candidates {
		col, score, resolvedCity, err := s.buildColumn(ctx, originLat, originLon, name, windowHours, in.Start, in.End)
		if err != nil {
			s.logger.WarnContext(ctx, "compare column skipped", slog.String("city", name), slog.Any("error", err))
			failures = append(failures, err)
			continue
		}
		resp.Columns = append(resp.Columns, col)
		scores = append(scores, columnScore{name: name, score: score})
		resolved = append(resolved, resolvedCity)
	}

	if len(resp.Columns) < 2 {
		if len(failures) > 0 {
			return nil, fmt.Errorf("%w: %w", ErrTooFewResolvable, failures[0])
		}
		return nil, ErrTooFewResolvable
	}

	// Plan a route through however many cities fit the window. Two-in-a-weekend
	// is just the smallest case of this, so the same planner answers both.
	route := s.planRoute(originName, originLat, originLon, resolved, in)
	resp.MultiCityPlan = toMultiCityPlanProto(route, resolved, in.AllowDualCity)

	// DualCityOption is retained for clients that have not moved to
	// MultiCityPlan yet. It describes the same route, narrowed to a yes/no.
	resp.DualCityOption = &comparev1.DualCityOption{
		Feasible:        route.Feasible && len(route.Cities) >= 2 && in.AllowDualCity,
		Outline:         route.Outline,
		TotalTravelMins: int32(route.TotalTravelMins),
		ProOnly:         !in.AllowDualCity,
	}
	if !in.AllowDualCity && route.Feasible && len(route.Cities) >= 2 {
		resp.DualCityOption.Outline = route.Outline + " (Pro: unlock the multi-city outline export)"
	}
	feasible := route.Feasible && len(route.Cities) >= 2

	_, reason := pickRecommendation(scores)
	resp.RecommendationReason = reason
	switch {
	case len(scores) >= 2 && scores[0].score >= scores[1].score:
		resp.Recommendation = comparev1.CompareRecommendation_COMPARE_RECOMMENDATION_FIRST
	default:
		resp.Recommendation = comparev1.CompareRecommendation_COMPARE_RECOMMENDATION_SECOND
	}
	if feasible && in.AllowDualCity && scores[0].score-scores[1].score < 3 {
		resp.Recommendation = comparev1.CompareRecommendation_COMPARE_RECOMMENDATION_BOTH
		if n := len(route.Cities); n > 2 {
			resp.RecommendationReason = fmt.Sprintf("All %d fit the window: %s", n, route.Outline)
		} else {
			resp.RecommendationReason = "Both cities are close enough to combine in this window"
		}
	}

	return resp, nil
}

func (s *Service) resolveOrigin(ctx context.Context, in CompareInput) (lat, lon float64, name string, err error) {
	if in.OriginCity == "" && in.OriginLat == 0 && in.OriginLon == 0 {
		return 0, 0, "", fmt.Errorf("origin city or coordinates required")
	}

	// The resolver handles the coordinate short-circuit itself, and uses the
	// coordinates to attach a stored city when one is close by — which is what
	// gives a coordinate-supplied origin a city_id, and therefore its POIs.
	resolved, err := s.cities.Resolve(ctx, cityrepo.ResolveQuery{
		Name: in.OriginCity,
		Lat:  in.OriginLat,
		Lon:  in.OriginLon,
	})
	if err != nil {
		// %w, not %v: the handler decides between "that is not a place" and
		// "our geocoder is down" by unwrapping this, and a flattened error
		// would make every failure look like the user's fault.
		return 0, 0, "", fmt.Errorf("origin %q: %w", in.OriginCity, err)
	}

	name = resolved.City.Name
	if name == "" {
		name = in.OriginCity
	}
	return resolved.Lat, resolved.Lon, name, nil
}

func (s *Service) buildColumn(
	ctx context.Context,
	originLat, originLon float64,
	cityName string,
	windowHours float64,
	// The concrete window, carried alongside windowHours because a duration
	// alone cannot answer "which holidays fall inside it".
	windowStart, windowEnd time.Time,
) (*comparev1.CityCompareColumn, float64, resolvedCity, error) {
	resolved, err := s.cities.Resolve(ctx, cityrepo.ResolveQuery{Name: cityName})
	if err != nil {
		return nil, 0, resolvedCity{}, fmt.Errorf("candidate %q: %w", cityName, err)
	}
	city := resolved.City
	lat, lon := resolved.Lat, resolved.Lon

	distKm := HaversineKm(originLat, originLon, lat, lon)
	travelMins := DriveMins(distKm)

	pois, err := s.pois.GetPOIsByCityID(ctx, city.ID)
	if err != nil {
		s.logger.WarnContext(ctx, "poi fetch failed", slog.Any("error", err))
	}
	if len(pois) > maxTopPOIs {
		pois = pois[:maxTopPOIs]
	}

	weatherDays := int(windowHours/24) + 1
	if weatherDays < 2 {
		weatherDays = 2
	}
	if weatherDays > 5 {
		weatherDays = 5
	}
	fc, _ := s.weather.Forecast(ctx, lat, lon, weatherDays)
	weatherClear := true
	for _, d := range fc {
		if strings.Contains(strings.ToLower(d.Condition), "rain") {
			weatherClear = false
			break
		}
	}

	pros, cons := buildProsCons(city.Name, pois, distKm, travelMins, weatherClear)
	score := scoreColumn(len(pois), distKm, weatherClear)

	// The go/no-go judgement, computed from the same inputs this column already
	// shows. Same function as the standalone GetGoScore RPC, so the number a
	// user sees on /compare matches the one they get anywhere else.
	// Same alert sources GetGoScore uses, so a public holiday that drops the
	// standalone score drops this column by the same amount. One algorithm and
	// one set of inputs is the whole point of sharing the scorer.
	var alerts []localcontext.Alert
	if s.signals.Enabled() {
		alerts = s.signals.Gather(ctx, lat, lon, windowStart, windowEnd)
	}

	goScore := localcontext.Score(localcontext.ScoreInput{
		CityName:         city.Name,
		Forecast:         fc,
		WeatherEstimated: s.weatherEst,
		TravelMins:       travelMins,
		WindowHours:      windowHours,
		POICount:         len(pois),
		Alerts:           alerts,
	})

	col := &comparev1.CityCompareColumn{
		CityName:           city.Name,
		CityId:             city.ID.String(),
		Country:            city.Country,
		CenterLat:          lat,
		CenterLon:          lon,
		DistanceKm:         distKm,
		TravelMins:         int32(travelMins),
		WeatherIsEstimated: s.weatherEst,
		Pros:               pros,
		Cons:               cons,
		GoScore:            localcontext.ToGoScoreProto(goScore),
		StaySnippet:        fmt.Sprintf("Search stays in %s center", city.Name),
		EatSnippet:         fmt.Sprintf("Reserve tables in %s", city.Name),
		BookingOptions: []*comparev1.BookingLink{
			{Provider: "booking.com", Label: "Book stay", Url: s.booking.SearchURL(city.Name, city.Country)},
			{Provider: "thefork", Label: "Reserve dinner", Url: s.dining.RestaurantURL(city.Name)},
		},
	}

	for _, d := range fc {
		col.Weather = append(col.Weather, &lcv1.WeatherDay{
			Date:       timestamppb.New(d.Date),
			HighC:      d.HighC,
			LowC:       d.LowC,
			Condition:  d.Condition,
			PrecipProb: d.PrecipProb,
		})
	}
	for _, p := range pois {
		if proto := presenter.ToPOIProto(&p); proto != nil {
			col.TopPois = append(col.TopPois, proto)
		}
	}

	topts, _ := s.transport.Options(ctx, originLat, originLon, lat, lon)
	for _, t := range topts {
		link := &comparev1.TransportLink{
			Mode:         t.Mode,
			Summary:      t.Summary,
			DurationMins: int32(t.DurationMins),
		}
		if t.Mode == "drive" {
			link.Url = ptr(s.transport.RideURL(originLat, originLon, lat, lon))
		}
		col.TransportOptions = append(col.TransportOptions, link)
	}

	return col, score, resolvedCity{
		id:       city.ID.String(),
		name:     city.Name,
		lat:      lat,
		lon:      lon,
		goScore:  goScore.Score,
		poiCount: len(pois),
		scorePB:  col.GoScore,
	}, nil
}

func ptr(s string) *string { return &s }
