package compare

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	poirepo "github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi/presenter"
	comparev1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/compare/v1"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/localcontext"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxTopPOIs = 8

// PlanChecker resolves subscription plan for freemium gates.
type PlanChecker interface {
	EffectivePlan(ctx context.Context, userID uuid.UUID) (string, error)
}

// Service compares weekend city candidates.
type Service struct {
	cities    cityrepo.Repository
	pois      poirepo.Service
	weather   localcontext.WeatherAdapter
	transport localcontext.StubTransportWithDrive
	booking   localcontext.BookingComDeepLink
	dining    localcontext.OpenTableDeepLink
	weatherEst bool
	logger    *slog.Logger
	plans     PlanChecker
}

func NewService(
	cities cityrepo.Repository,
	pois poirepo.Service,
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
	OriginCity     string
	OriginLat      float64
	OriginLon      float64
	Candidates     []string
	Start          time.Time
	End            time.Time
	UserID         uuid.UUID
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

	for _, name := range in.Candidates {
		col, score, resolvedCity, err := s.buildColumn(ctx, originLat, originLon, name, windowHours)
		if err != nil {
			s.logger.WarnContext(ctx, "compare column skipped", slog.String("city", name), slog.Any("error", err))
			continue
		}
		resp.Columns = append(resp.Columns, col)
		scores = append(scores, columnScore{name: name, score: score})
		resolved = append(resolved, resolvedCity)
	}

	if len(resp.Columns) < 2 {
		return nil, fmt.Errorf("need at least 2 resolvable candidate cities")
	}

	feasible, outline, totalMins := dualCityFeasible(originLat, originLon, resolved, windowHours)
	resp.DualCityOption = &comparev1.DualCityOption{
		Feasible:        feasible && in.AllowDualCity,
		Outline:         outline,
		TotalTravelMins: int32(totalMins),
		ProOnly:         !in.AllowDualCity,
	}
	if !in.AllowDualCity && feasible {
		resp.DualCityOption.Outline = outline + " (Pro: unlock dual-city outline export)"
	}

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
		resp.RecommendationReason = "Both cities are close enough for a split weekend"
	}

	return resp, nil
}

func (s *Service) resolveOrigin(ctx context.Context, in CompareInput) (lat, lon float64, name string, err error) {
	if in.OriginLat != 0 || in.OriginLon != 0 {
		return in.OriginLat, in.OriginLon, in.OriginCity, nil
	}
	if in.OriginCity == "" {
		return 0, 0, "", fmt.Errorf("origin city or coordinates required")
	}
	city, err := s.cities.FindCityByFuzzyName(ctx, in.OriginCity)
	if err != nil || city == nil {
		return 0, 0, "", fmt.Errorf("origin city not found: %s", in.OriginCity)
	}
	if city.CenterLatitude == nil || city.CenterLongitude == nil {
		return 0, 0, "", fmt.Errorf("origin city missing coordinates")
	}
	return *city.CenterLatitude, *city.CenterLongitude, city.Name, nil
}

func (s *Service) buildColumn(
	ctx context.Context,
	originLat, originLon float64,
	cityName string,
	windowHours float64,
) (*comparev1.CityCompareColumn, float64, resolvedCity, error) {
	city, err := s.cities.FindCityByFuzzyName(ctx, cityName)
	if err != nil || city == nil {
		return nil, 0, resolvedCity{}, fmt.Errorf("city not found: %w", err)
	}
	if city.CenterLatitude == nil || city.CenterLongitude == nil {
		return nil, 0, resolvedCity{}, fmt.Errorf("city missing coordinates: %s", cityName)
	}
	lat, lon := *city.CenterLatitude, *city.CenterLongitude

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

	return col, score, resolvedCity{name: city.Name, lat: lat, lon: lon}, nil
}

func ptr(s string) *string { return &s }
