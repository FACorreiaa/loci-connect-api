package city

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Service interface {
	GetAllCities(ctx context.Context) ([]locitypes.CityDetail, error)
	GetCityByID(ctx context.Context, cityID uuid.UUID) (*locitypes.CityDetail, error)
	GetCityByName(ctx context.Context, name string) (*locitypes.CityDetail, error)
	SearchCities(ctx context.Context, query string, limit int) ([]locitypes.CityDetail, error)
}

// ErrCityNotFound is returned when a lookup resolves to no city, so the handler
// can map it to a NotFound status instead of a generic internal error.
var ErrCityNotFound = errors.New("city not found")

type ServiceImpl struct {
	logger *slog.Logger
	repo   Repository
	// Optional, attached via WithGeocoder. Nil means database-only search,
	// which is what this service did before there was a geocoder.
	fwd geocode.Forward
}

// WithGeocoder lets city search fall back to the geocoder.
//
// It exists because SearchCities reads the cities table, and that table only
// ever held cities some earlier conversation had generated content for. A picker
// backed by it alone offers nothing for most of the world — which is why the
// client's city search module sat unused: there was nothing useful to show.
//
// A builder rather than a constructor argument because it is genuinely
// optional: search worked without it and must keep working when geocoding is
// switched off.
func (s *ServiceImpl) WithGeocoder(fwd geocode.Forward) *ServiceImpl {
	s.fwd = fwd
	return s
}

func NewCityService(repo Repository, logger *slog.Logger) *ServiceImpl {
	return &ServiceImpl{
		logger: logger,
		repo:   repo,
	}
}

// GetAllCities retrieves all cities from the database
func (s *ServiceImpl) GetAllCities(ctx context.Context) ([]locitypes.CityDetail, error) {
	ctx, span := otel.Tracer("CityService").Start(ctx, "GetAllCities")
	defer span.End()

	l := s.logger.With(slog.String("method", "GetAllCities"))

	l.InfoContext(ctx, "Retrieving all cities from database")

	cities, err := s.repo.GetAllCities(ctx)
	if err != nil {
		l.ErrorContext(ctx, "Failed to retrieve cities from repository", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Repository operation failed")
		return nil, fmt.Errorf("failed to retrieve cities: %w", err)
	}

	l.InfoContext(ctx, "Successfully retrieved cities", slog.Int("count", len(cities)))
	span.SetAttributes(attribute.Int("cities.count", len(cities)))
	span.SetStatus(codes.Ok, "Cities retrieved successfully")

	return cities, nil
}

// GetCityByID retrieves one city by primary key.
func (s *ServiceImpl) GetCityByID(ctx context.Context, cityID uuid.UUID) (*locitypes.CityDetail, error) {
	ctx, span := otel.Tracer("CityService").Start(ctx, "GetCityByID", trace.WithAttributes(
		attribute.String("city.id", cityID.String()),
	))
	defer span.End()

	city, err := s.repo.GetCityByID(ctx, cityID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Repository operation failed")
		return nil, fmt.Errorf("failed to retrieve city: %w", err)
	}
	if city == nil {
		span.SetStatus(codes.Ok, "City not found")
		return nil, ErrCityNotFound
	}

	span.SetStatus(codes.Ok, "City retrieved")
	return city, nil
}

// GetCityByName resolves a city from a human-typed name. Fuzzy matching is
// deliberate: callers pass whatever the user or the LLM wrote ("Evora",
// "Évora"), and an exact match would fail on the accent alone.
func (s *ServiceImpl) GetCityByName(ctx context.Context, name string) (*locitypes.CityDetail, error) {
	ctx, span := otel.Tracer("CityService").Start(ctx, "GetCityByName", trace.WithAttributes(
		attribute.String("city.name", name),
	))
	defer span.End()

	if name == "" {
		return nil, ErrCityNotFound
	}

	city, err := s.repo.FindCityByFuzzyName(ctx, name)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Repository operation failed")
		return nil, fmt.Errorf("failed to retrieve city by name: %w", err)
	}
	if city == nil {
		span.SetStatus(codes.Ok, "City not found")
		return nil, ErrCityNotFound
	}

	span.SetStatus(codes.Ok, "City retrieved")
	return city, nil
}

// SearchCities backs the client's city picker.
func (s *ServiceImpl) SearchCities(ctx context.Context, query string, limit int) ([]locitypes.CityDetail, error) {
	ctx, span := otel.Tracer("CityService").Start(ctx, "SearchCities", trace.WithAttributes(
		attribute.String("search.query", query),
		attribute.Int("search.limit", limit),
	))
	defer span.End()

	cities, err := s.repo.SearchCitiesByName(ctx, query, limit)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Repository operation failed")
		return nil, fmt.Errorf("failed to search cities: %w", err)
	}

	cities = s.mergeGeocoded(ctx, query, limit, cities)

	span.SetAttributes(attribute.Int("cities.count", len(cities)))
	span.SetStatus(codes.Ok, "Cities searched")
	return cities, nil
}

// thinResultThreshold is how few stored matches count as "we have nothing useful
// to offer", and so when it is worth asking the geocoder.
const thinResultThreshold = 5

// mergeGeocoded tops up a thin result with places from the geocoder.
//
// Three deliberate limits. An empty query is the picker's mount-time browse and
// never reaches the provider. A healthy set of stored matches is left alone, so
// the common case stays a single database query. And nothing here is persisted:
// this runs on every keystroke, and a search field must not write rows. Cities
// are created when someone actually compares them, through the resolver.
//
// Geocoder-only entries carry an empty id, which is both legal on the wire and
// meaningful to the client: there is no row yet, so send the name and
// coordinates and let the server create it.
func (s *ServiceImpl) mergeGeocoded(
	ctx context.Context,
	query string,
	limit int,
	stored []locitypes.CityDetail,
) []locitypes.CityDetail {
	if s.fwd == nil || strings.TrimSpace(query) == "" || len(stored) >= thinResultThreshold {
		return stored
	}

	places, err := s.fwd.Search(ctx, query, limit)
	if err != nil {
		// Search degrades to whatever is stored. A provider hiccup should not
		// empty a picker that already had something to show.
		s.logger.WarnContext(ctx, "city search could not reach the geocoder",
			slog.String("query", query), slog.Any("error", err))
		return stored
	}

	seen := make(map[string]struct{}, len(stored)+len(places))
	for _, c := range stored {
		seen[dedupeKey(c.Name, c.Country)] = struct{}{}
	}

	out := stored
	for _, p := range places {
		key := dedupeKey(p.Name, p.CountryCode)
		if _, dup := seen[key]; dup {
			continue
		}
		// The stored rows carry a country name and the geocoder a code, so
		// check both spellings before deciding this is a new place.
		if _, dup := seen[dedupeKey(p.Name, p.Country)]; dup {
			continue
		}
		seen[key] = struct{}{}

		lat, lon := p.Lat, p.Lon
		out = append(out, locitypes.CityDetail{
			Name:            p.Name,
			Country:         p.Country,
			StateProvince:   p.Admin1,
			CenterLatitude:  &lat,
			CenterLongitude: &lon,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func dedupeKey(name, country string) string {
	return foldName(name) + "|" + foldName(country)
}
