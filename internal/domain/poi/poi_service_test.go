package poi

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/types" // Ensure this path is correct
)

type MockCityRepository struct {
	mock.Mock
}

func (m *MockCityRepository) GetCity(ctx context.Context, lat, lon float64) (uuid.UUID, string, error) {
	args := m.Called(ctx, lat, lon)
	return args.Get(0).(uuid.UUID), args.Get(1).(string), args.Error(2)
}

func (m *MockCityRepository) SaveCity(ctx context.Context, city locitypes.CityDetail) (uuid.UUID, error) {
	args := m.Called(ctx, city)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockCityRepository) FindCityByNameAndCountry(ctx context.Context, name, country string) (*locitypes.CityDetail, error) {
	args := m.Called(ctx, name, country)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.CityDetail), args.Error(1)
}

type stubDiscoverRepo struct{}

func (stubDiscoverRepo) TrackSearch(_ context.Context, _ uuid.UUID, _, _, _ string, _ int) error {
	return nil
}

type stubEmbeddingClient struct{}

func (stubEmbeddingClient) GenerateQueryEmbedding(_ context.Context, _ string) ([]float32, error) {
	return nil, nil
}

func (stubEmbeddingClient) GeneratePOIEmbedding(_ context.Context, _, _, _ string) ([]float32, error) {
	return nil, nil
}

func (m *MockCityRepository) GetCityByID(ctx context.Context, cityID uuid.UUID) (*locitypes.CityDetail, error) {
	args := m.Called(ctx, cityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.CityDetail), args.Error(1)
}

func (m *MockCityRepository) FindSimilarCities(ctx context.Context, queryEmbedding []float32, limit int) ([]locitypes.CityDetail, error) {
	args := m.Called(ctx, queryEmbedding, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.CityDetail), args.Error(1)
}

func (m *MockCityRepository) UpdateCityEmbedding(ctx context.Context, cityID uuid.UUID, embedding []float32) error {
	args := m.Called(ctx, cityID, embedding)
	return args.Error(0)
}

func (m *MockCityRepository) GetCitiesWithoutEmbeddings(ctx context.Context, limit int) ([]locitypes.CityDetail, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.CityDetail), args.Error(1)
}

func (m *MockCityRepository) GetCityIDByName(ctx context.Context, cityName string) (uuid.UUID, error) {
	args := m.Called(ctx, cityName)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockCityRepository) FindCityByFuzzyName(ctx context.Context, name string) (*locitypes.CityDetail, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.CityDetail), args.Error(1)
}

func (m *MockCityRepository) GetAllCities(ctx context.Context) ([]locitypes.CityDetail, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.CityDetail), args.Error(1)
}

// MockPOIRepository is a mock implementation of POIRepository
type MockPOIRepository struct {
	mock.Mock
}

func (m *MockPOIRepository) GetPOIsByLocationAndDistanceWithCategory(_ context.Context, _, _, _ float64, _ string) ([]locitypes.POIDetailedInfo, error) {
	panic("implement me")
}

func (m *MockPOIRepository) SavePoi(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	args := m.Called(ctx, poi, cityID)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) FindPoiByNameAndCity(ctx context.Context, name string, cityID uuid.UUID) (*locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, name, cityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) GetPOIsByCityAndDistance(ctx context.Context, cityID uuid.UUID, userLocation locitypes.UserLocation) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, cityID, userLocation)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) GetPOIsByLocationAndDistance(ctx context.Context, lat, lon, radiusMeters float64) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, lat, lon, radiusMeters)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) GetPOIsByLocationAndDistanceWithFilters(ctx context.Context, lat, lon, radiusMeters float64, filters map[string]string) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, lat, lon, radiusMeters, filters)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) AddPoiToFavourites(ctx context.Context, userID, poiID uuid.UUID) (uuid.UUID, error) {
	args := m.Called(ctx, userID, poiID)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) AddLLMPoiToFavourite(ctx context.Context, userID, llmPoiID uuid.UUID) (uuid.UUID, error) {
	args := m.Called(ctx, userID, llmPoiID)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) CheckPoiExists(ctx context.Context, poiID uuid.UUID) (bool, error) {
	args := m.Called(ctx, poiID)
	return args.Get(0).(bool), args.Error(1)
}

