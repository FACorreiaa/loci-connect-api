package city

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/FACorreiaa/loci-connect-api/internal/types"
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

	span.SetAttributes(attribute.Int("cities.count", len(cities)))
	span.SetStatus(codes.Ok, "Cities searched")
	return cities, nil
}
