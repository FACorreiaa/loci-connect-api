package recents

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

var _ Repository = (*RepositoryImpl)(nil)

// PgxPool is an interface that abstracts pgxpool.Pool for testing.
type PgxPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ PgxPool = (*pgxpool.Pool)(nil)

type Repository interface {
	GetUserRecentInteractions(ctx context.Context, userID uuid.UUID, page, limit int, filterOptions *locitypes.RecentInteractionsFilter) (*locitypes.RecentInteractionsResponse, error)
	GetCityPOIsByInteraction(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.POIDetailedInfo, error)
	GetCityHotelsByInteraction(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.HotelDetailedInfo, error)
	GetCityRestaurantsByInteraction(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.RestaurantDetailedInfo, error)
	GetCityItinerariesByInteraction(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.UserSavedItinerary, error)
	GetCityFavorites(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.POIDetailedInfo, error)
	GetUserActivityFeed(ctx context.Context, userID uuid.UUID, limit, offset int, filter locitypes.ActivityFeedFilter) ([]locitypes.ActivityEntry, error)
}

type RepositoryImpl struct {
	pgpool PgxPool
	logger *slog.Logger
}

func NewRepository(pgpool PgxPool, logger *slog.Logger) *RepositoryImpl {
	return &RepositoryImpl{
		pgpool: pgpool,
		logger: logger,
	}
}

// Local row structs for database scanning

// cityInteractionRow is used for the main recent interactions query
type cityInteractionRow struct {
	CityName         string    `db:"city_name"`
	LastActivity     time.Time `db:"last_activity"`
	InteractionCount int       `db:"interaction_count"`
	SessionID        uuid.UUID `db:"session_id"`
	Title            string    `db:"title"`
	POICount         int       `db:"poi_count"`
}

// activityFeedRow is one row of activityFeedQuery. Every column is coalesced to
// a non-NULL value in SQL so this can stay free of pointers: a feed row with no
// city is a feed row with an empty city, not a missing one.
type activityFeedRow struct {
	Kind       string    `db:"kind"`
	ID         string    `db:"id"`
	RefID      string    `db:"ref_id"`
	Detail     string    `db:"detail"`
	CityName   string    `db:"city_name"`
	CityID     string    `db:"city_id"`
	Label      string    `db:"label"`
	OccurredAt time.Time `db:"occurred_at"`
}

// recentInteractionRow is used for getCityInteractions query
type recentInteractionRow struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	CityName  string     `db:"city_name"`
	CityID    *uuid.UUID `db:"city_id"`
	Prompt    string     `db:"prompt"`
	Response  *string    `db:"response"`
	ModelName string     `db:"model_name"`
	LatencyMs int        `db:"latency_ms"`
	CreatedAt time.Time  `db:"created_at"`
}

// poiRow is used for POI queries
type poiRow struct {
	ID           uuid.UUID `db:"id"`
	Name         string    `db:"name"`
	Latitude     float64   `db:"latitude"`
	Longitude    float64   `db:"longitude"`
	Description  *string   `db:"description"`
	Address      *string   `db:"address"`
	Website      *string   `db:"website"`
	PhoneNumber  *string   `db:"phone_number"`
	OpeningHours *string   `db:"opening_hours"`
	PriceRange   *string   `db:"price_range"`
	Category     *string   `db:"category"`
	Tags         []string  `db:"tags"`
	Images       []string  `db:"images"`
	Rating       float64   `db:"rating"`
	CreatedAt    time.Time `db:"created_at"`
}

// hotelRow is used for hotel queries
type hotelRow struct {
	ID               uuid.UUID `db:"id"`
	Name             string    `db:"name"`
	Latitude         float64   `db:"latitude"`
	Longitude        float64   `db:"longitude"`
	Category         *string   `db:"category"`
	Description      *string   `db:"description"`
	Address          *string   `db:"address"`
	Website          *string   `db:"website"`
	PhoneNumber      *string   `db:"phone_number"`
	PriceRange       *string   `db:"price_range"`
	Tags             []string  `db:"tags"`
	Images           []string  `db:"images"`
	Rating           float64   `db:"rating"`
	LlmInteractionID uuid.UUID `db:"llm_interaction_id"`
}

