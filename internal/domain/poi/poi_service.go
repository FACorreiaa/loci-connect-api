package poi

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/ai"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ Service = (*ServiceImpl)(nil)

// Service defines the business logic contract for POI operations.
type Service interface {
	AddPoiToFavourites(ctx context.Context, userID, poiID uuid.UUID, isLLMGenerated bool) (uuid.UUID, error)
	RemovePoiFromFavourites(ctx context.Context, userID, poiID uuid.UUID, isLLMGenerated bool) error
	GetFavouritePOIsByUserID(ctx context.Context, userID uuid.UUID) ([]locitypes.POIDetailedInfo, error)
	GetFavouritePOIsByUserIDPaginated(ctx context.Context, userID uuid.UUID, limit, offset int) ([]locitypes.POIDetailedInfo, int, error)
	GetPOI(ctx context.Context, poiID uuid.UUID) (*locitypes.POIDetailedInfo, error)
	GetPOIsByCityID(ctx context.Context, cityID uuid.UUID) ([]locitypes.POIDetailedInfo, error)

	// SearchPOIs Traditional search
	SearchPOIs(ctx context.Context, filter locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error)

	// SearchPOIsSemantic Semantic search methods
	SearchPOIsSemantic(ctx context.Context, query string, limit int) ([]locitypes.POIDetailedInfo, error)
	SearchPOIsSemanticByCity(ctx context.Context, query string, cityID uuid.UUID, limit int) ([]locitypes.POIDetailedInfo, error)
	SearchPOIsByQueryAndCity(ctx context.Context, query, cityName string) ([]locitypes.POIDetailedInfo, error)
	SearchPOIsHybrid(ctx context.Context, filter locitypes.POIFilter, query string, semanticWeight float64) ([]locitypes.POIDetailedInfo, error)
	GenerateEmbeddingForPOI(ctx context.Context, poiID uuid.UUID) error
	GenerateEmbeddingsForAllPOIs(ctx context.Context, batchSize int) error

	// GetItinerary Itinerary management
	GetItinerary(ctx context.Context, userID, itineraryID uuid.UUID) (*locitypes.UserSavedItinerary, error)
	GetItineraries(ctx context.Context, userID uuid.UUID, page, pageSize int) (*locitypes.PaginatedUserItinerariesResponse, error)
	UpdateItinerary(ctx context.Context, userID, itineraryID uuid.UUID, updates locitypes.UpdateItineraryRequest) (*locitypes.UserSavedItinerary, error)

	// GetGeneralPOIByDistance Discover Service
	GetGeneralPOIByDistance(ctx context.Context, userID uuid.UUID, lat, lon, distance float64) ([]locitypes.POIDetailedInfo, error) //, categoryFilter string

	// GetNearbyRestaurants Domain-specific discover services
	GetNearbyRestaurants(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, cuisineType, priceRange string) ([]locitypes.POIDetailedInfo, error)
	GetNearbyActivities(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, activityType, duration string) ([]locitypes.POIDetailedInfo, error)
	GetNearbyHotels(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, starRating, amenities string) ([]locitypes.POIDetailedInfo, error)
	GetNearbyAttractions(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, attractionType, isOutdoor string) ([]locitypes.POIDetailedInfo, error)

	// FindOrCreateLLMPOI LLM POI management
	FindOrCreateLLMPOI(ctx context.Context, poiData *locitypes.POIDetailedInfo) (uuid.UUID, error)
}

type ServiceImpl struct {
	logger           *slog.Logger
	poiRepository    Repository
	embeddingService generativeAI.EmbeddingClient
	aiClient         generativeAI.ChatClient
	cityRepo         city.Repository
	discoverRepo     interface {
		TrackSearch(ctx context.Context, userID uuid.UUID, query, cityName, source string, resultCount int) error
	}
	cache       cachestore.Store
	llmSem      *concurrency.LLMSemaphore
	prefVectors preference.VectorReader
}