func (m *MockPOIRepository) RemovePoiFromFavourites(ctx context.Context, userID, poiID uuid.UUID) error {
	args := m.Called(ctx, poiID, userID)
	return args.Error(0)
}

func (m *MockPOIRepository) CheckLlmPoiExists(ctx context.Context, llmPoiID uuid.UUID) (bool, error) {
	args := m.Called(ctx, llmPoiID)
	return args.Get(0).(bool), args.Error(1)
}

func (m *MockPOIRepository) RemoveLLMPoiFromFavourite(ctx context.Context, userID, llmPoiID uuid.UUID) error {
	args := m.Called(ctx, userID, llmPoiID)
	return args.Error(0)
}

func (m *MockPOIRepository) GetFavouritePOIsByUserID(ctx context.Context, userID uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) GetFavouritePOIsByUserIDPaginated(ctx context.Context, userID uuid.UUID, limit, offset int) ([]locitypes.POIDetailedInfo, int, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Get(1).(int), args.Error(2)
}

func (m *MockPOIRepository) GetPOIsByCityID(ctx context.Context, cityID uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, cityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) FindPOIDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64) (*locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, cityID, lat, lon, tolerance)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) SavePOIDetails(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	args := m.Called(ctx, poi, cityID)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) SearchPOIs(ctx context.Context, filter locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) FindSimilarPOIs(ctx context.Context, queryEmbedding []float32, limit int) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, queryEmbedding, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) FindSimilarPOIsByCity(ctx context.Context, queryEmbedding []float32, cityID uuid.UUID, limit int) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, queryEmbedding, cityID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) SearchPOIsHybrid(ctx context.Context, filter locitypes.POIFilter, queryEmbedding []float32, semanticWeight float64) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, filter, queryEmbedding, semanticWeight)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) UpdatePOIEmbedding(ctx context.Context, poiID uuid.UUID, embedding []float32) error {
	args := m.Called(ctx, poiID, embedding)
	return args.Error(0)
}

func (m *MockPOIRepository) GetPOIsWithoutEmbeddings(ctx context.Context, limit int) ([]locitypes.POIDetailedInfo, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.POIDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) FindHotelDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64) ([]locitypes.HotelDetailedInfo, error) {
	args := m.Called(ctx, cityID, lat, lon, tolerance)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.HotelDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) SaveHotelDetails(ctx context.Context, hotel locitypes.HotelDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	args := m.Called(ctx, hotel, cityID)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) GetHotelByID(ctx context.Context, hotelID uuid.UUID) (*locitypes.HotelDetailedInfo, error) {
	args := m.Called(ctx, hotelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.HotelDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) FindRestaurantDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64, preferences *locitypes.RestaurantUserPreferences) ([]locitypes.RestaurantDetailedInfo, error) {
	args := m.Called(ctx, cityID, lat, lon, tolerance, preferences)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]locitypes.RestaurantDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) SaveRestaurantDetails(ctx context.Context, restaurant locitypes.RestaurantDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	args := m.Called(ctx, restaurant, cityID)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) GetRestaurantByID(ctx context.Context, restaurantID uuid.UUID) (*locitypes.RestaurantDetailedInfo, error) {
	args := m.Called(ctx, restaurantID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.RestaurantDetailedInfo), args.Error(1)
}

func (m *MockPOIRepository) GetItinerary(ctx context.Context, userID, itineraryID uuid.UUID) (*locitypes.UserSavedItinerary, error) {
	args := m.Called(ctx, userID, itineraryID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.UserSavedItinerary), args.Error(1)
}

func (m *MockPOIRepository) GetItineraries(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]locitypes.UserSavedItinerary, int, error) {
	args := m.Called(ctx, userID, page, pageSize)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]locitypes.UserSavedItinerary), args.Get(1).(int), args.Error(2)
}

func (m *MockPOIRepository) GetItineraryByUserIDAndCityID(ctx context.Context, userID, cityID uuid.UUID) (*locitypes.UserSavedItinerary, error) {
	args := m.Called(ctx, userID, cityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.UserSavedItinerary), args.Error(1)
}