// restaurantRow is used for restaurant queries
type restaurantRow struct {
	ID               uuid.UUID `db:"id"`
	Name             string    `db:"name"`
	Latitude         float64   `db:"latitude"`
	Longitude        float64   `db:"longitude"`
	Category         *string   `db:"category"`
	Description      *string   `db:"description"`
	Address          *string   `db:"address"`
	Website          *string   `db:"website"`
	PhoneNumber      *string   `db:"phone_number"`
	PriceLevel       *string   `db:"price_level"`
	CuisineType      *string   `db:"cuisine_type"`
	Tags             []string  `db:"tags"`
	Images           []string  `db:"images"`
	Rating           float64   `db:"rating"`
	LlmInteractionID uuid.UUID `db:"llm_interaction_id"`
}

// itineraryRow is used for itinerary queries
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
	CreatedAt              time.Time  `db:"created_at"`
	UpdatedAt              time.Time  `db:"updated_at"`
}

// GetUserRecentInteractions fetches recent interactions grouped by city
func (r *RepositoryImpl) GetUserRecentInteractions(ctx context.Context, userID uuid.UUID, page, limit int, filterOptions *locitypes.RecentInteractionsFilter) (*locitypes.RecentInteractionsResponse, error) {
	ctx, span := otel.Tracer("RecentsRepository").Start(ctx, "GetUserRecentInteractions", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.Int("page", page),
		attribute.Int("limit", limit),
		attribute.String("sort_by", filterOptions.SortBy),
		attribute.String("sort_order", filterOptions.SortOrder),
		attribute.String("search", filterOptions.Search),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetUserRecentInteractions"))

	// Build WHERE clause with filters - include ALL interactions, not just those with city_name
	whereConditions := []string{"user_id = $1"}
	args := []any{userID}
	argIndex := 2

	// Add search filter
	if filterOptions.Search != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("LOWER(city_name) LIKE LOWER($%d)", argIndex))
		args = append(args, "%"+filterOptions.Search+"%")
		argIndex++
	}

	// Build HAVING clause for interaction count filters
	// Only add conditions when they're actually specified (> 0)
	havingConditions := []string{}
	if filterOptions.MinInteractions > 0 {
		havingConditions = append(havingConditions, fmt.Sprintf("COUNT(*) >= %d", filterOptions.MinInteractions))
	}
	if filterOptions.MaxInteractions > 0 {
		havingConditions = append(havingConditions, fmt.Sprintf("COUNT(*) <= %d", filterOptions.MaxInteractions))
	}

	// Build ORDER BY clause
	var orderBy string
	switch filterOptions.SortBy {
	case "city_name":
		orderBy = "city_name"
	case "interaction_count":
		orderBy = "interaction_count"
	case "poi_count":
		orderBy = "poi_count"
	default:
		orderBy = "last_activity"
	}

	if filterOptions.SortOrder == "asc" {
		orderBy += " ASC"
	} else {
		orderBy += " DESC"
	}

	// Build the main query - simplified to avoid correlated subquery issues
	// Group by normalized city_name (NULL/empty becomes 'General Search')
	subquery := fmt.Sprintf(`
        SELECT
            COALESCE(NULLIF(l.city_name, ''), 'General Search') as city_name,
            MAX(l.created_at) as last_activity,
            COUNT(*) as interaction_count,
            MAX(l.session_id::text)::uuid as session_id,
            CASE
                WHEN MAX(l.city_name) IS NOT NULL AND MAX(l.city_name) != ''
                THEN 'Trip to ' || MAX(l.city_name)
                ELSE 'Travel Planning'
            END as title,
            0 as poi_count
        FROM llm_interactions l
        WHERE %s
        GROUP BY COALESCE(NULLIF(l.city_name, ''), 'General Search')
        %s
    `, strings.Join(whereConditions, " AND "),
		func() string {
			if len(havingConditions) > 0 {
				return "HAVING " + strings.Join(havingConditions, " AND ")
			}
			return ""
		}())

	query := fmt.Sprintf(`
        SELECT
            city_name,
            last_activity,
            interaction_count,
            session_id,
            title,
            poi_count
        FROM (%s) as city_data
        ORDER BY %s
        LIMIT $%d OFFSET $%d
    `, subquery, orderBy, argIndex, argIndex+1)

	args = append(args, limit, (page-1)*limit)
	l.InfoContext(ctx, "Executing query",
		slog.String("query", query),
		slog.Any("params", args))

	rows, err := r.pgpool.Query(ctx, query, args...)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query recent interactions", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query recent interactions: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[cityInteractionRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect city interaction rows", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database read failed")
		return nil, fmt.Errorf("failed to read recent interactions: %w", err)
	}

	var cities []locitypes.CityInteractions
	for _, row := range dbRows {
		interactions, err := r.getCityInteractions(ctx, userID, row.CityName)
		if err != nil {
			l.WarnContext(ctx, "Failed to get interactions for city",
				slog.String("city", row.CityName),
				slog.Any("error", err))
			continue
		}

		cities = append(cities, locitypes.CityInteractions{
			CityName:     row.CityName,
			Interactions: interactions,
			SessionIDs:   []uuid.UUID{row.SessionID},
			POICount:     row.POICount,
			LastActivity: row.LastActivity,
			SessionID:    row.SessionID,
			Title:        row.Title,
		})
	}

	// Build count query with same filters - count cities, not sessions
	countSubquery := fmt.Sprintf(`
        SELECT
            l.city_name,
            COUNT(*) as interaction_count
        FROM llm_interactions l
        WHERE %s
        GROUP BY l.city_name, l.user_id
        %s
    `, strings.Join(whereConditions, " AND "),
		func() string {
			if len(havingConditions) > 0 {
				return "HAVING " + strings.Join(havingConditions, " AND ")
			}
			return ""
		}())

	// For count, we need to count the results from the grouped query
	countWrapperQuery := fmt.Sprintf("SELECT COUNT(*) as count FROM (%s) as grouped_results", countSubquery)

	// Use the same args except for limit and offset
	countArgs := args[:len(args)-2]

	var totalCount int
	if err := r.pgpool.QueryRow(ctx, countWrapperQuery, countArgs...).Scan(&totalCount); err != nil {
		l.ErrorContext(ctx, "Failed to query total recent interactions count", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get total count: %w", err)
	}

	l.InfoContext(ctx, "Successfully retrieved recent interactions",
		slog.Int("cities_count", totalCount),
		slog.String("user_id", userID.String()))

	span.SetAttributes(attribute.Int("results.cities", totalCount))
	span.SetStatus(codes.Ok, "Recent interactions retrieved")

	return &locitypes.RecentInteractionsResponse{
		Cities: cities,
		Total:  totalCount,
	}, nil
}