func NewServiceImpl(
	poiRepository Repository,
	embeddingService generativeAI.EmbeddingClient,
	cityRepo city.Repository,
	discoverRepo interface {
		TrackSearch(ctx context.Context, userID uuid.UUID, query, cityName, source string, resultCount int) error
	},
	aiCfg config.AIConfig,
	llmSem *concurrency.LLMSemaphore,
	appCache cachestore.Store,
	logger *slog.Logger,
) *ServiceImpl {
	ctx := context.Background()
	logger.Debug("initializing POI AI client", slog.String("model", aiCfg.Model))
	aiClient, err := ai.NewChatClient(ctx, aiCfg, logger)
	if err != nil {
		logger.Error("Failed to initialize AI client", slog.Any("error", err))
		// For now, set to nil and handle gracefully in methods
		aiClient = nil
	}

	if embeddingService == nil {
		embeddingService, err = ai.NewEmbeddingClient(ctx, aiCfg, logger)
		if err != nil {
			logger.Error("Failed to initialize embedding client", slog.Any("error", err))
		}
	}

	return &ServiceImpl{
		logger:           logger,
		poiRepository:    poiRepository,
		aiClient:         aiClient,
		cityRepo:         cityRepo,
		discoverRepo:     discoverRepo,
		cache:            appCache,
		embeddingService: embeddingService,
		llmSem:           llmSem,
	}
}

// SetPreferenceVectors enables preference-aware semantic search blending.
func (s *ServiceImpl) SetPreferenceVectors(r preference.VectorReader) {
	if s != nil {
		s.prefVectors = r
	}
}

func (s *ServiceImpl) acquireLLMSlot(ctx context.Context) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	return s.llmSem.Acquire(ctx)
}

func (s *ServiceImpl) generateWithLLMSlot(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	// Quota-armed channels (MCP API keys) pay here — right before the LLM
	// call — so cache and database hits stay free. Web RPCs never arm the
	// context and are unaffected.
	if err := subscription.ConsumeQuotaFromContext(ctx); err != nil {
		return nil, err
	}
	release, err := s.acquireLLMSlot(ctx)
	if err != nil {
		return nil, fmt.Errorf("LLM capacity exceeded: %w", err)
	}
	defer release()
	return s.aiClient.Generate(ctx, prompt, config)
}

// embedQueryMetered generates a query embedding, charging quota-armed
// channels (MCP) for the provider call while leaving web RPCs unmetered.
func (s *ServiceImpl) embedQueryMetered(ctx context.Context, query string) ([]float32, error) {
	if err := subscription.ConsumeQuotaFromContext(ctx); err != nil {
		return nil, err
	}
	return s.embeddingService.GenerateQueryEmbedding(ctx, query)
}

func (s *ServiceImpl) GetPOIsByCityID(ctx context.Context, cityID uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	pois, err := s.poiRepository.GetPOIsByCityID(ctx, cityID)
	if err != nil {
		s.logger.Error("failed to get POIs by city ID", "error", err)
		return nil, err
	}
	return pois, nil
}

func (s *ServiceImpl) GetPOI(ctx context.Context, poiID uuid.UUID) (*locitypes.POIDetailedInfo, error) {
	poi, err := s.poiRepository.GetPOIByID(ctx, poiID)
	if err != nil {
		s.logger.Error("failed to get POI by ID", "error", err)
		return nil, err
	}
	return poi, nil
}

// FindOrCreateLLMPOI finds an existing LLM POI by name or creates a new one
func (s *ServiceImpl) FindOrCreateLLMPOI(ctx context.Context, poiData *locitypes.POIDetailedInfo) (uuid.UUID, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "FindOrCreateLLMPOI")
	defer span.End()

	if poiData == nil {
		return uuid.Nil, fmt.Errorf("POI data cannot be nil")
	}

	// First, try to find existing POI by name and city
	id, err := s.poiRepository.FindLLMPOIByNameAndCity(ctx, poiData.Name, poiData.City)
	if err == nil && id != uuid.Nil {
		s.logger.InfoContext(ctx, "Found existing LLM POI", "name", poiData.Name, "id", id)
		span.SetAttributes(attribute.String("operation", "found_existing"))
		return id, nil
	}

	s.logger.InfoContext(ctx, "Created new LLM POI", "name", poiData.Name, "id", id)
	span.SetAttributes(attribute.String("operation", "created_new"))
	return id, nil
}

// FindLLMPOIByName finds an LLM POI by name, searching across all cities
func (s *ServiceImpl) FindLLMPOIByName(ctx context.Context, poiName string) (uuid.UUID, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "FindLLMPOIByName", trace.WithAttributes(
		attribute.String("poi.name", poiName),
	))
	defer span.End()

	// For removal purposes, we need to find the POI by name
	// Since we don't have city context, we'll search by name only
	// This could be enhanced later to include city context if needed
	return s.poiRepository.FindLLMPOIByName(ctx, poiName)
}
