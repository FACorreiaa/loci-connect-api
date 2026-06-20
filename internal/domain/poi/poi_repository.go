package poi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

var _ Repository = (*RepositoryImpl)(nil)

type Repository interface {
	SavePoi(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error)
	FindPoiByNameAndCity(ctx context.Context, name string, cityID uuid.UUID) (*locitypes.POIDetailedInfo, error)
	// GetPOIsByNamesAndCitySortedByDistance(ctx context.Context, names []string, cityID uuid.UUID, userLocation locitypes.UserLocation) ([]locitypes.POIDetailedInfo, error)
	GetPOIsByCityAndDistance(ctx context.Context, cityID uuid.UUID, userLocation locitypes.UserLocation) ([]locitypes.POIDetailedInfo, error)
	GetPOIsByLocationAndDistance(ctx context.Context, lat, lon, radiusMeters float64) ([]locitypes.POIDetailedInfo, error)
	GetPOIsByLocationAndDistanceWithCategory(ctx context.Context, lat, lon, radiusMeters float64, category string) ([]locitypes.POIDetailedInfo, error)
	// GetPOIsByLocationAndDistanceWithFilters(ctx context.Context, lat, lon, radiusMeters float64, filters map[string]string) ([]locitypes.POIDetailedInfo, error)
	AddPoiToFavourites(ctx context.Context, userID, poiID uuid.UUID) (uuid.UUID, error)
	AddLLMPoiToFavourite(ctx context.Context, userID, llmPoiID uuid.UUID) (uuid.UUID, error)
	RemovePoiFromFavourites(ctx context.Context, userID, poiID uuid.UUID) error

	RemoveLLMPoiFromFavourite(ctx context.Context, userID, llmPoiID uuid.UUID) error
	CheckPoiExists(ctx context.Context, poiID uuid.UUID) (bool, error)
	FindLLMPOIByNameAndCity(ctx context.Context, name, city string) (uuid.UUID, error)
	FindLLMPOIByName(ctx context.Context, name string) (uuid.UUID, error)
	GetFavouritePOIsByUserID(ctx context.Context, userID uuid.UUID) ([]locitypes.POIDetailedInfo, error)
	GetFavouritePOIsByUserIDPaginated(ctx context.Context, userID uuid.UUID, limit, offset int) ([]locitypes.POIDetailedInfo, int, error)
	GetPOIByID(ctx context.Context, poiID uuid.UUID) (*locitypes.POIDetailedInfo, error)
	GetPOIsByCityID(ctx context.Context, cityID uuid.UUID) ([]locitypes.POIDetailedInfo, error)

	// POI details
	FindPOIDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64) (*locitypes.POIDetailedInfo, error)
	SavePOIDetails(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error)
	SearchPOIs(ctx context.Context, filter locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error)

	// Vector similarity search methods
	FindSimilarPOIs(ctx context.Context, queryEmbedding []float32, limit int) ([]locitypes.POIDetailedInfo, error)
	FindSimilarPOIsByCity(ctx context.Context, queryEmbedding []float32, cityID uuid.UUID, limit int) ([]locitypes.POIDetailedInfo, error)
	SearchPOIsHybrid(ctx context.Context, filter locitypes.POIFilter, queryEmbedding []float32, semanticWeight float64) ([]locitypes.POIDetailedInfo, error)
	UpdatePOIEmbedding(ctx context.Context, poiID uuid.UUID, embedding []float32) error
	GetPOIsWithoutEmbeddings(ctx context.Context, limit int) ([]locitypes.POIDetailedInfo, error)

	// Hotels
	FindHotelDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64) ([]locitypes.HotelDetailedInfo, error)
	SaveHotelDetails(ctx context.Context, hotel locitypes.HotelDetailedInfo, cityID uuid.UUID) (uuid.UUID, error)
	GetHotelByID(ctx context.Context, hotelID uuid.UUID) (*locitypes.HotelDetailedInfo, error)
	// Restaurants
	FindRestaurantDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64, preferences *locitypes.RestaurantUserPreferences) ([]locitypes.RestaurantDetailedInfo, error)
	SaveRestaurantDetails(ctx context.Context, restaurant locitypes.RestaurantDetailedInfo, cityID uuid.UUID) (uuid.UUID, error)
	GetRestaurantByID(ctx context.Context, restaurantID uuid.UUID) (*locitypes.RestaurantDetailedInfo, error)
	// GetPOIsByCityIDAndCategory(ctx context.Context, cityID uuid.UUID, category string) ([]locitypes.POIDetailedInfo, error)
	// GetPOIsByCityIDAndCategories(ctx context.Context, cityID uuid.UUID, categories []string) ([]locitypes.POIDetailedInfo, error)
	// GetPOIsByCityIDAndName(ctx context.Context, cityID uuid.UUID, name string) ([]locitypes.POIDetailedInfo, error)
	// GetPOIsByCityIDAndNames(ctx context.Context, cityID uuid.UUID, names []string) ([]locitypes.POIDetailedInfo, error)
	// GetPOIsByCityIDAndNameSortedByDistance(ctx context.Context, cityID uuid.UUID, name string, userLocation locitypes.UserLocation) ([]locitypes.POIDetailedInfo, error)
	// GetPOIsByCityIDAndNamesSortedByDistance(ctx context.Context, cityID uuid.UUID, names []string, userLocation locitypes.UserLocation) ([]locitypes.POIDetailedInfo, error)

	// AddPersonalizedPOItoFavourites(ctx context.Context, poiID uuid.UUID, userID uuid.UUID) (uuid.UUID, error)

	GetItinerary(ctx context.Context, userID, itineraryID uuid.UUID) (*locitypes.UserSavedItinerary, error)
	GetItineraries(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]locitypes.UserSavedItinerary, int, error)
	UpdateItinerary(ctx context.Context, userID, itineraryID uuid.UUID, updates locitypes.UpdateItineraryRequest) (*locitypes.UserSavedItinerary, error)
	SaveItinerary(ctx context.Context, userID, cityID uuid.UUID) (uuid.UUID, error)
	SaveItineraryPOIs(ctx context.Context, itineraryID uuid.UUID, pois []locitypes.POIDetailedInfo) error
	SavePOItoPointsOfInterest(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error)
	CityExists(ctx context.Context, cityID uuid.UUID) (bool, error)

	// Distance
	CalculateDistancePostGIS(ctx context.Context, userLat, userLon, poiLat, poiLon float64) (float64, error)
	SaveLlmPoisToDatabase(ctx context.Context, userID uuid.UUID, pois []locitypes.POIDetailedInfo, genAIResponse *locitypes.GenAIResponse, llmInteractionID uuid.UUID) error
	SaveLlmInteraction(ctx context.Context, interaction *locitypes.LlmInteraction) (uuid.UUID, error)
}

type RepositoryImpl struct {
	logger *slog.Logger
	pgpool PgxPool
}

// PgxPool abstracts the pgxpool.Pool for easier testing.
type PgxPool interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
}

var _ PgxPool = (*pgxpool.Pool)(nil)

func NewRepository(pgxpool PgxPool, logger *slog.Logger) *RepositoryImpl {
	return &RepositoryImpl{
		logger: logger,
		pgpool: pgxpool,
	}
}

func (r *RepositoryImpl) SavePoi(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	// Validate coordinates before opening a transaction
	if poi.Latitude < -90 || poi.Latitude > 90 || poi.Longitude < -180 || poi.Longitude > 180 {
		return uuid.Nil, fmt.Errorf("invalid coordinates: lat=%f, lon=%f", poi.Latitude, poi.Longitude)
	}
	if poi.Name == "" {
		return uuid.Nil, fmt.Errorf("POI name is required")
	}

	query := `
        INSERT INTO points_of_interest (
            name, description, location, city_id, poi_type, source, ai_summary
        ) VALUES (
            $1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326), $5, $6, $7, $8
        ) RETURNING id
    `
	var id uuid.UUID
	if err := db.WithTx(ctx, r.pgpool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, query,
			poi.Name, poi.DescriptionPOI, poi.Longitude, poi.Latitude, cityID,
			poi.Category, "loci_ai", poi.DescriptionPOI,
		).Scan(&id); err != nil {
			return fmt.Errorf("failed to insert POI: %w", err)
		}
		return nil
	}); err != nil {
		return uuid.Nil, err
	}
	// Log the successful insertion
	r.logger.Info("POI saved successfully", slog.String("name", poi.Name), slog.String("id", id.String()))

	return id, nil
}

func (r *RepositoryImpl) FindPoiByNameAndCity(ctx context.Context, name string, cityID uuid.UUID) (*locitypes.POIDetailedInfo, error) {
	query := `
        SELECT name, description, ST_Y(location) as lat, ST_X(location) as lon, COALESCE(poi_type, '') AS category
        FROM points_of_interest
        WHERE name = $1 AND city_id = $2
    `
	rows, err := r.pgpool.Query(ctx, query, name, cityID)
	if err != nil {
		return nil, fmt.Errorf("failed to find POI: %w", err)
	}

	type poiRow struct {
		Name           string  `db:"name"`
		DescriptionPOI string  `db:"description"`
		Latitude       float64 `db:"lat"`
		Longitude      float64 `db:"lon"`
		Category       string  `db:"category"`
	}

	poiDbRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[poiRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to collect POI: %w", err)
	}
	// Log the successful retrieval
	r.logger.Info("POI found successfully",
		slog.String("name", poiDbRow.Name),
		slog.Float64("latitude", poiDbRow.Latitude),
		slog.Float64("longitude", poiDbRow.Longitude),
		slog.String("cityID", cityID.String()))

	return &locitypes.POIDetailedInfo{
		Name:           poiDbRow.Name,
		DescriptionPOI: poiDbRow.DescriptionPOI,
		Latitude:       poiDbRow.Latitude,
		Longitude:      poiDbRow.Longitude,
		Category:       poiDbRow.Category,
	}, nil
}

// poiCityDistanceRow is a DB row struct for GetPOIsByCityAndDistance query
type poiCityDistanceRow struct {
	ID             uuid.UUID `db:"id"`
	Name           string    `db:"name"`
	Longitude      float64   `db:"longitude"`
	Latitude       float64   `db:"latitude"`
	Category       string    `db:"category"`
	DescriptionPOI string    `db:"description_poi"`
	Distance       float64   `db:"distance"`
}

func (r *RepositoryImpl) GetPOIsByCityAndDistance(ctx context.Context, cityID uuid.UUID, userLocation locitypes.UserLocation) ([]locitypes.POIDetailedInfo, error) {
	userPoint := fmt.Sprintf("SRID=4326;POINT(%f %f)", userLocation.UserLon, userLocation.UserLat)
	query := `
        SELECT
            id,
            name,
            ST_X(location::geometry) AS longitude,
            ST_Y(location::geometry) AS latitude,
            COALESCE(poi_type, '') AS category,
            COALESCE(ai_summary, '') AS description_poi,
            ST_Distance(location::geography, ST_GeomFromText($1, 4326)::geography) AS distance
        FROM points_of_interest
        WHERE city_id = $2 AND ST_DWithin(location::geography, ST_GeomFromText($1, 4326)::geography, $3 * 1000)
        ORDER BY distance ASC
    `
	rows, err := r.pgpool.Query(ctx, query, userPoint, cityID, userLocation.SearchRadiusKm)
	if err != nil {
		return nil, fmt.Errorf("failed to query POIs: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[poiCityDistanceRow])
	if err != nil {
		return nil, fmt.Errorf("failed to collect POI rows: %w", err)
	}

	// Convert DB rows to domain type
	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
			DescriptionPOI: row.DescriptionPOI,
			Distance:       row.Distance,
		}
	}

	return pois, nil
}