func (m *MockPOIRepository) UpdateItinerary(ctx context.Context, userID, itineraryID uuid.UUID, updates locitypes.UpdateItineraryRequest) (*locitypes.UserSavedItinerary, error) {
	args := m.Called(ctx, userID, itineraryID, updates)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*locitypes.UserSavedItinerary), args.Error(1)
}

func (m *MockPOIRepository) SaveItinerary(ctx context.Context, userID, cityID uuid.UUID) (uuid.UUID, error) {
	args := m.Called(ctx, userID, cityID)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) SaveItineraryPOIs(ctx context.Context, itineraryID uuid.UUID, pois []locitypes.POIDetailedInfo) error {
	args := m.Called(ctx, itineraryID, pois)
	return args.Error(0)
}

func (m *MockPOIRepository) SavePOItoPointsOfInterest(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	args := m.Called(ctx, poi, cityID)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) CityExists(ctx context.Context, cityID uuid.UUID) (bool, error) {
	args := m.Called(ctx, cityID)
	return args.Get(0).(bool), args.Error(1)
}

func (m *MockPOIRepository) CalculateDistancePostGIS(ctx context.Context, userLat, userLon, poiLat, poiLon float64) (float64, error) {
	args := m.Called(ctx, userLat, userLon, poiLat, poiLon)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockPOIRepository) SaveLlmPoisToDatabase(ctx context.Context, userID uuid.UUID, pois []locitypes.POIDetailedInfo, genAIResponse *locitypes.GenAIResponse, llmInteractionID uuid.UUID) error {
	args := m.Called(ctx, userID, pois, genAIResponse, llmInteractionID)
	return args.Error(0)
}

func (m *MockPOIRepository) SaveLlmInteraction(ctx context.Context, interaction *locitypes.LlmInteraction) (uuid.UUID, error) {
	args := m.Called(ctx, interaction)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) CreateLLMPOI(ctx context.Context, poiData *locitypes.POIDetailedInfo) (uuid.UUID, error) {
	args := m.Called(ctx, poiData)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) FindLLMPOIByNameAndCity(ctx context.Context, name, city string) (uuid.UUID, error) {
	args := m.Called(ctx, name, city)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) FindLLMPOIByName(ctx context.Context, name string) (uuid.UUID, error) {
	args := m.Called(ctx, name)
	return args.Get(0).(uuid.UUID), args.Error(1)
}

func (m *MockPOIRepository) RemoveLLMPoiFromFavouriteByName(ctx context.Context, userID uuid.UUID, poiName string) error {
	args := m.Called(ctx, userID, poiName)
	return args.Error(0)
}

// Helper to setup service with mock repository
func setupPOIServiceTest() (*ServiceImpl, *MockPOIRepository, *MockCityRepository) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})) // or io.Discard
	mockRepo := new(MockPOIRepository)
	mockCityRepo := new(MockCityRepository)
	embeddingService := stubEmbeddingClient{}
	service := NewServiceImpl(mockRepo, embeddingService, mockCityRepo, stubDiscoverRepo{}, logger)
	return service, mockRepo, mockCityRepo
}

func TestPOIServiceImpl_AddPoiToFavourites(t *testing.T) {
	if os.Getenv("RUN_FULL_TESTS") == "" {
		t.Skip("Skipping POI service tests until external dependencies are configured")
	}
	service, mockRepo, _ := setupPOIServiceTest()
	ctx := context.Background()
	userID := uuid.New()
	poiID := uuid.New()
	expectedFavouriteID := uuid.New()

	t.Run("success", func(t *testing.T) {
		mockRepo.On("AddPoiToFavourites", ctx, userID, poiID).Return(expectedFavouriteID, nil).Once()

		favID, err := service.AddPoiToFavourites(ctx, userID, poiID, true)
		require.NoError(t, err)
		assert.Equal(t, expectedFavouriteID, favID)
		mockRepo.AssertExpectations(t)
	})

	t.Run("repository error", func(t *testing.T) {
		expectedErr := errors.New("db error")
		mockRepo.On("AddPoiToFavourites", ctx, userID, poiID).Return(uuid.Nil, expectedErr).Once()

		_, err := service.AddPoiToFavourites(ctx, userID, poiID, true)
		require.Error(t, err)
		assert.EqualError(t, err, expectedErr.Error()) // Service just passes through the error
		mockRepo.AssertExpectations(t)
	})
}