// getCityInteractions gets recent interactions for a specific city
func (r *RepositoryImpl) getCityInteractions(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.RecentInteraction, error) {
	query := `
		SELECT
			id,
			user_id,
			city_name,
			city_id,
			prompt,
			response,
			model_name,
			latency_ms,
			created_at
		FROM llm_interactions
		WHERE user_id = $1 AND city_name = $2
		ORDER BY created_at DESC
		LIMIT 5
	`

	rows, err := r.pgpool.Query(ctx, query, userID, cityName)
	if err != nil {
		return nil, fmt.Errorf("failed to query city interactions: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[recentInteractionRow])
	if err != nil {
		return nil, fmt.Errorf("failed to read city interactions: %w", err)
	}

	interactions := make([]locitypes.RecentInteraction, 0, len(dbRows))
	for _, row := range dbRows {
		interaction := locitypes.RecentInteraction{
			ID:        row.ID,
			UserID:    row.UserID,
			CityName:  row.CityName,
			CityID:    row.CityID,
			Prompt:    row.Prompt,
			ModelUsed: row.ModelName,
			LatencyMs: row.LatencyMs,
			CreatedAt: row.CreatedAt,
		}
		if row.Response != nil {
			interaction.ResponseText = *row.Response
		}
		interactions = append(interactions, interaction)
	}

	return interactions, nil
}

// GetCityPOIsByInteraction gets all POIs for a city from user's interactions
func (r *RepositoryImpl) GetCityPOIsByInteraction(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("RecentsRepository").Start(ctx, "GetCityPOIsByInteraction", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("city_name", cityName),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetCityPOIsByInteraction"))

	query := `
		SELECT DISTINCT
			pd.id,
			pd.name,
			pd.latitude,
			pd.longitude,
			pd.description,
			pd.address,
			pd.website,
			pd.phone_number,
			pd.opening_hours,
			pd.price_range,
			pd.category,
			pd.tags,
			pd.images,
			pd.rating,
			pd.created_at
		FROM poi_details pd
		JOIN llm_interactions li ON pd.llm_interaction_id = li.id
		WHERE li.user_id = $1 AND li.city_name = $2
		ORDER BY pd.created_at DESC
	`

	rows, err := r.pgpool.Query(ctx, query, userID, cityName)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query city POIs", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query city POIs: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[poiRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect POI rows", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database read failed")
		return nil, fmt.Errorf("failed to read city POIs: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, 0, len(dbRows))
	for _, row := range dbRows {
		poi := locitypes.POIDetailedInfo{
			ID:        row.ID,
			Name:      row.Name,
			Latitude:  row.Latitude,
			Longitude: row.Longitude,
			Rating:    row.Rating,
			Tags:      row.Tags,
			Images:    row.Images,
			CreatedAt: row.CreatedAt,
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
		if row.PriceRange != nil {
			poi.PriceRange = *row.PriceRange
		}
		if row.Category != nil {
			poi.Category = *row.Category
		}

		pois = append(pois, poi)
	}

	l.InfoContext(ctx, "Successfully retrieved city POIs",
		slog.String("city_name", cityName),
		slog.Int("poi_count", len(pois)))

	span.SetAttributes(attribute.Int("results.pois", len(pois)))
	span.SetStatus(codes.Ok, "City POIs retrieved")

	return pois, nil
}

// GetCityHotelsByInteraction gets all hotels for a city from user's interactions
func (r *RepositoryImpl) GetCityHotelsByInteraction(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.HotelDetailedInfo, error) {
	ctx, span := otel.Tracer("RecentsRepository").Start(ctx, "GetCityHotelsByInteraction", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("city_name", cityName),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetCityHotelsByInteraction"))

	query := `
		SELECT DISTINCT
			hd.id,
			hd.name,
			hd.latitude,
			hd.longitude,
			hd.category,
			hd.description,
			hd.address,
			hd.website,
			hd.phone_number,
			hd.price_range,
			hd.tags,
			hd.images,
			hd.rating,
			hd.llm_interaction_id
		FROM hotel_details hd
		JOIN llm_interactions li ON hd.llm_interaction_id = li.id
		WHERE li.user_id = $1 AND li.city_name = $2
		ORDER BY hd.created_at DESC
	`

	rows, err := r.pgpool.Query(ctx, query, userID, cityName)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query city hotels", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query city hotels: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[hotelRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect hotel rows", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database read failed")
		return nil, fmt.Errorf("failed to read city hotels: %w", err)
	}

	hotels := make([]locitypes.HotelDetailedInfo, 0, len(dbRows))
	for _, row := range dbRows {
		hotel := locitypes.HotelDetailedInfo{
			ID:               row.ID,
			Name:             row.Name,
			Latitude:         row.Latitude,
			Longitude:        row.Longitude,
			Rating:           row.Rating,
			Tags:             row.Tags,
			Images:           row.Images,
			LlmInteractionID: row.LlmInteractionID,
			City:             cityName,
		}

		if row.Category != nil {
			hotel.Category = *row.Category
		}
		if row.Description != nil {
			hotel.Description = *row.Description
		}
		if row.Address != nil {
			hotel.Address = *row.Address
		}
		if row.Website != nil {
			hotel.Website = row.Website
		}
		if row.PhoneNumber != nil {
			hotel.PhoneNumber = row.PhoneNumber
		}
		if row.PriceRange != nil {
			hotel.PriceRange = row.PriceRange
		}

		hotels = append(hotels, hotel)
	}

	l.InfoContext(ctx, "Successfully retrieved city hotels",
		slog.String("city_name", cityName),
		slog.Int("hotel_count", len(hotels)))

	span.SetAttributes(attribute.Int("results.hotels", len(hotels)))
	span.SetStatus(codes.Ok, "City hotels retrieved")

	return hotels, nil
}

// GetCityRestaurantsByInteraction gets all restaurants for a city from user's interactions
func (r *RepositoryImpl) GetCityRestaurantsByInteraction(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.RestaurantDetailedInfo, error) {
	ctx, span := otel.Tracer("RecentsRepository").Start(ctx, "GetCityRestaurantsByInteraction", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("city_name", cityName),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetCityRestaurantsByInteraction"))

	query := `
		SELECT DISTINCT
			rd.id,
			rd.name,
			rd.latitude,
			rd.longitude,
			rd.category,
			rd.description,
			rd.address,
			rd.website,
			rd.phone_number,
			rd.price_level,
			rd.cuisine_type,
			rd.tags,
			rd.images,
			rd.rating,
			rd.llm_interaction_id
		FROM restaurant_details rd
		JOIN llm_interactions li ON rd.llm_interaction_id = li.id
		WHERE li.user_id = $1 AND li.city_name = $2
		ORDER BY rd.created_at DESC
	`

	rows, err := r.pgpool.Query(ctx, query, userID, cityName)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query city restaurants", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query city restaurants: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[restaurantRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect restaurant rows", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database read failed")
		return nil, fmt.Errorf("failed to read city restaurants: %w", err)
	}

	restaurants := make([]locitypes.RestaurantDetailedInfo, 0, len(dbRows))
	for _, row := range dbRows {
		restaurant := locitypes.RestaurantDetailedInfo{
			ID:               row.ID,
			Name:             row.Name,
			Latitude:         row.Latitude,
			Longitude:        row.Longitude,
			Rating:           row.Rating,
			Tags:             row.Tags,
			Images:           row.Images,
			LlmInteractionID: row.LlmInteractionID,
			City:             cityName,
		}

		if row.Category != nil {
			restaurant.Category = *row.Category
		}
		if row.Description != nil {
			restaurant.Description = *row.Description
		}
		restaurant.Address = row.Address
		restaurant.Website = row.Website
		restaurant.PhoneNumber = row.PhoneNumber
		restaurant.PriceLevel = row.PriceLevel
		restaurant.CuisineType = row.CuisineType

		restaurants = append(restaurants, restaurant)
	}

	l.InfoContext(ctx, "Successfully retrieved city restaurants",
		slog.String("city_name", cityName),
		slog.Int("restaurant_count", len(restaurants)))

	span.SetAttributes(attribute.Int("results.restaurants", len(restaurants)))
	span.SetStatus(codes.Ok, "City restaurants retrieved")

	return restaurants, nil
}

// GetCityItinerariesByInteraction gets all saved itineraries for a city from user's interactions
func (r *RepositoryImpl) GetCityItinerariesByInteraction(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.UserSavedItinerary, error) {
	ctx, span := otel.Tracer("RecentsRepository").Start(ctx, "GetCityItinerariesByInteraction", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("city_name", cityName),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetCityItinerariesByInteraction"))

	query := `
		SELECT DISTINCT
			usi.id,
			usi.user_id,
			usi.source_llm_interaction_id,
			usi.session_id,
			usi.primary_city_id,
			usi.title,
			usi.description,
			usi.markdown_content,
			usi.tags,
			usi.estimated_duration_days,
			usi.estimated_cost_level,
			usi.is_public,
			usi.created_at,
			usi.updated_at
		FROM user_saved_itineraries usi
		JOIN llm_interactions li ON (usi.source_llm_interaction_id = li.id OR usi.session_id IN (
			SELECT DISTINCT session_id FROM llm_interactions WHERE user_id = $1 AND city_name = $2
		))
		WHERE usi.user_id = $1
		ORDER BY usi.created_at DESC
	`

	rows, err := r.pgpool.Query(ctx, query, userID, cityName)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query city itineraries", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query city itineraries: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[itineraryRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect itinerary rows", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database read failed")
		return nil, fmt.Errorf("failed to read city itineraries: %w", err)
	}

	itineraries := make([]locitypes.UserSavedItinerary, 0, len(dbRows))
	for _, row := range dbRows {
		itinerary := locitypes.UserSavedItinerary{
			ID:                     row.ID,
			UserID:                 row.UserID,
			Title:                  row.Title,
			MarkdownContent:        row.MarkdownContent,
			Tags:                   row.Tags,
			IsPublic:               row.IsPublic,
			CreatedAt:              row.CreatedAt,
			UpdatedAt:              row.UpdatedAt,
			SourceLlmInteractionID: row.SourceLlmInteractionID,
			SessionID:              row.SessionID,
			PrimaryCityID:          row.PrimaryCityID,
			Description:            row.Description,
			EstimatedDurationDays:  row.EstimatedDurationDays,
			EstimatedCostLevel:     row.EstimatedCostLevel,
		}

		itineraries = append(itineraries, itinerary)
	}

	l.InfoContext(ctx, "Successfully retrieved city itineraries",
		slog.String("city_name", cityName),
		slog.Int("itinerary_count", len(itineraries)))

	span.SetAttributes(attribute.Int("results.itineraries", len(itineraries)))
	span.SetStatus(codes.Ok, "City itineraries retrieved")

	return itineraries, nil
}

// GetCityFavorites gets all favorite POIs for a city (both regular and LLM POIs)
func (r *RepositoryImpl) GetCityFavorites(ctx context.Context, userID uuid.UUID, cityName string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("RecentsRepository").Start(ctx, "GetCityFavorites", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("city_name", cityName),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetCityFavorites"))

	// Query both regular POI favorites and LLM POI favorites
	query := `
		-- Regular POI favorites
		SELECT DISTINCT
			pd.id,
			pd.name,
			pd.latitude,
			pd.longitude,
			pd.description,
			pd.address,
			pd.website,
			pd.phone_number,
			pd.opening_hours,
			pd.price_range,
			pd.category,
			pd.tags,
			pd.images,
			pd.rating,
			pd.created_at
		FROM poi_details pd
		JOIN user_favorite_pois ufp ON pd.id = ufp.poi_id
		JOIN cities c ON pd.city_id = c.id
		WHERE ufp.user_id = $1 AND LOWER(c.name) = LOWER($2)

		UNION

		-- LLM POI favorites
		SELECT DISTINCT
			lp.id,
			lp.name,
			lp.latitude,
			lp.longitude,
			lp.description,
			lp.address,
			lp.website,
			lp.phone_number,
			lp.opening_hours,
			lp.price_range,
			lp.category,
			lp.tags,
			lp.images,
			lp.rating,
			lp.created_at
		FROM llm_poi lp
		JOIN user_favorite_llm_pois uflp ON lp.id = uflp.llm_poi_id
		JOIN llm_interactions li ON lp.llm_interaction_id = li.id
		WHERE uflp.user_id = $1 AND LOWER(li.city_name) = LOWER($2)

		ORDER BY created_at DESC
	`

	rows, err := r.pgpool.Query(ctx, query, userID, cityName)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query city favorites", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query city favorites: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[poiRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect favorite POI rows", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database read failed")
		return nil, fmt.Errorf("failed to read city favorites: %w", err)
	}

	pois := make([]locitypes.POIDetailedInfo, 0, len(dbRows))
	for _, row := range dbRows {
		poi := locitypes.POIDetailedInfo{
			ID:        row.ID,
			Name:      row.Name,
			Latitude:  row.Latitude,
			Longitude: row.Longitude,
			Rating:    row.Rating,
			Tags:      row.Tags,
			Images:    row.Images,
			CreatedAt: row.CreatedAt,
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
		if row.PriceRange != nil {
			poi.PriceRange = *row.PriceRange
		}
		if row.Category != nil {
			poi.Category = *row.Category
		}

		pois = append(pois, poi)
	}

	l.InfoContext(ctx, "Successfully retrieved city favorites",
		slog.String("city_name", cityName),
		slog.Int("favorite_count", len(pois)))

	span.SetAttributes(attribute.Int("results.favorites", len(pois)))
	span.SetStatus(codes.Ok, "City favorites retrieved")

	return pois, nil
}

// activityFeedQuery is the recents feed: everything the user did, newest
// first, in one pass.
//
// Three sources, because "what did I do" is not one table:
//
//   - llm_interactions — one row per prompt the unified chat stream handled, so
//     one row per thing the user asked for. This is the bulk of the feed.
//   - user_saved_itineraries — the trips they kept.
//   - user_favorites — the places they starred.
//
// Four things here are load-bearing.
//
// **The prompt branch is an allowlist, not a denylist.** llm_interactions also
// holds internal model calls — POI detail lookups written by the chat service's
// own helpers — which are not activities anybody performed. Matching the exact
// prefix buildInteractionRow writes means any future internal prompt is
// excluded by default rather than by being enumerated. `intent IS NOT NULL` is
// the same allowlist for rows written since intent started being recorded;
// nothing but the user-facing stream sets it.
//
// **The COALESCE on intent** reads the domain back out of that prefix for rows
// written before the column was populated, so the feed is not blank on the day
// this ships. Migration 0088 backfills them; this stays as the safety net.
//
// **Each branch carries its own LIMIT.** The global newest-N is necessarily a
// subset of the union of the per-branch newest-N, so limiting inside each
// branch is correct and bounds the merge to three pages instead of making
// Postgres materialise every row the user has ever produced and sort it. The
// branch limit is limit+offset, because page two's rows are in the first
// limit+offset of each branch.
//
// **The ordering is (timestamp, id), never timestamp alone.** Favourites added
// in one batch share added_at to the microsecond. With a bare timestamp sort
// those ties are free to reorder between two page fetches, which duplicates
// some rows and silently drops others.
//
// Every filter is a fixed placeholder tested with `$n IS NULL OR ...`, so the
// query is a constant and the planner sees the same shape every time.
const activityFeedQuery = `
WITH prompts AS (
    SELECT
        l.id                                    AS id,
        'prompt'::text                          AS kind,
        COALESCE(l.session_id::text, '')::text  AS ref_id,
        COALESCE(
            NULLIF(l.intent, ''),
            substring(l.prompt from '^Unified Chat Stream - Domain:\s*(\w+),'),
            ''
        )::text                                 AS detail,
        COALESCE(l.city_name, '')::text         AS city_name,
        COALESCE(l.city_id::text, '')::text     AS city_id,
        l.prompt::text                          AS label,
        l.created_at                            AS occurred_at
    FROM llm_interactions l
    WHERE l.user_id = $1
      AND (l.intent IS NOT NULL OR l.prompt LIKE 'Unified Chat Stream - Domain: %%')
      AND ($5::text[] IS NULL OR 'prompt' = ANY($5))
      AND ($6::text[] IS NULL OR COALESCE(NULLIF(l.intent, ''), substring(l.prompt from '^Unified Chat Stream - Domain:\s*(\w+),'), '') = ANY($6))
      AND ($7::text IS NULL OR l.prompt ILIKE '%%' || $7 || '%%')
      AND ($8::text IS NULL OR LOWER(l.city_name) = LOWER($8))
      AND ($9::timestamptz IS NULL OR l.created_at >= $9)
      AND ($10::timestamptz IS NULL OR l.created_at <= $10)
    ORDER BY l.created_at %[1]s, l.id %[1]s
    LIMIT $2
),
saved AS (
    SELECT
        s.id,
        'saved_itinerary'::text,
        COALESCE(s.session_id::text, '')::text,
        'itinerary'::text,
        COALESCE(c.name, '')::text,
        COALESCE(s.primary_city_id::text, '')::text,
        s.title::text,
        s.created_at
    FROM user_saved_itineraries s
    LEFT JOIN cities c ON c.id = s.primary_city_id
    WHERE s.user_id = $1
      AND ($5::text[] IS NULL OR 'saved_itinerary' = ANY($5))
      AND ($6::text[] IS NULL OR 'itinerary' = ANY($6))
      AND ($7::text IS NULL OR s.title ILIKE '%%' || $7 || '%%')
      AND ($8::text IS NULL OR LOWER(c.name) = LOWER($8))
      AND ($9::timestamptz IS NULL OR s.created_at >= $9)
      AND ($10::timestamptz IS NULL OR s.created_at <= $10)
    ORDER BY s.created_at %[1]s, s.id %[1]s
    LIMIT $2
),
favourites AS (
    SELECT
        f.id,
        'favourite'::text,
        f.item_id::text,
        COALESCE(NULLIF(f.content_type, ''), 'poi')::text,
        COALESCE(f.city_name, '')::text,
        ''::text,
        f.item_name::text,
        f.added_at
    FROM user_favorites f
    WHERE f.user_id = $1
      AND ($5::text[] IS NULL OR 'favourite' = ANY($5))
      AND ($6::text[] IS NULL OR COALESCE(NULLIF(f.content_type, ''), 'poi') = ANY($6))
      AND ($7::text IS NULL OR f.item_name ILIKE '%%' || $7 || '%%')
      AND ($8::text IS NULL OR LOWER(f.city_name) = LOWER($8))
      AND ($9::timestamptz IS NULL OR f.added_at >= $9)
      AND ($10::timestamptz IS NULL OR f.added_at <= $10)
    ORDER BY f.added_at %[1]s, f.id %[1]s
    LIMIT $2
)
SELECT kind, id::text AS id, ref_id, detail, city_name, city_id, label, occurred_at
FROM (
    SELECT * FROM prompts
    UNION ALL SELECT * FROM saved
    UNION ALL SELECT * FROM favourites
) feed
-- Qualified on purpose: unqualified "id" would bind to the text alias in the
-- select list, and text ordering of a UUID is not the ordering each branch
-- used to pick its rows.
ORDER BY feed.occurred_at %[1]s, feed.id %[1]s
LIMIT $3 OFFSET $4
`

// GetUserActivityFeed returns one page of the recents feed.
//
// It deliberately returns no total. Counting a three-way union means running
// every branch to completion with no limit to short-circuit it — a full scan of
// the user's entire history on every page request, for a number that only ever
// renders as a label. Callers ask for one row more than they need instead, and
// read the extra row's existence as "there is another page".
func (r *RepositoryImpl) GetUserActivityFeed(ctx context.Context, userID uuid.UUID, limit, offset int, filter locitypes.ActivityFeedFilter) ([]locitypes.ActivityEntry, error) {
	ctx, span := otel.Tracer("RecentsRepository").Start(ctx, "GetUserActivityFeed", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.Int("limit", limit),
		attribute.Int("offset", offset),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetUserActivityFeed"))

	direction := "DESC"
	if filter.Ascending {
		direction = "ASC"
	}
	query := fmt.Sprintf(activityFeedQuery, direction)

	rows, err := r.pgpool.Query(ctx, query,
		userID,
		limit+offset, // $2: how deep each branch has to reach to cover this page
		limit,
		offset,
		nilIfEmptySlice(filter.Kinds),
		nilIfEmptySlice(filter.Details),
		nilIfEmptyString(filter.Search),
		nilIfEmptyString(filter.CityName),
		nilIfZeroTime(filter.Since),
		nilIfZeroTime(filter.Until),
	)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query activity feed", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query activity feed: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[activityFeedRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect activity feed rows", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database read failed")
		return nil, fmt.Errorf("failed to read activity feed: %w", err)
	}

	entries := make([]locitypes.ActivityEntry, 0, len(dbRows))
	for _, row := range dbRows {
		entries = append(entries, locitypes.ActivityEntry{
			Kind:       locitypes.ActivityKind(row.Kind),
			ID:         row.ID,
			RefID:      row.RefID,
			Detail:     row.Detail,
			CityName:   row.CityName,
			CityID:     row.CityID,
			Label:      row.Label,
			OccurredAt: row.OccurredAt,
		})
	}

	span.SetAttributes(attribute.Int("results.entries", len(entries)))
	span.SetStatus(codes.Ok, "Activity feed retrieved")

	return entries, nil
}

// The three helpers below turn an unset filter field into a SQL NULL, which is
// what every `$n IS NULL OR ...` test in activityFeedQuery reads as "do not
// narrow on this".

func nilIfEmptySlice(v []string) []string {
	if len(v) == 0 {
		return nil
	}
	return v
}

func nilIfEmptyString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func nilIfZeroTime(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}