func (r *RepositoryImpl) CheckPoiExists(ctx context.Context, poiID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM points_of_interest WHERE id = $1)`
	var exists bool
	if err := r.pgpool.QueryRow(ctx, query, poiID).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to query points_of_interest: %w", err)
	}
	return exists, nil
}

func (r *RepositoryImpl) AddPoiToFavourites(ctx context.Context, userID, poiID uuid.UUID) (uuid.UUID, error) {
	query := `
        INSERT INTO user_favorite_pois (user_id, poi_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, poi_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING id
    `
	var id uuid.UUID
	if err := r.pgpool.QueryRow(ctx, query, userID, poiID).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("failed to add POI to favourites: %w", err)
	}
	return id, nil
}

func (r *RepositoryImpl) AddLLMPoiToFavourite(ctx context.Context, userID, llmPoiID uuid.UUID) (uuid.UUID, error) {
	query := `
        INSERT INTO user_favorite_llm_pois (user_id, llm_poi_id)
        VALUES ($1, $2)
        ON CONFLICT (user_id, llm_poi_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING id
    `
	var id uuid.UUID
	if err := r.pgpool.QueryRow(ctx, query, userID, llmPoiID).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("failed to insert into user_favorite_llm_pois: %w", err)
	}
	return id, nil
}

func (r *RepositoryImpl) RemovePoiFromFavourites(ctx context.Context, userID, poiID uuid.UUID) error {
	query := `
		DELETE FROM user_favorite_pois
		WHERE user_id = $1 AND poi_id = $2
	`
	result, err := r.pgpool.Exec(ctx, query, userID, poiID)
	if err != nil {
		return fmt.Errorf("failed to remove POI from favourites: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("no favourite POI found to remove")
	}
	return nil
}

func (r *RepositoryImpl) RemoveLLMPoiFromFavourite(ctx context.Context, userID, llmPoiID uuid.UUID) error {
	// Try direct removal first
	query := `
		DELETE FROM user_favorite_llm_pois
		WHERE user_id = $1 AND llm_poi_id = $2
	`
	result, err := r.pgpool.Exec(ctx, query, userID, llmPoiID)
	if err != nil {
		return fmt.Errorf("failed to remove LLM POI from favourites: %w", err)
	}

	rowsAffected := result.RowsAffected()
	r.logger.InfoContext(ctx, "Delete query result", slog.Int64("rows_affected", rowsAffected))

	if rowsAffected == 0 {
		return fmt.Errorf("no favourite LLM POI found to remove")
	}
	return nil
}

// favoritePOIRow is a DB row struct for GetFavouritePOIsByUserID query
type favoritePOIRow struct {
	FavoriteID     uuid.UUID `db:"favorite_id"`
	Notes          string    `db:"notes"`
	AddedAt        time.Time `db:"added_at"`
	ID             uuid.UUID `db:"id"`
	Name           string    `db:"name"`
	Longitude      float64   `db:"longitude"`
	Latitude       float64   `db:"latitude"`
	Category       string    `db:"category"`
	DescriptionPOI string    `db:"description_poi"`
	Address        string    `db:"address"`
	Website        string    `db:"website"`
	PhoneNumber    string    `db:"phone_number"`
	OpeningHours   string    `db:"opening_hours"`
	Rating         float64   `db:"rating"`
	PriceLevel     string    `db:"price_level"`
	POISource      string    `db:"poi_source"`
}

func (r *RepositoryImpl) GetFavouritePOIsByUserID(ctx context.Context, userID uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	query := `
		SELECT
			favorite_id,
			COALESCE(notes, '') AS notes,
			added_at,
			id,
			name,
			longitude,
			latitude,
			COALESCE(category, '') AS category,
			COALESCE(description_poi, '') AS description_poi,
			COALESCE(address, '') AS address,
			COALESCE(website, '') AS website,
			COALESCE(phone_number, '') AS phone_number,
			COALESCE(opening_hours::text, '') AS opening_hours,
			COALESCE(rating, 0) AS rating,
			COALESCE(price_level, '') AS price_level,
			poi_source
		FROM (
			SELECT
				ufp.id as favorite_id,
				ufp.notes,
				ufp.added_at,
				poi.id,
				poi.name,
				ST_X(poi.location) AS longitude,
				ST_Y(poi.location) AS latitude,
				poi.poi_type AS category,
				poi.description AS description_poi,
				poi.address,
				poi.website,
				poi.phone_number,
				poi.opening_hours,
				poi.average_rating as rating,
				poi.price_level::text as price_level,
				'regular' as poi_source
			FROM user_favorite_pois ufp
			INNER JOIN points_of_interest poi ON ufp.poi_id = poi.id
			WHERE ufp.user_id = $1

			UNION ALL

			SELECT
				uflp.id as favorite_id,
				uflp.notes,
				uflp.added_at,
				llmsp.id,
				llmsp.name,
				llmsp.longitude,
				llmsp.latitude,
				llmsp.category,
				llmsp.description AS description_poi,
				llmsp.address,
				llmsp.website,
				llmsp.phone_number,
				llmsp.opening_hours,
				llmsp.rating,
				llmsp.price_level,
				'llm' as poi_source
			FROM user_favorite_llm_pois uflp
			INNER JOIN llm_suggested_pois as llmsp ON uflp.llm_poi_id = llmsp.id
			WHERE uflp.user_id = $1
		) combined_favorites
		ORDER BY added_at DESC
	`
	rows, err := r.pgpool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query favourite POIs: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[favoritePOIRow])
	if err != nil {
		return nil, fmt.Errorf("failed to collect favourite POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
			DescriptionPOI: row.DescriptionPOI,
			Address:        row.Address,
			Website:        row.Website,
			PhoneNumber:    row.PhoneNumber,
			Rating:         row.Rating,
			PriceLevel:     row.PriceLevel,
		}
	}

	r.logger.Info("Favourite POIs retrieved successfully", slog.String("userID", userID.String()), slog.Int("count", len(pois)))
	return pois, nil
}

func (r *RepositoryImpl) GetFavouritePOIsByUserIDPaginated(ctx context.Context, userID uuid.UUID, limit, offset int) ([]locitypes.POIDetailedInfo, int, error) {
	// First get the total count
	countQuery := `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM user_favorite_pois ufp
			INNER JOIN points_of_interest poi ON ufp.poi_id = poi.id
			WHERE ufp.user_id = $1

			UNION ALL

			SELECT 1 FROM user_favorite_llm_pois uflp
			INNER JOIN llm_suggested_pois ON uflp.llm_poi_id = llm_suggested_pois.id
			WHERE uflp.user_id = $1
		) combined_count
	`

	var totalCount int
	if err := r.pgpool.QueryRow(ctx, countQuery, userID).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("failed to count favourite POIs: %w", err)
	}

	// Then get the paginated results with COALESCE for nullable fields
	query := `
		SELECT
			favorite_id,
			COALESCE(notes, '') AS notes,
			added_at,
			id,
			name,
			longitude,
			latitude,
			COALESCE(category, '') AS category,
			COALESCE(description_poi, '') AS description_poi,
			COALESCE(address, '') AS address,
			COALESCE(website, '') AS website,
			COALESCE(phone_number, '') AS phone_number,
			COALESCE(opening_hours::text, '') AS opening_hours,
			COALESCE(rating, 0) AS rating,
			COALESCE(price_level, '') AS price_level,
			poi_source
		FROM (
			SELECT
				ufp.id as favorite_id,
				ufp.notes,
				ufp.added_at,
				poi.id,
				poi.name,
				ST_X(poi.location) AS longitude,
				ST_Y(poi.location) AS latitude,
				poi.poi_type AS category,
				poi.description AS description_poi,
				poi.address,
				poi.website,
				poi.phone_number,
				poi.opening_hours,
				poi.average_rating as rating,
				poi.price_level::text as price_level,
				'regular' as poi_source
			FROM user_favorite_pois ufp
			INNER JOIN points_of_interest poi ON ufp.poi_id = poi.id
			WHERE ufp.user_id = $1

			UNION ALL

			SELECT
				uflp.id as favorite_id,
				uflp.notes,
				uflp.added_at,
				llmsp.id,
				llmsp.name,
				llmsp.longitude,
				llmsp.latitude,
				llmsp.category,
				llmsp.description AS description_poi,
				llmsp.address,
				llmsp.website,
				llmsp.phone_number,
				llmsp.opening_hours,
				llmsp.rating,
				llmsp.price_level,
				'llm' as poi_source
			FROM user_favorite_llm_pois uflp
			INNER JOIN llm_suggested_pois llmsp ON uflp.llm_poi_id = llmsp.id
			WHERE uflp.user_id = $1
		) combined_favorites
		ORDER BY added_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.pgpool.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query favourite POIs: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[favoritePOIRow])
	if err != nil {
		return nil, 0, fmt.Errorf("failed to collect favourite POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
			DescriptionPOI: row.DescriptionPOI,
			Address:        row.Address,
			Website:        row.Website,
			PhoneNumber:    row.PhoneNumber,
			Rating:         row.Rating,
			PriceLevel:     row.PriceLevel,
		}
	}

	r.logger.Info("Paginated favourite POIs retrieved successfully",
		slog.String("userID", userID.String()),
		slog.Int("count", len(pois)),
		slog.Int("total", totalCount),
		slog.Int("limit", limit),
		slog.Int("offset", offset))
	return pois, totalCount, nil
}

// poiCityIDRow is a DB row struct for GetPOIsByCityID query
type poiCityIDRow struct {
	ID             uuid.UUID `db:"id"`
	Name           string    `db:"name"`
	DescriptionPOI string    `db:"description"`
	Longitude      float64   `db:"longitude"`
	Latitude       float64   `db:"latitude"`
	Category       string    `db:"poi_type"`
}

func (r *RepositoryImpl) GetPOIsByCityID(ctx context.Context, cityID uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	query := `
		SELECT 
			id, 
			name, 
			COALESCE(description, '') AS description, 
			ST_X(location) AS longitude, 
			ST_Y(location) AS latitude, 
			COALESCE(poi_type, '') AS poi_type
		FROM points_of_interest
		WHERE city_id = $1
	`
	rows, err := r.pgpool.Query(ctx, query, cityID)
	if err != nil {
		return nil, fmt.Errorf("failed to query POIs by city ID: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[poiCityIDRow])
	if err != nil {
		return nil, fmt.Errorf("failed to collect POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			DescriptionPOI: row.DescriptionPOI,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
		}
	}

	r.logger.Info("POIs retrieved successfully by city ID", slog.String("cityID", cityID.String()), slog.Int("count", len(pois)))
	return pois, nil
}

func (r *RepositoryImpl) GetPOIByID(ctx context.Context, poiID uuid.UUID) (*locitypes.POIDetailedInfo, error) {
	query := `
		SELECT
			id,
			name,
			COALESCE(description, '') AS description,
			ST_X(location) AS longitude,
			ST_Y(location) AS latitude,
			COALESCE(category, '') AS category,
			COALESCE(address, '') AS address,
			COALESCE(website, '') AS website,
			COALESCE(phone_number, '') AS phone_number,
			opening_hours,
			COALESCE(price_level::text, '') AS price_level,
			COALESCE(average_rating, 0) AS rating,
			city_id,
			COALESCE(tags, '{}') AS tags,
			COALESCE(images, '{}') AS images,
			created_at
		FROM points_of_interest
		WHERE id = $1
	`

	type poiByIDRow struct {
		ID           uuid.UUID         `db:"id"`
		Name         string            `db:"name"`
		Description  string            `db:"description"`
		Longitude    float64           `db:"longitude"`
		Latitude     float64           `db:"latitude"`
		Category     string            `db:"category"`
		Address      string            `db:"address"`
		Website      string            `db:"website"`
		PhoneNumber  string            `db:"phone_number"`
		OpeningHours map[string]string `db:"opening_hours"`
		PriceLevel   string            `db:"price_level"`
		Rating       float64           `db:"rating"`
		CityID       uuid.UUID         `db:"city_id"`
		Tags         []string          `db:"tags"`
		Images       []string          `db:"images"`
		CreatedAt    time.Time         `db:"created_at"`
	}

	rows, err := r.pgpool.Query(ctx, query, poiID)
	if err != nil {
		return nil, fmt.Errorf("failed to query POI by ID: %w", err)
	}

	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[poiByIDRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to collect POI by ID: %w", err)
	}

	return &locitypes.POIDetailedInfo{
		ID:           row.ID,
		Name:         row.Name,
		Description:  row.Description,
		Longitude:    row.Longitude,
		Latitude:     row.Latitude,
		Category:     row.Category,
		Address:      row.Address,
		Website:      row.Website,
		PhoneNumber:  row.PhoneNumber,
		OpeningHours: row.OpeningHours,
		PriceLevel:   row.PriceLevel,
		Rating:       row.Rating,
		CityID:       row.CityID,
		Tags:         row.Tags,
		Images:       row.Images,
		CreatedAt:    row.CreatedAt,
	}, nil
}

func (r *RepositoryImpl) FindPOIDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64) (*locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("Repository").Start(ctx, "FindPOIDetailedInfos", trace.WithAttributes(
		attribute.String("city.id", cityID.String()),
		attribute.Float64("latitude", lat),
		attribute.Float64("longitude", lon),
	))
	defer span.End()

	query := `
        SELECT
            id, name, description, latitude, longitude, address, website, phone_number,
            opening_hours, price_range, category, tags, images, rating, llm_interaction_id
        FROM poi_details
        WHERE city_id = $1
        AND ST_DWithin(
            location::geography,
            ST_SetSRID(ST_MakePoint($2, $3)::geography, 4326),
            $4
        )
        LIMIT 1
    `
	rows, err := r.pgpool.Query(ctx, query, cityID, lon, lat, tolerance)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to query POI details")
		return nil, fmt.Errorf("failed to query poi_details: %w", err)
	}

	type poiDetailsRow struct {
		ID               uuid.UUID         `db:"id"`
		Name             string            `db:"name"`
		Description      string            `db:"description"`
		Latitude         float64           `db:"latitude"`
		Longitude        float64           `db:"longitude"`
		Address          string            `db:"address"`
		Website          string            `db:"website"`
		PhoneNumber      string            `db:"phone_number"`
		OpeningHours     map[string]string `db:"opening_hours"`
		PriceRange       string            `db:"price_range"`
		Category         string            `db:"category"`
		Tags             []string          `db:"tags"`
		Images           []string          `db:"images"`
		Rating           float64           `db:"rating"`
		LlmInteractionID *uuid.UUID        `db:"llm_interaction_id"`
	}

	dbRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[poiDetailsRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			span.SetStatus(codes.Ok, "No POI found")
			return nil, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to read POI details")
		return nil, fmt.Errorf("failed to read poi_details: %w", err)
	}

	poi := locitypes.POIDetailedInfo{
		ID:               dbRow.ID,
		Name:             dbRow.Name,
		Description:      dbRow.Description,
		Latitude:         dbRow.Latitude,
		Longitude:        dbRow.Longitude,
		Address:          dbRow.Address,
		Website:          dbRow.Website,
		PhoneNumber:      dbRow.PhoneNumber,
		OpeningHours:     dbRow.OpeningHours,
		PriceRange:       dbRow.PriceRange,
		Category:         dbRow.Category,
		Tags:             dbRow.Tags,
		Images:           dbRow.Images,
		Rating:           dbRow.Rating,
		LlmInteractionID: uuid.Nil,
	}
	if dbRow.LlmInteractionID != nil {
		poi.LlmInteractionID = *dbRow.LlmInteractionID
	}

	span.SetStatus(codes.Ok, "POI details found")
	return &poi, nil
}

func (r *RepositoryImpl) SavePOIDetails(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	ctx, span := otel.Tracer("Repository").Start(ctx, "SavePOIDetailedInfos", trace.WithAttributes(
		attribute.String("city.id", func() string {
			return "null"
		}()),
		attribute.String("poi.name", poi.Name),
	))
	defer span.End()

	// Validate coordinates
	if poi.Latitude < -90 || poi.Latitude > 90 || poi.Longitude < -180 || poi.Longitude > 180 {
		err := fmt.Errorf("invalid coordinates: lat=%f, lon=%f", poi.Latitude, poi.Longitude)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Invalid coordinates")
		return uuid.Nil, err
	}

	// Check for duplicate POI by name and location (within 100m radius)
	// Updated to work without city constraint for discover endpoint
	duplicateCheckQuery := `
		SELECT id FROM poi_details
		WHERE LOWER(name) = LOWER($1)
		AND ST_DWithin(
			location::geography,
			ST_SetSRID(ST_MakePoint($2, $3)::geography, 4326),
			100
		)
		LIMIT 1
	`
	var existingID uuid.UUID
	err := r.pgpool.QueryRow(ctx, duplicateCheckQuery, poi.Name, poi.Longitude, poi.Latitude).Scan(&existingID)
	if err == nil {
		// Duplicate found
		r.logger.InfoContext(ctx, "POI already exists, skipping save",
			slog.String("poi_name", poi.Name),
			slog.String("existing_id", existingID.String()),
			slog.String("city_id", func() string {
				return "null"
			}()))
		span.SetAttributes(attribute.String("poi.existing_id", existingID.String()))
		span.SetStatus(codes.Ok, "POI already exists")
		return existingID, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		// Unexpected error
		r.logger.WarnContext(ctx, "Error checking for duplicate POI",
			slog.Any("error", err),
			slog.String("poi_name", poi.Name))
	}

	// Start a transaction to ensure both tables are updated atomically
	poiID := uuid.New()
	if err := db.WithTxBegin(ctx, r.pgpool, func(tx pgx.Tx) error {
		// Insert into poi_details table
		POIDetailedInfosQuery := `
        INSERT INTO poi_details (
            id, city_id, name, description, latitude, longitude, location,
            address, website, phone_number, opening_hours, price_range, category,
            tags, images, rating, llm_interaction_id
        ) VALUES (
            $1, $2, $3, $4, $5, $6, ST_SetSRID(ST_MakePoint($7, $8), 4326),
            $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
        )
    `
		if _, err := tx.Exec(ctx, POIDetailedInfosQuery,
			poiID, cityID, poi.Name, poi.Description, poi.Latitude, poi.Longitude,
			poi.Longitude, poi.Latitude, // lon, lat for ST_MakePoint
			poi.Address, poi.Website, poi.PhoneNumber, poi.OpeningHours,
			poi.PriceRange, poi.Category, poi.Tags, poi.Images, poi.Rating,
			func() any {
				if poi.LlmInteractionID == uuid.Nil {
					return nil
				}
				return poi.LlmInteractionID
			}(),
		); err != nil {
			r.logger.ErrorContext(ctx, "Failed to save POI details",
				slog.Any("error", err),
				slog.String("poi_name", poi.Name),
				slog.String("poi_id", poiID.String()))
			span.RecordError(err)
			span.SetStatus(codes.Error, "Failed to save poi_details")
			return fmt.Errorf("failed to save poi_details: %w", err)
		}

		// Convert price_range to price_level for points_of_interest
		var priceLevel *int
		if poi.PriceRange != "" {
			switch poi.PriceRange {
			case "€", "$", "free", "Free", "1":
				level := 1
				priceLevel = &level
			case "€€", "$$", "budget", "Budget", "2":
				level := 2
				priceLevel = &level
			case "€€€", "$$$", "moderate", "Moderate", "3":
				level := 3
				priceLevel = &level
			case "€€€€", "$$$$", "expensive", "Expensive", "4":
				level := 4
				priceLevel = &level
			case "luxury", "Luxury", "premium", "Premium", "5":
				level := 5
				priceLevel = &level
			default:
				r.logger.WarnContext(ctx, "Unknown price range",
					slog.String("price_range", poi.PriceRange),
					slog.String("poi_name", poi.Name))
				// Default to level 2 (budget) for unknown price ranges
				level := 2
				priceLevel = &level
			}
		}

		// Insert into points_of_interest table
		poisQuery := `
        INSERT INTO points_of_interest (
            id, name, description, location, city_id, address, poi_type,
            website, phone_number, opening_hours, category, price_level,
            average_rating, source, ai_summary, tags
        ) VALUES (
            $1, $2, $3, ST_SetSRID(ST_MakePoint($4, $5), 4326), $6, $7, $8,
            $9, $10, $11, $12, $13, $14, $15, $16, $17
        )
    `
		if _, err := tx.Exec(ctx, poisQuery,
			poiID, poi.Name, poi.Description,
			poi.Longitude, poi.Latitude, // lon, lat for ST_MakePoint
			cityID, poi.Address, poi.Category,
			poi.Website, poi.PhoneNumber, poi.OpeningHours,
			poi.Category, priceLevel, poi.Rating,
			"loci_ai", poi.Description, poi.Tags,
		); err != nil {
			r.logger.ErrorContext(ctx, "Failed to save POI to points_of_interest",
				slog.Any("error", err),
				slog.String("poi_name", poi.Name),
				slog.String("poi_id", poiID.String()))
			span.RecordError(err)
			span.SetStatus(codes.Error, "Failed to save POI to points_of_interest")
			return fmt.Errorf("failed to save points_of_interest: %w", err)
		}
		return nil
	}); err != nil {
		return uuid.Nil, err
	}

	r.logger.InfoContext(ctx, "Successfully saved POI to database",
		slog.String("poi_name", poi.Name),
		slog.String("poi_id", poiID.String()),
		slog.String("city_id", func() string {
			return "null"
		}()),
		slog.Float64("latitude", poi.Latitude),
		slog.Float64("longitude", poi.Longitude))

	span.SetAttributes(attribute.String("poi.id", poiID.String()))
	span.SetStatus(codes.Ok, "POI details saved successfully to both tables")
	return poiID, nil
}

// hotelDetailsRow is a DB row struct for FindHotelDetails query
type hotelDetailsRow struct {
	ID               uuid.UUID  `db:"id"`
	Name             string     `db:"name"`
	Description      string     `db:"description"`
	Latitude         float64    `db:"latitude"`
	Longitude        float64    `db:"longitude"`
	Address          string     `db:"address"`
	Website          string     `db:"website"`
	PhoneNumber      string     `db:"phone_number"`
	OpeningHours     string     `db:"opening_hours"`
	PriceRange       string     `db:"price_range"`
	Category         string     `db:"category"`
	Tags             []string   `db:"tags"`
	Images           []string   `db:"images"`
	Rating           float64    `db:"rating"`
	LlmInteractionID *uuid.UUID `db:"llm_interaction_id"`
}

func (r *RepositoryImpl) FindHotelDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64) ([]locitypes.HotelDetailedInfo, error) {
	ctx, span := otel.Tracer("HotelRepository").Start(ctx, "FindHotelDetails", trace.WithAttributes(
		attribute.String("city.id", cityID.String()),
		attribute.Float64("latitude", lat),
		attribute.Float64("longitude", lon),
	))
	defer span.End()

	query := `
        SELECT
            id, name, COALESCE(description, '') AS description, latitude, longitude, 
            COALESCE(address, '') AS address, COALESCE(website, '') AS website, 
            COALESCE(phone_number, '') AS phone_number, COALESCE(opening_hours::text, '') AS opening_hours, 
            COALESCE(price_range, '') AS price_range, COALESCE(category, '') AS category, 
            COALESCE(tags, '{}') AS tags, COALESCE(images, '{}') AS images, 
            COALESCE(rating, 0) AS rating, llm_interaction_id
        FROM hotel_details
        WHERE city_id = $1
        AND ST_DWithin(
            location::geography,
            ST_SetSRID(ST_MakePoint($2, $3)::geography, 4326),
            $4
        )
    `
	rows, err := r.pgpool.Query(ctx, query, cityID, lon, lat, tolerance)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to query hotel details")
		return nil, fmt.Errorf("failed to query hotel_details: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[hotelDetailsRow])
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to collect hotel details")
		return nil, fmt.Errorf("failed to collect hotel_details: %w", err)
	}

	hotels := make([]locitypes.HotelDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		var website, phoneNumber, openingHours, priceRange *string
		if row.Website != "" {
			website = &row.Website
		}
		if row.PhoneNumber != "" {
			phoneNumber = &row.PhoneNumber
		}
		if row.OpeningHours != "" {
			openingHours = &row.OpeningHours
		}
		if row.PriceRange != "" {
			priceRange = &row.PriceRange
		}
		hotels[i] = locitypes.HotelDetailedInfo{
			ID:           row.ID,
			Name:         row.Name,
			Description:  row.Description,
			Latitude:     row.Latitude,
			Longitude:    row.Longitude,
			Address:      row.Address,
			Website:      website,
			PhoneNumber:  phoneNumber,
			OpeningHours: openingHours,
			PriceRange:   priceRange,
			Category:     row.Category,
			Tags:         row.Tags,
			Images:       row.Images,
			Rating:       row.Rating,
			LlmInteractionID: func() uuid.UUID {
				if row.LlmInteractionID == nil {
					return uuid.Nil
				}
				return *row.LlmInteractionID
			}(),
		}
	}

	span.SetStatus(codes.Ok, "Hotel details found")
	return hotels, nil
}

func (r *RepositoryImpl) SaveHotelDetails(ctx context.Context, hotel locitypes.HotelDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	ctx, span := otel.Tracer("HotelRepository").Start(ctx, "SaveHotelDetails", trace.WithAttributes(
		attribute.String("city.id", cityID.String()),
		attribute.String("hotel.name", hotel.Name),
	))
	defer span.End()

	var openingHours *string
	if hotel.OpeningHours != nil && *hotel.OpeningHours != "" {
		// Verify it's valid JSON
		if json.Valid([]byte(*hotel.OpeningHours)) {
			openingHours = hotel.OpeningHours
		} else {
			// Log warning and set to nil if invalid
			r.logger.WarnContext(ctx, "Invalid JSON for opening_hours, setting to NULL", slog.String("value", *hotel.OpeningHours))
			openingHours = nil
		}
	}

	query := `
        INSERT INTO hotel_details (
            id, city_id, name, description, latitude, longitude, location,
            address, website, phone_number, opening_hours, price_range, category,
            tags, images, rating, llm_interaction_id
        ) VALUES (
            $1, $2, $3, $4, $5, $6, ST_SetSRID(ST_MakePoint($7, $8), 4326),
            $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
        )
        RETURNING id
    `
	var id uuid.UUID
	err := r.pgpool.QueryRow(ctx, query,
		uuid.New(), cityID, hotel.Name, hotel.Description, hotel.Latitude, hotel.Longitude,
		hotel.Longitude, hotel.Latitude, // lon, lat for ST_MakePoint
		hotel.Address, hotel.Website, hotel.PhoneNumber, openingHours,
		hotel.PriceRange, hotel.Category, hotel.Tags, hotel.Images, hotel.Rating,
		func() any {
			if hotel.LlmInteractionID == uuid.Nil {
				return nil
			}
			return hotel.LlmInteractionID
		}(),
	).Scan(&id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to save hotel details")
		return uuid.Nil, fmt.Errorf("failed to save hotel_details: %w", err)
	}

	span.SetAttributes(attribute.String("hotel.id", id.String()))
	span.SetStatus(codes.Ok, "Hotel details saved successfully")
	return id, nil
}

func (r *RepositoryImpl) GetHotelByID(ctx context.Context, hotelID uuid.UUID) (*locitypes.HotelDetailedInfo, error) {
	ctx, span := otel.Tracer("HotelRepository").Start(ctx, "GetHotelByID", trace.WithAttributes(
		attribute.String("hotel.id", hotelID.String()),
	))
	defer span.End()

	query := `
		SELECT
			id, name, description, latitude, longitude, address, website, phone_number,
			opening_hours, price_range, category, tags, images, rating, llm_interaction_id
		FROM hotel_details
		WHERE id = $1
	`
	rows, err := r.pgpool.Query(ctx, query, hotelID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to query hotel details by ID")
		return nil, fmt.Errorf("failed to query hotel_details by ID: %w", err)
	}

	dbRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[hotelDetailsRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			span.SetStatus(codes.Ok, "No hotel found")
			return nil, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to collect hotel details by ID")
		return nil, fmt.Errorf("failed to collect hotel_details by ID: %w", err)
	}

	var openingHours *string
	if dbRow.OpeningHours != "" {
		openingHours = &dbRow.OpeningHours
	}
	var website *string
	if dbRow.Website != "" {
		website = &dbRow.Website
	}
	var phoneNumber *string
	if dbRow.PhoneNumber != "" {
		phoneNumber = &dbRow.PhoneNumber
	}
	var priceRange *string
	if dbRow.PriceRange != "" {
		priceRange = &dbRow.PriceRange
	}

	hotel := locitypes.HotelDetailedInfo{
		ID:               dbRow.ID,
		Name:             dbRow.Name,
		Description:      dbRow.Description,
		Latitude:         dbRow.Latitude,
		Longitude:        dbRow.Longitude,
		Address:          dbRow.Address,
		Website:          website,
		PhoneNumber:      phoneNumber,
		OpeningHours:     openingHours,
		PriceRange:       priceRange,
		Category:         dbRow.Category,
		Tags:             dbRow.Tags,
		Images:           dbRow.Images,
		Rating:           dbRow.Rating,
		LlmInteractionID: uuid.Nil,
	}
	if dbRow.LlmInteractionID != nil {
		hotel.LlmInteractionID = *dbRow.LlmInteractionID
	}

	span.SetStatus(codes.Ok, "Hotel details found by ID")
	return &hotel, nil
}

// restaurantDetailsRow is a DB row struct for FindRestaurantDetails query
type restaurantDetailsRow struct {
	ID               uuid.UUID  `db:"id"`
	Name             string     `db:"name"`
	Description      string     `db:"description"`
	Latitude         float64    `db:"latitude"`
	Longitude        float64    `db:"longitude"`
	Address          string     `db:"address"`
	Website          string     `db:"website"`
	PhoneNumber      string     `db:"phone_number"`
	OpeningHours     string     `db:"opening_hours"`
	PriceLevel       string     `db:"price_level"`
	Category         string     `db:"category"`
	Tags             []string   `db:"tags"`
	Images           []string   `db:"images"`
	Rating           float64    `db:"rating"`
	CuisineType      string     `db:"cuisine_type"`
	LlmInteractionID *uuid.UUID `db:"llm_interaction_id"`
}

func (r *RepositoryImpl) FindRestaurantDetails(ctx context.Context, cityID uuid.UUID, lat, lon, tolerance float64, preferences *locitypes.RestaurantUserPreferences) ([]locitypes.RestaurantDetailedInfo, error) {
	ctx, span := otel.Tracer("RestaurantRepository").Start(ctx, "FindRestaurantDetails")
	defer span.End()

	query := `
        SELECT
            id, name, COALESCE(description, '') AS description, latitude, longitude, 
            COALESCE(address, '') AS address, COALESCE(website, '') AS website, 
            COALESCE(phone_number, '') AS phone_number, COALESCE(opening_hours::text, '') AS opening_hours,
            COALESCE(price_level, '') AS price_level, COALESCE(category, '') AS category, 
            COALESCE(tags, '{}') AS tags, COALESCE(images, '{}') AS images, 
            COALESCE(rating, 0) AS rating, COALESCE(cuisine_type, '') AS cuisine_type, llm_interaction_id
        FROM restaurant_details
        WHERE city_id = $1
        AND ST_DWithin(
            location::geography,
            ST_SetSRID(ST_MakePoint($2, $3)::geography, 4326),
            $4
        )
    `
	args := []any{cityID, lon, lat, tolerance}
	if preferences != nil {
		if preferences.PreferredCuisine != "" {
			query += ` AND cuisine_type = $5`
			args = append(args, preferences.PreferredCuisine)
		}
		if preferences.PreferredPriceRange != "" {
			query += fmt.Sprintf(` AND price_level = $%d`, len(args)+1)
			args = append(args, preferences.PreferredPriceRange)
		}
	}

	rows, err := r.pgpool.Query(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to query restaurants")
		return nil, fmt.Errorf("failed to query restaurant_details: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[restaurantDetailsRow])
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect restaurant rows: %w", err)
	}

	restaurants := make([]locitypes.RestaurantDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		var address, website, phoneNumber, openingHours, priceLevel, cuisineType *string
		if row.Address != "" {
			address = &row.Address
		}
		if row.Website != "" {
			website = &row.Website
		}
		if row.PhoneNumber != "" {
			phoneNumber = &row.PhoneNumber
		}
		if row.OpeningHours != "" {
			openingHours = &row.OpeningHours
		}
		if row.PriceLevel != "" {
			priceLevel = &row.PriceLevel
		}
		if row.CuisineType != "" {
			cuisineType = &row.CuisineType
		}
		restaurants[i] = locitypes.RestaurantDetailedInfo{
			ID:           row.ID,
			Name:         row.Name,
			Description:  row.Description,
			Latitude:     row.Latitude,
			Longitude:    row.Longitude,
			Address:      address,
			Website:      website,
			PhoneNumber:  phoneNumber,
			OpeningHours: openingHours,
			PriceLevel:   priceLevel,
			Category:     row.Category,
			Tags:         row.Tags,
			Images:       row.Images,
			Rating:       row.Rating,
			CuisineType:  cuisineType,
			LlmInteractionID: func() uuid.UUID {
				if row.LlmInteractionID == nil {
					return uuid.Nil
				}
				return *row.LlmInteractionID
			}(),
		}
	}
	span.SetStatus(codes.Ok, "Restaurants found")
	return restaurants, nil
}

func (r *RepositoryImpl) SaveRestaurantDetails(ctx context.Context, restaurant locitypes.RestaurantDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	ctx, span := otel.Tracer("RestaurantRepository").Start(ctx, "SaveRestaurantDetails", trace.WithAttributes(
		attribute.String("restaurant.name", restaurant.Name),
		attribute.String("city.id", cityID.String()),
	))
	defer span.End()

	// Normalize opening_hours
	var openingHoursJSON *string
	if restaurant.OpeningHours != nil && *restaurant.OpeningHours != "" {
		if json.Valid([]byte(*restaurant.OpeningHours)) {
			openingHoursJSON = restaurant.OpeningHours
		} else if marshalled, err := json.Marshal(map[string]string{"general": *restaurant.OpeningHours}); err == nil {
			marshalledStr := string(marshalled)
			openingHoursJSON = &marshalledStr
		} else {
			r.logger.WarnContext(ctx, "Invalid JSON for opening_hours, setting to NULL",
				slog.String("value", *restaurant.OpeningHours),
				slog.String("restaurant_name", restaurant.Name),
				slog.Any("marshal_error", err))
		}
	}

	query := `
        INSERT INTO restaurant_details (
            id, city_id, name, description, latitude, longitude, location,
            address, website, phone_number, opening_hours, price_level, category,
            cuisine_type, tags, images, rating, llm_interaction_id
        ) VALUES (
            $1, $2, $3, $4, $5, $6, ST_SetSRID(ST_MakePoint($7, $8), 4326),
            $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19 -- Added $19
        ) RETURNING id
    `
	var id uuid.UUID
	if err := r.pgpool.QueryRow(ctx, query,
		restaurant.ID,
		cityID,                      // $2: city_id
		restaurant.Name,             // $3: name
		restaurant.Description,      // $4: description
		restaurant.Latitude,         // $5: latitude
		restaurant.Longitude,        // $6: longitude
		restaurant.Longitude,        // $7: location (longitude for ST_MakePoint)
		restaurant.Latitude,         // $8: location (latitude for ST_MakePoint)
		restaurant.Address,          // $9: address
		restaurant.Website,          // $10: website
		restaurant.PhoneNumber,      // $11: phone_number
		openingHoursJSON,            // $12: opening_hours JSON or nil
		restaurant.PriceLevel,       // $13: price_level
		restaurant.Category,         // $14: category
		restaurant.CuisineType,      // $15: cuisine_type
		restaurant.Tags,             // $16: tags (TEXT[])
		restaurant.Images,           // $17: images (TEXT[])
		restaurant.Rating,           // $18: rating (DOUBLE PRECISION)
		restaurant.LlmInteractionID, // $19: llm_interaction_id (UUID)
	).Scan(&id); err != nil {
		r.logger.ErrorContext(ctx, "Failed to save restaurant details",
			slog.Any("error", err),
			slog.String("restaurant_name", restaurant.Name),
			slog.String("city_id", cityID.String()))
		span.RecordError(err)
		span.SetStatus(codes.Error, "DB INSERT failed")
		return uuid.Nil, fmt.Errorf("failed to save restaurant_details: %w", err)
	}

	span.SetAttributes(attribute.String("db.restaurant.id", id.String())) // Log the ID returned by the DB
	span.SetStatus(codes.Ok, "Restaurant saved")
	return id, nil
}

func (r *RepositoryImpl) GetRestaurantByID(ctx context.Context, restaurantID uuid.UUID) (*locitypes.RestaurantDetailedInfo, error) {
	ctx, span := otel.Tracer("RestaurantRepository").Start(ctx, "GetRestaurantByID")
	defer span.End()

	query := `
        SELECT
            id, name, description, latitude, longitude, address, website, phone_number,
            opening_hours, price_level, category, tags, images, rating, cuisine_type, llm_interaction_id
        FROM restaurant_details
        WHERE id = $1
    `
	rows, err := r.pgpool.Query(ctx, query, restaurantID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get restaurant: %w", err)
	}

	dbRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[restaurantDetailsRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			span.SetStatus(codes.Ok, "Restaurant not found")
			return nil, nil
		}
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect restaurant: %w", err)
	}

	restaurant := locitypes.RestaurantDetailedInfo{
		ID:           dbRow.ID,
		Name:         dbRow.Name,
		Description:  dbRow.Description,
		Latitude:     dbRow.Latitude,
		Longitude:    dbRow.Longitude,
		Address:      stringPtr(dbRow.Address),
		Website:      stringPtr(dbRow.Website),
		PhoneNumber:  stringPtr(dbRow.PhoneNumber),
		OpeningHours: stringPtr(dbRow.OpeningHours),
		PriceLevel:   stringPtr(dbRow.PriceLevel),
		Category:     dbRow.Category,
		Tags:         dbRow.Tags,
		Images:       dbRow.Images,
		Rating:       dbRow.Rating,
		CuisineType:  stringPtr(dbRow.CuisineType),
		LlmInteractionID: func() uuid.UUID {
			if dbRow.LlmInteractionID == nil {
				return uuid.Nil
			}
			return *dbRow.LlmInteractionID
		}(),
	}
	span.SetStatus(codes.Ok, "Restaurant found")
	return &restaurant, nil
}

// searchPOIsRow is a DB row struct for SearchPOIs query
type searchPOIsRow struct {
	ID             uuid.UUID `db:"id"`
	Name           string    `db:"name"`
	Description    string    `db:"description"`
	Longitude      float64   `db:"longitude"`
	Latitude       float64   `db:"latitude"`
	Category       string    `db:"category"`
	DistanceMeters float64   `db:"distance_meters"`
}

func (r *RepositoryImpl) SearchPOIs(ctx context.Context, filter locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("Repository").Start(ctx, "SearchPOIs", trace.WithAttributes(
		attribute.Float64("location.latitude", filter.Location.Latitude),
		attribute.Float64("location.longitude", filter.Location.Longitude),
		attribute.Float64("radius", filter.Radius),
		attribute.String("category", filter.Category),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "SearchPOIs"))

	// Base query using PostGIS for geospatial filtering with COALESCE for nullable fields
	query := `
        SELECT
            id,
            name,
            COALESCE(description, '') AS description,
            ST_X(location::geometry) AS longitude,
            ST_Y(location::geometry) AS latitude,
            COALESCE(category, '') AS category,
            ST_Distance(
                location,
                ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography
            ) AS distance_meters
        FROM points_of_interest
        WHERE ST_DWithin(
            location,
            ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
            $3
        )
    `
	args := []any{
		filter.Location.Longitude, // $1
		filter.Location.Latitude,  // $2
		filter.Radius * 1000,      // $3 (convert km to meters for ST_DWithin)
	}

	// Add category filter if provided
	if filter.Category != "" {
		query += ` AND category = $4`
		args = append(args, filter.Category) // $4
	}

	// Order by distance
	query += ` ORDER BY distance_meters ASC`

	l.DebugContext(ctx, "Executing POI search query", slog.String("query", query), slog.Any("args", args))

	// Execute query
	rows, err := r.pgpool.Query(ctx, query, args...)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query POIs", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to search points_of_interest: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[searchPOIsRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect POI rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			DescriptionPOI: row.Description,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
			Distance:       row.DistanceMeters / 1000, // Convert meters to km
		}
	}

	// Log and set span status
	if len(pois) == 0 {
		l.InfoContext(ctx, "No POIs found")
		span.SetStatus(codes.Ok, "No POIs found")
	} else {
		l.InfoContext(ctx, "POIs found", slog.Int("count", len(pois)))
		span.SetStatus(codes.Ok, "POIs found")
	}

	return pois, nil
}

func (r *RepositoryImpl) GetItinerary(ctx context.Context, userID, itineraryID uuid.UUID) (*locitypes.UserSavedItinerary, error) {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "GetItinerary", trace.WithAttributes(
		semconv.DBSystemPostgreSQL,
		attribute.String("db.operation", "SELECT"),
		attribute.String("db.sql.table", "user_saved_itineraries"),
		attribute.String("user.id", userID.String()),
		attribute.String("itinerary.id", itineraryID.String()),
	))
	defer span.End()

	query := `
		SELECT
			id, user_id, source_llm_interaction_id, session_id, primary_city_id, title, description,
			markdown_content, tags, estimated_duration_days, estimated_cost_level, is_public
		FROM user_saved_itineraries
		WHERE id = $1 AND user_id = $2
	`
	rows, err := r.pgpool.Query(ctx, query, itineraryID, userID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to query user_saved_itineraries: %w", err)
	}

	type itineraryRow struct {
		ID                     uuid.UUID  `db:"id"`
		UserID                 uuid.UUID  `db:"user_id"`
		SourceLlmInteractionID *uuid.UUID `db:"source_llm_interaction_id"`
		SessionID              *uuid.UUID `db:"session_id"`
		PrimaryCityID          *uuid.UUID `db:"primary_city_id"`
		Title                  string     `db:"title"`
		Description            *string    `db:"description"`
		MarkdownContent        string     `db:"markdown_content"`
		Tags                   []string   `db:"tags"`
		EstimatedDurationDays  *int32     `db:"estimated_duration_days"`
		EstimatedCostLevel     *int32     `db:"estimated_cost_level"`
		IsPublic               bool       `db:"is_public"`
	}

	dbRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[itineraryRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = fmt.Errorf("no itinerary found with ID %s for user %s", itineraryID, userID)
			span.RecordError(err)
			return nil, err
		}
		span.RecordError(err)
		return nil, fmt.Errorf("failed to read itinerary: %w", err)
	}

	itinerary := locitypes.UserSavedItinerary{
		ID:                     dbRow.ID,
		UserID:                 dbRow.UserID,
		Title:                  dbRow.Title,
		MarkdownContent:        dbRow.MarkdownContent,
		Tags:                   dbRow.Tags,
		IsPublic:               dbRow.IsPublic,
		SourceLlmInteractionID: dbRow.SourceLlmInteractionID,
		SessionID:              dbRow.SessionID,
		PrimaryCityID:          dbRow.PrimaryCityID,
		Description:            dbRow.Description,
		EstimatedDurationDays:  dbRow.EstimatedDurationDays,
		EstimatedCostLevel:     dbRow.EstimatedCostLevel,
	}

	return &itinerary, nil
}

func (r *RepositoryImpl) GetItineraries(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]locitypes.UserSavedItinerary, int, error) {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "GetItineraries", trace.WithAttributes(
		semconv.DBSystemPostgreSQL,
		attribute.String("db.operation", "SELECT"),
		attribute.String("db.sql.table", "user_saved_itineraries"),
		attribute.String("user.id", userID.String()),
		attribute.Int("page", page),
		attribute.Int("page_size", pageSize),
	))
	defer span.End()

	offset := (page - 1) * pageSize
	query := `
		SELECT
			id, user_id, source_llm_interaction_id, session_id, primary_city_id, title, description,
			markdown_content, tags, estimated_duration_days, estimated_cost_level, is_public
		FROM user_saved_itineraries
		WHERE user_id = $1
		LIMIT $2 OFFSET $3
	`
	rows, err := r.pgpool.Query(ctx, query, userID, pageSize, offset)
	if err != nil {
		span.RecordError(err)
		return nil, 0, fmt.Errorf("failed to query user_saved_itineraries: %w", err)
	}

	itineraries, err := pgx.CollectRows(rows, pgx.RowToStructByName[locitypes.UserSavedItinerary])
	if err != nil {
		span.RecordError(err)
		return nil, 0, fmt.Errorf("failed to collect user_saved_itineraries rows: %w", err)
	}

	countQuery := `
		SELECT COUNT(*) FROM user_saved_itineraries WHERE user_id = $1
	`
	var totalCount int
	if err := r.pgpool.QueryRow(ctx, countQuery, userID).Scan(&totalCount); err != nil {
		span.RecordError(err)
		return nil, 0, fmt.Errorf("failed to count user_saved_itineraries: %w", err)
	}
	span.SetAttributes(
		attribute.Int("total_records", totalCount),
		attribute.Int("itineraries.count", len(itineraries)),
	)
	span.SetStatus(codes.Ok, "Itineraries retrieved successfully")
	return itineraries, totalCount, nil
}

func (r *RepositoryImpl) UpdateItinerary(ctx context.Context, userID, itineraryID uuid.UUID, updates locitypes.UpdateItineraryRequest) (*locitypes.UserSavedItinerary, error) {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "UpdateItinerary", trace.WithAttributes(
		semconv.DBSystemPostgreSQL,
		attribute.String("db.operation", "UPDATE"),
		attribute.String("db.sql.table", "user_saved_itineraries"),
		attribute.String("user.id", userID.String()),
		attribute.String("itinerary.id", itineraryID.String()),
	))
	defer span.End()

	setClauses := []string{}
	args := []any{}
	argCount := 1 // Start arg counter for $1, $2, ...

	if updates.Title != nil {
		setClauses = append(setClauses, fmt.Sprintf("title = $%d", argCount))
		args = append(args, *updates.Title)
		argCount++
		span.SetAttributes(attribute.Bool("update.title", true))
	}
	if updates.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argCount))
		var desc any
		if *updates.Description != "" {
			desc = *updates.Description
		}
		args = append(args, desc)
		argCount++
		span.SetAttributes(attribute.Bool("update.description", true))
	}
	if updates.Tags != nil {
		setClauses = append(setClauses, fmt.Sprintf("tags = $%d", argCount))
		args = append(args, updates.Tags)
		argCount++
		span.SetAttributes(attribute.Bool("update.tags", true))
	}
	if updates.EstimatedDurationDays != nil {
		setClauses = append(setClauses, fmt.Sprintf("estimated_duration_days = $%d", argCount))
		args = append(args, *updates.EstimatedDurationDays)
		argCount++
		span.SetAttributes(attribute.Bool("update.duration", true))
	}
	if updates.EstimatedCostLevel != nil {
		setClauses = append(setClauses, fmt.Sprintf("estimated_cost_level = $%d", argCount))
		args = append(args, *updates.EstimatedCostLevel)
		argCount++
		span.SetAttributes(attribute.Bool("update.cost", true))
	}
	if updates.IsPublic != nil {
		setClauses = append(setClauses, fmt.Sprintf("is_public = $%d", argCount))
		args = append(args, *updates.IsPublic)
		argCount++
		span.SetAttributes(attribute.Bool("update.is_public", true))
	}
	if updates.MarkdownContent != nil {
		setClauses = append(setClauses, fmt.Sprintf("markdown_content = $%d", argCount))
		args = append(args, *updates.MarkdownContent)
		argCount++
		span.SetAttributes(attribute.Bool("update.markdown", true))
	}

	if len(setClauses) == 0 {
		span.AddEvent("No fields provided for update.")
		return nil, fmt.Errorf("no fields to update for itinerary %s", itineraryID)
	}

	// Always update the updated_at timestamp
	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())
	argCount++

	// Store the current argCount for the WHERE clause
	whereIDPlaceholder := argCount
	args = append(args, itineraryID)
	argCount++
	userIDPlaceholder := argCount
	args = append(args, userID)

	query := fmt.Sprintf(`
        UPDATE user_saved_itineraries
        SET %s
        WHERE id = $%d AND user_id = $%d
        RETURNING id, user_id, source_llm_interaction_id, primary_city_id, title, description,
                  markdown_content, tags, estimated_duration_days, estimated_cost_level, is_public,
                  created_at, updated_at
    `, strings.Join(setClauses, ", "), whereIDPlaceholder, userIDPlaceholder)

	r.logger.DebugContext(ctx, "Executing UpdateItinerary query", slog.String("query", query), slog.Any("args_count", len(args)))

	updateRows, err := r.pgpool.Query(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "DB UPDATE failed")
		r.logger.ErrorContext(ctx, "Failed to update itinerary", slog.Any("error", err))
		return nil, fmt.Errorf("failed to update user_saved_itineraries: %w", err)
	}

	type itineraryRow struct {
		ID                     uuid.UUID  `db:"id"`
		UserID                 uuid.UUID  `db:"user_id"`
		SourceLlmInteractionID *uuid.UUID `db:"source_llm_interaction_id"`
		PrimaryCityID          *uuid.UUID `db:"primary_city_id"`
		Title                  string     `db:"title"`
		Description            *string    `db:"description"`
		MarkdownContent        string     `db:"markdown_content"`
		Tags                   []string   `db:"tags"`
		EstimatedDurationDays  *int32     `db:"estimated_duration_days"`
		EstimatedCostLevel     *int32     `db:"estimated_cost_level"`
		IsPublic               bool       `db:"is_public"`
		CreatedAt              time.Time  `db:"created_at"`
		UpdatedAt              time.Time  `db:"updated_at"`
	}

	dbRow, err := pgx.CollectOneRow(updateRows, pgx.RowToStructByName[itineraryRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			notFoundErr := fmt.Errorf("itinerary with ID %s not found for user %s or does not exist", itineraryID, userID)
			span.RecordError(notFoundErr)
			span.SetStatus(codes.Error, "Itinerary not found or not owned by user")
			return nil, notFoundErr
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "DB UPDATE failed")
		r.logger.ErrorContext(ctx, "Failed to read updated itinerary", slog.Any("error", err))
		return nil, fmt.Errorf("failed to read updated itinerary: %w", err)
	}

	updatedItinerary := locitypes.UserSavedItinerary{
		ID:                     dbRow.ID,
		UserID:                 dbRow.UserID,
		Title:                  dbRow.Title,
		MarkdownContent:        dbRow.MarkdownContent,
		Tags:                   dbRow.Tags,
		IsPublic:               dbRow.IsPublic,
		CreatedAt:              dbRow.CreatedAt,
		UpdatedAt:              dbRow.UpdatedAt,
		SourceLlmInteractionID: dbRow.SourceLlmInteractionID,
		PrimaryCityID:          dbRow.PrimaryCityID,
		Description:            dbRow.Description,
		EstimatedDurationDays:  dbRow.EstimatedDurationDays,
		EstimatedCostLevel:     dbRow.EstimatedCostLevel,
	}

	span.SetStatus(codes.Ok, "Itinerary updated successfully")
	return &updatedItinerary, nil
}

func (r *RepositoryImpl) SaveItinerary(ctx context.Context, userID, cityID uuid.UUID) (uuid.UUID, error) {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "SaveItinerary")
	defer span.End()

	query := `
        INSERT INTO itineraries (user_id, city_id, created_at, updated_at)
        VALUES ($1, $2, NOW(), NOW())
        RETURNING id
    `
	var itineraryID uuid.UUID
	if err := r.pgpool.QueryRow(ctx, query, userID, cityID).Scan(&itineraryID); err != nil {
		span.RecordError(err)
		return uuid.Nil, fmt.Errorf("failed to save itinerary: %w", err)
	}
	span.SetAttributes(attribute.String("itinerary.id", itineraryID.String()))
	return itineraryID, nil
}

func (r *RepositoryImpl) SavePOItoPointsOfInterest(ctx context.Context, poi locitypes.POIDetailedInfo, cityID uuid.UUID) (uuid.UUID, error) {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "SavePOItoPointsOfInterest")
	defer span.End()

	// Check if POI exists
	queryCheck := `
        SELECT id FROM points_of_interest
        WHERE name = $1 AND city_id = $2
    `
	var existingID uuid.UUID
	err := r.pgpool.QueryRow(ctx, queryCheck, poi.Name, cityID).Scan(&existingID)
	if err == nil {
		return existingID, nil // POI already exists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		span.RecordError(err)
		return uuid.Nil, fmt.Errorf("failed to check POI existence: %w", err)
	}

	// Insert new POI
	queryInsert := `
        INSERT INTO points_of_interest (id, city_id, name, latitude, longitude, category)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id
    `
	poiID := uuid.New()
	if err := r.pgpool.QueryRow(ctx, queryInsert, poiID, cityID, poi.Name, poi.Latitude, poi.Longitude, poi.Category).Scan(&poiID); err != nil {
		span.RecordError(err)
		return uuid.Nil, fmt.Errorf("failed to save POI to points_of_interest: %w", err)
	}
	span.SetAttributes(attribute.String("poi.id", poiID.String()))
	return poiID, nil
}

type ItineraryPOISource struct {
	pois        []locitypes.POIDetailedInfo
	itineraryID uuid.UUID
	idx         int
}

func (ips *ItineraryPOISource) Next() bool {
	ips.idx++
	return ips.idx < len(ips.pois)
}

func (ips *ItineraryPOISource) Values() ([]any, error) {
	poi := ips.pois[ips.idx]
	return []any{ips.itineraryID, poi.ID, ips.idx, poi.DescriptionPOI}, nil
}

func stringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func (ips *ItineraryPOISource) Err() error {
	return nil
}

func (r *RepositoryImpl) SaveItineraryPOIs(ctx context.Context, itineraryID uuid.UUID, pois []locitypes.POIDetailedInfo) error {
	ctx, span := otel.Tracer("LlmInteractionRepo").Start(ctx, "SaveItineraryPOIs")
	defer span.End()

	for i := range pois {
		poiID, err := r.SavePOItoPointsOfInterest(ctx, pois[i], pois[i].CityID) // Assume CityID is added to POIDetailedInfo or passed separately
		if err != nil {
			span.RecordError(err)
			return fmt.Errorf("failed to ensure POI in points_of_interest: %w", err)
		}
		pois[i].ID = poiID
	}

	source := &ItineraryPOISource{
		pois:        pois,
		itineraryID: itineraryID,
		idx:         -1,
	}

	_, err := r.pgpool.CopyFrom(
		ctx,
		pgx.Identifier{"itinerary_pois"},
		[]string{"itinerary_id", "poi_id", "order_index", "ai_description"},
		source,
	)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to save itinerary POIs: %w", err)
	}

	span.SetAttributes(attribute.Int("pois.count", len(pois)))
	return nil
}

func (r *RepositoryImpl) CityExists(ctx context.Context, cityID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM cities WHERE id = $1)`
	var exists bool
	if err := r.pgpool.QueryRow(ctx, query, cityID).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check city existence: %w", err)
	}
	return exists, nil
}

// similarPOIRow is a DB row struct for FindSimilarPOIs query
type similarPOIRow struct {
	ID              uuid.UUID `db:"id"`
	Name            string    `db:"name"`
	Description     string    `db:"description"`
	Longitude       float64   `db:"longitude"`
	Latitude        float64   `db:"latitude"`
	Category        string    `db:"category"`
	SimilarityScore float64   `db:"similarity_score"`
}

// FindSimilarPOIs finds POIs similar to the provided query embedding using cosine similarity
func (r *RepositoryImpl) FindSimilarPOIs(ctx context.Context, queryEmbedding []float32, limit int) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("Repository").Start(ctx, "FindSimilarPOIs", trace.WithAttributes(
		attribute.Int("embedding.dimension", len(queryEmbedding)),
		attribute.Int("limit", limit),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "FindSimilarPOIs"))

	// Convert []float32 to pgvector format string
	embeddingStr := fmt.Sprintf("[%v]", strings.Join(func() []string {
		strs := make([]string, len(queryEmbedding))
		for i, v := range queryEmbedding {
			strs[i] = fmt.Sprintf("%f", v)
		}
		return strs
	}(), ","))

	query := `
        SELECT
            id,
            name,
            COALESCE(description, '') AS description,
            ST_X(location::geometry) AS longitude,
            ST_Y(location::geometry) AS latitude,
            COALESCE(poi_type, '') AS category,
            1 - (embedding <=> $1::vector) AS similarity_score
        FROM points_of_interest
        WHERE embedding IS NOT NULL
        ORDER BY embedding <=> $1::vector
        LIMIT $2
    `

	l.DebugContext(ctx, "Executing similarity search query",
		slog.String("query", query),
		slog.Int("embedding_dim", len(queryEmbedding)),
		slog.Int("limit", limit))

	rows, err := r.pgpool.Query(ctx, query, embeddingStr, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query similar POIs", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to search similar POIs: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[similarPOIRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect similar POI rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect similar POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			DescriptionPOI: row.Description,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
			Distance:       row.SimilarityScore, // Store similarity score in distance field
		}
	}

	l.InfoContext(ctx, "Similar POIs found", slog.Int("count", len(pois)))
	span.SetAttributes(attribute.Int("results.count", len(pois)))
	span.SetStatus(codes.Ok, "Similar POIs found")

	return pois, nil
}

// FindSimilarPOIsByCity finds POIs similar to the provided query embedding within a specific city
func (r *RepositoryImpl) FindSimilarPOIsByCity(ctx context.Context, queryEmbedding []float32, cityID uuid.UUID, limit int) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("Repository").Start(ctx, "FindSimilarPOIsByCity", trace.WithAttributes(
		attribute.String("city.id", cityID.String()),
		attribute.Int("embedding.dimension", len(queryEmbedding)),
		attribute.Int("limit", limit),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "FindSimilarPOIsByCity"))

	// Convert []float32 to pgvector format string
	embeddingStr := fmt.Sprintf("[%v]", strings.Join(func() []string {
		strs := make([]string, len(queryEmbedding))
		for i, v := range queryEmbedding {
			strs[i] = fmt.Sprintf("%f", v)
		}
		return strs
	}(), ","))

	query := `
        SELECT
            id,
            name,
            COALESCE(description, '') AS description,
            ST_X(location::geometry) AS longitude,
            ST_Y(location::geometry) AS latitude,
            COALESCE(poi_type, '') AS category,
            1 - (embedding <=> $1::vector) AS similarity_score
        FROM points_of_interest
        WHERE embedding IS NOT NULL AND city_id = $2
        ORDER BY embedding <=> $1::vector
        LIMIT $3
    `

	l.DebugContext(ctx, "Executing city-specific similarity search",
		slog.String("city_id", cityID.String()),
		slog.Int("embedding_dim", len(queryEmbedding)),
		slog.Int("limit", limit))

	rows, err := r.pgpool.Query(ctx, query, embeddingStr, cityID, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query similar POIs by city", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to search similar POIs by city: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[similarPOIRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect similar POI rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect similar POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			DescriptionPOI: row.Description,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
			Distance:       row.SimilarityScore,
			CityID:         cityID,
		}
	}

	l.InfoContext(ctx, "Similar POIs by city found",
		slog.String("city_id", cityID.String()),
		slog.Int("count", len(pois)))
	span.SetAttributes(
		attribute.String("city.id", cityID.String()),
		attribute.Int("results.count", len(pois)),
	)
	span.SetStatus(codes.Ok, "Similar POIs by city found")

	return pois, nil
}

// hybridSearchRow is a DB row struct for SearchPOIsHybrid query
type hybridSearchRow struct {
	ID              uuid.UUID `db:"id"`
	Name            string    `db:"name"`
	Description     string    `db:"description"`
	Longitude       float64   `db:"longitude"`
	Latitude        float64   `db:"latitude"`
	Category        string    `db:"category"`
	DistanceMeters  float64   `db:"distance_meters"`
	SimilarityScore float64   `db:"similarity_score"`
	HybridScore     float64   `db:"hybrid_score"`
}

// SearchPOIsHybrid combines spatial filtering with semantic similarity search
func (r *RepositoryImpl) SearchPOIsHybrid(ctx context.Context, filter locitypes.POIFilter, queryEmbedding []float32, semanticWeight float64) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("Repository").Start(ctx, "SearchPOIsHybrid", trace.WithAttributes(
		attribute.Float64("location.latitude", filter.Location.Latitude),
		attribute.Float64("location.longitude", filter.Location.Longitude),
		attribute.Float64("radius", filter.Radius),
		attribute.String("category", filter.Category),
		attribute.Float64("semantic.weight", semanticWeight),
		attribute.Int("embedding.dimension", len(queryEmbedding)),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "SearchPOIsHybrid"))

	// Convert []float32 to pgvector format string
	embeddingStr := fmt.Sprintf("[%v]", strings.Join(func() []string {
		strs := make([]string, len(queryEmbedding))
		for i, v := range queryEmbedding {
			strs[i] = fmt.Sprintf("%f", v)
		}
		return strs
	}(), ","))

	// Hybrid search combining spatial distance and semantic similarity
	query := `
        SELECT
            id,
            name,
            COALESCE(description, '') AS description,
            ST_X(location::geometry) AS longitude,
            ST_Y(location::geometry) AS latitude,
            COALESCE(poi_type, '') AS category,
            ST_Distance(
                location,
                ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography
            ) AS distance_meters,
            CASE
                WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $6::vector)
                ELSE 0
            END AS similarity_score,
            -- Hybrid score: weighted combination of spatial proximity and semantic similarity
            CASE
                WHEN embedding IS NOT NULL THEN
                    (1 - $5) * (1 / (1 + ST_Distance(location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) / 1000)) +
                    $5 * (1 - (embedding <=> $6::vector))
                ELSE
                    (1 - $5) * (1 / (1 + ST_Distance(location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) / 1000))
            END AS hybrid_score
        FROM points_of_interest
        WHERE ST_DWithin(
            location,
            ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
            $3
        )
    `

	args := []any{
		filter.Location.Longitude, // $1
		filter.Location.Latitude,  // $2
		filter.Radius * 1000,      // $3 (convert km to meters)
	}

	// Add category filter if provided
	argIndex := 4
	if filter.Category != "" {
		query += fmt.Sprintf(` AND poi_type = $%d`, argIndex)
		args = append(args, filter.Category)
		_ = argIndex + 1 // argIndex incremented but not used after this point
	}

	// Add semantic weight and embedding (adjust indexes based on whether category was added)
	args = append(args, semanticWeight) // semantic weight
	args = append(args, embeddingStr)   // embedding

	// Order by hybrid score (descending)
	query += ` ORDER BY hybrid_score DESC`

	l.DebugContext(ctx, "Executing hybrid search query",
		slog.String("query", query),
		slog.Any("args_count", len(args)),
		slog.Float64("semantic_weight", semanticWeight))

	rows, err := r.pgpool.Query(ctx, query, args...)
	if err != nil {
		l.ErrorContext(ctx, "Failed to execute hybrid search", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to execute hybrid POI search: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[hybridSearchRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect hybrid search rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect hybrid search rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			DescriptionPOI: row.Description,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
			Distance:       row.DistanceMeters / 1000, // Convert meters to km
		}
	}

	l.InfoContext(ctx, "Hybrid search POIs found",
		slog.Int("count", len(pois)),
		slog.Float64("semantic_weight", semanticWeight))
	span.SetAttributes(
		attribute.Int("results.count", len(pois)),
		attribute.Float64("semantic.weight", semanticWeight),
	)
	span.SetStatus(codes.Ok, "Hybrid search completed")

	return pois, nil
}

// UpdatePOIEmbedding updates the embedding vector for a specific POI
func (r *RepositoryImpl) UpdatePOIEmbedding(ctx context.Context, poiID uuid.UUID, embedding []float32) error {
	ctx, span := otel.Tracer("Repository").Start(ctx, "UpdatePOIEmbedding", trace.WithAttributes(
		attribute.String("poi.id", poiID.String()),
		attribute.Int("embedding.dimension", len(embedding)),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "UpdatePOIEmbedding"))

	// Convert []float32 to pgvector format string
	embeddingStr := fmt.Sprintf("[%v]", strings.Join(func() []string {
		strs := make([]string, len(embedding))
		for i, v := range embedding {
			strs[i] = fmt.Sprintf("%f", v)
		}
		return strs
	}(), ","))

	query := `
        UPDATE points_of_interest
        SET embedding = $1::vector, embedding_generated_at = NOW()
        WHERE id = $2
    `

	result, err := r.pgpool.Exec(ctx, query, embeddingStr, poiID)
	if err != nil {
		l.ErrorContext(ctx, "Failed to update POI embedding",
			slog.Any("error", err),
			slog.String("poi_id", poiID.String()))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database update failed")
		return fmt.Errorf("failed to update POI embedding: %w", err)
	}

	if result.RowsAffected() == 0 {
		err := fmt.Errorf("no POI found with ID %s", poiID.String())
		l.WarnContext(ctx, "No POI found for embedding update", slog.String("poi_id", poiID.String()))
		span.RecordError(err)
		span.SetStatus(codes.Error, "POI not found")
		return err
	}

	l.InfoContext(ctx, "POI embedding updated successfully",
		slog.String("poi_id", poiID.String()),
		slog.Int("embedding_dimension", len(embedding)))
	span.SetAttributes(
		attribute.String("poi.id", poiID.String()),
		attribute.Int("embedding.dimension", len(embedding)),
	)
	span.SetStatus(codes.Ok, "POI embedding updated")

	return nil
}

// poiWithoutEmbeddingRow is a DB row struct for GetPOIsWithoutEmbeddings query
type poiWithoutEmbeddingRow struct {
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	Longitude   float64   `db:"longitude"`
	Latitude    float64   `db:"latitude"`
	Category    string    `db:"category"`
	CityID      uuid.UUID `db:"city_id"`
}

// GetPOIsWithoutEmbeddings retrieves POIs that don't have embeddings generated yet
func (r *RepositoryImpl) GetPOIsWithoutEmbeddings(ctx context.Context, limit int) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("Repository").Start(ctx, "GetPOIsWithoutEmbeddings", trace.WithAttributes(
		attribute.Int("limit", limit),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetPOIsWithoutEmbeddings"))

	query := `
        SELECT
            id,
            name,
            COALESCE(description, '') AS description,
            ST_X(location::geometry) AS longitude,
            ST_Y(location::geometry) AS latitude,
            COALESCE(poi_type, '') AS category,
            city_id
        FROM points_of_interest
        WHERE embedding IS NULL
        ORDER BY created_at ASC
        LIMIT $1
    `

	rows, err := r.pgpool.Query(ctx, query, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query POIs without embeddings", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query POIs without embeddings: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[poiWithoutEmbeddingRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect POI rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, len(dbRows))
	for i, row := range dbRows {
		pois[i] = locitypes.POIDetailedInfo{
			ID:             row.ID,
			Name:           row.Name,
			DescriptionPOI: row.Description,
			Longitude:      row.Longitude,
			Latitude:       row.Latitude,
			Category:       row.Category,
			CityID:         row.CityID,
		}
	}

	l.InfoContext(ctx, "POIs without embeddings found", slog.Int("count", len(pois)))
	span.SetAttributes(attribute.Int("results.count", len(pois)))
	span.SetStatus(codes.Ok, "POIs without embeddings retrieved")

	return pois, nil
}

// GetPOIsByLocationAndDistance retrieves POIs within a specified radius from a given location using PostGIS
func (r *RepositoryImpl) GetPOIsByLocationAndDistance(ctx context.Context, lat, lon, radiusMeters float64) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIRepository").Start(ctx, "GetPOIsByLocationAndDistance", trace.WithAttributes(
		attribute.Float64("location.lat", lat),
		attribute.Float64("location.lon", lon),
		attribute.Float64("radius.meters", radiusMeters),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetPOIsByLocationAndDistance"))

	// Build the query with optional category filter
	baseQuery := `
					SELECT
						id,
						name,
						description,
						longitude,
						latitude,
						category,
						address,
						website,
						phone_number,
						opening_hours,
						poi_type,
						price_level,
						rating,
						ROUND(CAST(distance_meters / 1000.0 AS numeric), 2) as distance,
						city_id,
						COALESCE(tags, '{}') as tags,
						COALESCE(rating_count, 0) as rating_count,
						COALESCE(is_sponsored, false) as is_sponsored
					FROM (
						SELECT
							id,
							name,
							COALESCE(description, '') as description,
							ST_X(location) as longitude,
							ST_Y(location) as latitude,
							COALESCE(category, '') as category,
							COALESCE(address, '') as address,
							COALESCE(website, '') as website,
							COALESCE(phone_number, '') as phone_number,
							opening_hours,
							COALESCE(poi_type, '') as poi_type,
							price_level,
							COALESCE(average_rating, 0) as rating,
							ST_Distance(location::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) as distance_meters,
							city_id,
							tags,
							rating_count,
							is_sponsored
						FROM points_of_interest
						WHERE ST_DWithin(
							location::geography,
							ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
							$3
						)
					) sub
					ORDER BY distance ASC LIMIT 50
				`

	var args []any
	args = append(args, lon, lat, radiusMeters) // $1, $2, $3

	l.DebugContext(ctx, "Executing POI distance query",
		slog.String("query", baseQuery),
		slog.Float64("lat", lat),
		slog.Float64("lon", lon),
		slog.Float64("radius_meters", radiusMeters))

	rows, err := r.pgpool.Query(ctx, baseQuery, args...)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query POIs by location and distance", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query POIs by location and distance: %w", err)
	}
	defer rows.Close()

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[struct {
		ID           uuid.UUID `db:"id"`
		Name         string    `db:"name"`
		Description  *string   `db:"description"`
		Longitude    float64   `db:"longitude"`
		Latitude     float64   `db:"latitude"`
		Category     string    `db:"category"`
		Address      *string   `db:"address"`
		Website      *string   `db:"website"`
		PhoneNumber  *string   `db:"phone_number"`
		OpeningHours *string   `db:"opening_hours"`
		PoiType      *string   `db:"poi_type"`
		PriceLevel   *int32    `db:"price_level"`
		Rating       *float64  `db:"rating"`
		Distance     float64   `db:"distance"`
		CityID       *string   `db:"city_id"`
		Tags         []string  `db:"tags"`
		RatingCount  *int32    `db:"rating_count"`
		IsSponsored  *bool     `db:"is_sponsored"`
	}])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect POI rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, 0, len(dbRows))
	for _, row := range dbRows {
		poi := locitypes.POIDetailedInfo{
			ID:        row.ID,
			Name:      row.Name,
			Longitude: row.Longitude,
			Latitude:  row.Latitude,
			Category:  row.Category,
			Distance:  row.Distance,
			Tags:      row.Tags,
		}
		if row.Description != nil {
			poi.Description = *row.Description
		}
		if row.Address != nil {
			poi.Address = *row.Address
		}
		if row.Website != nil {
			poi.Website = *row.Website
		}
		if row.PhoneNumber != nil {
			poi.PhoneNumber = *row.PhoneNumber
		}
		if row.OpeningHours != nil {
			poi.OpeningHours = map[string]string{"general": *row.OpeningHours}
		}
		if row.PoiType != nil && *row.PoiType != "" {
			poi.Category = *row.PoiType
		}
		if row.PriceLevel != nil {
			switch *row.PriceLevel {
			case 1:
				poi.PriceLevel = "€"
			case 2:
				poi.PriceLevel = "€€"
			case 3:
				poi.PriceLevel = "€€€"
			case 4:
				poi.PriceLevel = "€€€€"
			default:
				poi.PriceLevel = "Free"
			}
		} else {
			poi.PriceLevel = "Free"
		}
		if row.Rating != nil {
			poi.Rating = *row.Rating
		}
		if row.CityID != nil {
			poi.City = *row.CityID
		}
		if row.RatingCount != nil {
			popularityScore := int(*row.RatingCount)
			if row.IsSponsored != nil && *row.IsSponsored {
				popularityScore += 50
			}
			if popularityScore > 100 {
				poi.Priority = 10
			} else if popularityScore > 0 {
				poi.Priority = (popularityScore / 10) + 1
			} else {
				poi.Priority = 1
			}
		}

		pois = append(pois, poi)
	}

	l.InfoContext(ctx, "POIs by location and distance found",
		slog.Int("count", len(pois)),
		slog.Float64("radius_km", radiusMeters/1000))
	span.SetAttributes(attribute.Int("results.count", len(pois)))
	span.SetStatus(codes.Ok, "POIs by location and distance retrieved")

	return pois, nil
}

// GetPOIsByLocationAndDistanceWithCategory retrieves POIs within a specified radius from a given location filtered by category
func (r *RepositoryImpl) GetPOIsByLocationAndDistanceWithCategory(ctx context.Context, lat, lon, radiusMeters float64, category string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIRepository").Start(ctx, "GetPOIsByLocationAndDistanceWithCategory", trace.WithAttributes(
		attribute.Float64("location.lat", lat),
		attribute.Float64("location.lon", lon),
		attribute.Float64("radius.meters", radiusMeters),
		attribute.String("category", category),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetPOIsByLocationAndDistanceWithCategory"))

	// Build the query with category filter
	baseQuery := `
					SELECT
						id,
						name,
						description,
						longitude,
						latitude,
						category,
						address,
						website,
						phone_number,
						opening_hours,
						poi_type,
						price_level,
						rating,
						ROUND(CAST(distance_meters / 1000.0 AS numeric), 2) as distance,
						city_id,
						COALESCE(tags, '{}') as tags,
						COALESCE(rating_count, 0) as rating_count,
						COALESCE(is_sponsored, false) as is_sponsored
					FROM (
						SELECT
							id,
							name,
							COALESCE(description, '') as description,
							ST_X(location) as longitude,
							ST_Y(location) as latitude,
							COALESCE(category, '') as category,
							COALESCE(address, '') as address,
							COALESCE(website, '') as website,
							COALESCE(phone_number, '') as phone_number,
							opening_hours,
							COALESCE(poi_type, '') as poi_type,
							price_level,
							COALESCE(average_rating, 0) as rating,
							ST_Distance(location::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) as distance_meters,
							city_id,
							tags,
							rating_count,
							is_sponsored
						FROM points_of_interest
						WHERE ST_DWithin(
							location::geography,
							ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
							$3
						)
						AND LOWER(category) = LOWER($4)
					) sub
					ORDER BY distance ASC LIMIT 50
				`

	var args []any
	args = append(args, lon, lat, radiusMeters, category) // $1, $2, $3, $4

	l.DebugContext(ctx, "Executing POI distance query with category filter",
		slog.String("query", baseQuery),
		slog.Float64("lat", lat),
		slog.Float64("lon", lon),
		slog.Float64("radius_meters", radiusMeters),
		slog.String("category", category))

	rows, err := r.pgpool.Query(ctx, baseQuery, args...)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query POIs by location, distance and category", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query POIs by location, distance and category: %w", err)
	}
	defer rows.Close()

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[struct {
		ID           uuid.UUID `db:"id"`
		Name         string    `db:"name"`
		Description  *string   `db:"description"`
		Longitude    float64   `db:"longitude"`
		Latitude     float64   `db:"latitude"`
		Category     string    `db:"category"`
		Address      *string   `db:"address"`
		Website      *string   `db:"website"`
		PhoneNumber  *string   `db:"phone_number"`
		OpeningHours *string   `db:"opening_hours"`
		PoiType      *string   `db:"poi_type"`
		PriceLevel   *int32    `db:"price_level"`
		Rating       *float64  `db:"rating"`
		Distance     float64   `db:"distance"`
		CityID       *string   `db:"city_id"`
		Tags         []string  `db:"tags"`
		RatingCount  *int32    `db:"rating_count"`
		IsSponsored  *bool     `db:"is_sponsored"`
	}])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect POI rows", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Row collection failed")
		return nil, fmt.Errorf("failed to collect POI rows: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, 0, len(dbRows))
	for _, row := range dbRows {
		poi := locitypes.POIDetailedInfo{
			ID:        row.ID,
			Name:      row.Name,
			Longitude: row.Longitude,
			Latitude:  row.Latitude,
			Category:  row.Category,
			Distance:  row.Distance,
			Tags:      row.Tags,
		}
		if row.Description != nil {
			poi.DescriptionPOI = *row.Description
		}
		if row.Address != nil {
			poi.Address = *row.Address
		}
		if row.Website != nil {
			poi.Website = *row.Website
		}
		if row.PhoneNumber != nil {
			poi.PhoneNumber = *row.PhoneNumber
		}
		if row.OpeningHours != nil {
			poi.OpeningHours = map[string]string{"general": *row.OpeningHours}
		}
		if row.PoiType != nil {
			poi.Category = *row.PoiType
		}
		if row.PriceLevel != nil {
			poi.PriceLevel = fmt.Sprintf("%d", *row.PriceLevel)
		}
		if row.Rating != nil {
			poi.Rating = *row.Rating
		}
		if row.CityID != nil {
			poi.City = *row.CityID
		}
		pois = append(pois, poi)
	}

	l.InfoContext(ctx, "POIs by location, distance and category found",
		slog.Int("count", len(pois)),
		slog.Float64("radius_km", radiusMeters/1000),
		slog.String("category", category))
	span.SetAttributes(attribute.Int("results.count", len(pois)))
	span.SetStatus(codes.Ok, "POIs by location, distance and category retrieved")

	return pois, nil
}

func (r *RepositoryImpl) SaveLlmInteraction(ctx context.Context, interaction *locitypes.LlmInteraction) (uuid.UUID, error) {
	ctx, span := otel.Tracer("POIRepository").Start(ctx, "SaveLlmInteraction")
	defer span.End()

	l := r.logger.With(slog.String("method", "SaveLlmInteraction"))

	query := `
		INSERT INTO llm_interactions (user_id, model_name, prompt, response, latitude, longitude, distance)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`

	var id uuid.UUID
	if err := r.pgpool.QueryRow(ctx, query, interaction.UserID, interaction.ModelName, interaction.Prompt, interaction.Response, interaction.Latitude, interaction.Longitude, interaction.Distance).Scan(&id); err != nil {
		l.ErrorContext(ctx, "Failed to save LLM interaction", slog.Any("error", err))
		span.RecordError(err)
		return uuid.Nil, fmt.Errorf("failed to save LLM interaction: %w", err)
	}

	l.InfoContext(ctx, "Successfully saved LLM interaction", slog.String("id", id.String()))
	span.SetStatus(codes.Ok, "LLM interaction saved successfully")
	return id, nil
}

func (r *RepositoryImpl) SaveLlmPoisToDatabase(ctx context.Context, userID uuid.UUID, pois []locitypes.POIDetailedInfo, _ *locitypes.GenAIResponse, llmInteractionID uuid.UUID) error {
	ctx, span := otel.Tracer("POIRepository").Start(ctx, "SaveLlmPoisToDatabase", trace.WithAttributes(
		attribute.Int("poi.count", len(pois)),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "SaveLlmPoisToDatabase"))

	if len(pois) == 0 {
		l.InfoContext(ctx, "No LLM POIs to save.")
		return nil
	}

	tx, err := r.pgpool.Begin(ctx)
	if err != nil {
		l.ErrorContext(ctx, "Failed to begin transaction for saving LLM POIs", slog.Any("error", err))
		span.RecordError(err)
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			l.ErrorContext(ctx, "Failed to rollback transaction", slog.Any("error", rollbackErr))
		}
	}() // Rollback on error

	const insertLLMPOIQuery = `
        INSERT INTO llm_suggested_pois (id, user_id, llm_interaction_id, name, latitude, longitude, category, description_poi, distance, location)
        VALUES ($1, $2, $3, $4::TEXT, $5, $6, $7, $8, $9, ST_SetSRID(ST_MakePoint($6, $5), 4326))
        ON CONFLICT (name, latitude, longitude) DO NOTHING
    `

	batch := &pgx.Batch{}
	validCount := 0
	for _, poi := range pois {
		if poi.Name == "" {
			l.WarnContext(ctx, "POI has empty or nil name, skipping", slog.String("poi_name", poi.Name))
			continue
		}
		if poi.Latitude == 0 || poi.Longitude == 0 {
			l.WarnContext(ctx, "POI has invalid coordinates, skipping", slog.String("poi_name", poi.Name))
			continue
		}

		l.DebugContext(ctx, "Queueing POI batch insert",
			slog.String("poi_name", poi.Name),
			slog.Float64("latitude", poi.Latitude),
			slog.Float64("longitude", poi.Longitude),
			slog.String("category", poi.Category),
			slog.String("description", poi.Description),
			slog.Float64("distance", poi.Distance))

		batch.Queue(insertLLMPOIQuery, poi.ID, userID, llmInteractionID, poi.Name, poi.Latitude, poi.Longitude, poi.Category, poi.Description, poi.Distance)
		validCount++
	}

	if validCount == 0 {
		l.InfoContext(ctx, "No valid LLM POIs to insert after validation")
		return nil
	}

	br := tx.SendBatch(ctx, batch)
	defer br.Close()

	for i := 0; i < validCount; i++ {
		if _, err := br.Exec(); err != nil {
			l.ErrorContext(ctx, "Failed to execute LLM POI batch insert", slog.Any("error", err), slog.Int("batch_index", i))
			span.RecordError(err)
			return fmt.Errorf("failed to insert LLM POI at batch index %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		l.ErrorContext(ctx, "Failed to commit transaction for saving LLM POIs", slog.Any("error", err))
		span.RecordError(err)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	l.InfoContext(ctx, "Successfully saved LLM POIs to database", slog.Int("count", len(pois)))
	span.SetStatus(codes.Ok, "LLM POIs saved successfully")
	return nil
}

// CalculateDistancePostGIS calculateDistancePostGIS computes the distance between two points using PostGIS (in meters)
func (r *RepositoryImpl) CalculateDistancePostGIS(ctx context.Context, userLat, userLon, poiLat, poiLon float64) (float64, error) {
	query := `
        SELECT ST_Distance(
            ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
            ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography
        ) AS distance;
    `
	var distance float64
	if err := r.pgpool.QueryRow(ctx, query, userLon, userLat, poiLon, poiLat).Scan(&distance); err != nil {
		return 0, fmt.Errorf("failed to calculate distance with PostGIS: %w", err)
	}
	return distance, nil
}

// FindLLMPOIByNameAndCity finds an existing LLM POI by name and city
func (r *RepositoryImpl) FindLLMPOIByNameAndCity(ctx context.Context, name, city string) (uuid.UUID, error) {
	ctx, span := otel.Tracer("POIRepository").Start(ctx, "FindLLMPOIByNameAndCity")
	defer span.End()

	query := `
		SELECT id
		FROM llm_suggested_pois
		WHERE LOWER(name) = LOWER($1) AND LOWER(city_name) = LOWER($2)
		LIMIT 1
	`
	var id uuid.UUID
	if err := r.pgpool.QueryRow(ctx, query, name, city).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("LLM POI not found")
		}
		return uuid.Nil, fmt.Errorf("failed to find LLM POI: %w", err)
	}

	span.SetAttributes(attribute.String("poi.name", name), attribute.String("poi.city", city))
	return id, nil
}

// FindLLMPOIByName finds an existing LLM POI by name across all cities
func (r *RepositoryImpl) FindLLMPOIByName(ctx context.Context, name string) (uuid.UUID, error) {
	ctx, span := otel.Tracer("POIRepository").Start(ctx, "FindLLMPOIByName")
	defer span.End()

	query := `
		SELECT id
		FROM llm_suggested_pois
		WHERE LOWER(name) = LOWER($1)
		LIMIT 1
	`
	var id uuid.UUID
	if err := r.pgpool.QueryRow(ctx, query, name).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("LLM POI not found")
		}
		return uuid.Nil, fmt.Errorf("failed to find LLM POI: %w", err)
	}

	span.SetAttributes(attribute.String("poi.name", name))
	return id, nil
}
