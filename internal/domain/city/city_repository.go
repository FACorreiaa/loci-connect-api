package city

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var _ Repository = (*RepositoryImpl)(nil)

type Repository interface {
	SaveCity(ctx context.Context, city locitypes.CityDetail) (uuid.UUID, error)
	FindCityByNameAndCountry(ctx context.Context, city, country string) (*locitypes.CityDetail, error)
	FindCityByFuzzyName(ctx context.Context, cityName string) (*locitypes.CityDetail, error)
	GetCityIDByName(ctx context.Context, cityName string) (uuid.UUID, error)
	GetAllCities(ctx context.Context) ([]locitypes.CityDetail, error)

	// Vector similarity search methods
	FindSimilarCities(ctx context.Context, queryEmbedding []float32, limit int) ([]locitypes.CityDetail, error)
	UpdateCityEmbedding(ctx context.Context, cityID uuid.UUID, embedding []float32) error
	GetCitiesWithoutEmbeddings(ctx context.Context, limit int) ([]locitypes.CityDetail, error)

	GetCity(ctx context.Context, lat, lon float64) (uuid.UUID, string, error)
}

type RepositoryImpl struct {
	logger *slog.Logger
	pgpool *pgxpool.Pool
}

type idRow struct {
	ID uuid.UUID `db:"id"`
}

type idNameRow struct {
	ID   uuid.UUID `db:"id"`
	Name string    `db:"name"`
}

func NewCityRepository(pgxpool *pgxpool.Pool, logger *slog.Logger) *RepositoryImpl {
	return &RepositoryImpl{
		logger: logger,
		pgpool: pgxpool,
	}
}

func (r *RepositoryImpl) SaveCity(ctx context.Context, city locitypes.CityDetail) (uuid.UUID, error) {
	// First, try to find existing city to avoid race condition
	// Normalize empty values to ensure consistent matching
	normalizedCountry := city.Country
	if normalizedCountry == "" {
		normalizedCountry = "Unknown" // Use consistent default instead of empty string
	}
	normalizedState := city.StateProvince
	if normalizedState == "" {
		normalizedState = "Unknown" // Use consistent default instead of NULL
	}

	// Check if city already exists
	existingCity, err := r.FindCityByNameAndCountry(ctx, city.Name, normalizedCountry)
	if err == nil && existingCity != nil {
		// City already exists, return its ID
		return existingCity.ID, nil
	}

	query := `
        INSERT INTO cities (
            name, country, state_province, ai_summary, center_location
        ) VALUES (
            $1, $2, $3, $4,
            CASE
                WHEN ($5::DOUBLE PRECISION IS NOT NULL AND $6::DOUBLE PRECISION IS NOT NULL)
                     AND ($5::DOUBLE PRECISION != 0.0 OR $6::DOUBLE PRECISION != 0.0)
                     AND ($5::DOUBLE PRECISION >= -180 AND $5::DOUBLE PRECISION <= 180)
                     AND ($6::DOUBLE PRECISION >= -90 AND $6::DOUBLE PRECISION <= 90)
                THEN ST_SetSRID(ST_MakePoint($5::DOUBLE PRECISION, $6::DOUBLE PRECISION), 4326)
                ELSE NULL
            END
        )
        ON CONFLICT (name, state_province, country)
        DO UPDATE SET
            ai_summary = COALESCE(EXCLUDED.ai_summary, cities.ai_summary),
            center_location = COALESCE(EXCLUDED.center_location, cities.center_location),
            updated_at = NOW()
        RETURNING id
    `
	// Use normalized values for the insert
	insertRows, err := r.pgpool.Query(ctx, query,
		city.Name,
		normalizedCountry,
		normalizedState,
		city.AiSummary,
		city.CenterLongitude, // Pass pointer directly - pgx handles nil as NULL
		city.CenterLatitude,  // Pass pointer directly - pgx handles nil as NULL
	)
	if err == nil {
		insertRow, collectErr := pgx.CollectOneRow(insertRows, pgx.RowToStructByName[idRow])
		if collectErr == nil {
			return insertRow.ID, nil
		}
		err = collectErr
	}

	if err != nil {
		// If there's still a conflict (race condition), try to find and return existing city
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			existingCity, findErr := r.FindCityByNameAndCountry(ctx, city.Name, normalizedCountry)
			if findErr == nil && existingCity != nil {
				return existingCity.ID, nil
			}
		}
		return uuid.Nil, fmt.Errorf("failed to insert city: %w", err)
	}

	return uuid.Nil, fmt.Errorf("failed to insert city: %w", err)
}

// FindCityByNameAndCountry You'll also need to update FindCityByNameAndCountry to retrieve these new fields.
func (r *RepositoryImpl) FindCityByNameAndCountry(ctx context.Context, cityName, countryName string) (*locitypes.CityDetail, error) {
	query := `
        SELECT
            id, name, country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        AND ($2 = '' OR country = $2)
    `

	rows, err := r.pgpool.Query(ctx, query, cityName, countryName)
	if err != nil {
		return nil, fmt.Errorf("failed to query city '%s', '%s': %w", cityName, countryName, err)
	}

	city, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[locitypes.CityDetail])
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find city '%s', '%s': %w", cityName, countryName, err)
	}

	return city, nil
}

// FindCityByFuzzyName finds the city with the most similar name using trigram similarity.
func (r *RepositoryImpl) FindCityByFuzzyName(ctx context.Context, cityName string) (*locitypes.CityDetail, error) {
	query := `
		SELECT
			id, name, country,
			COALESCE(state_province, '') as state_province,
			COALESCE(ai_summary, '') as ai_summary,
			ST_Y(center_location) as center_latitude,
			ST_X(center_location) as center_longitude
		FROM cities
		WHERE similarity(name, $1) > 0.3
		ORDER BY similarity(name, $1) DESC
		LIMIT 1
	`

	rows, err := r.pgpool.Query(ctx, query, cityName)
	if err != nil {
		return nil, fmt.Errorf("failed to query city by fuzzy name '%s': %w", cityName, err)
	}

	city, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[locitypes.CityDetail])
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find city by fuzzy name '%s': %w", cityName, err)
	}

	return city, nil
}

// GetCityIDByName retrieves a city ID by its name
func (r *RepositoryImpl) GetCityIDByName(ctx context.Context, cityName string) (uuid.UUID, error) {
	ctx, span := otel.Tracer("CityRepository").Start(ctx, "GetCityIDByName", trace.WithAttributes(
		attribute.String("city.name", cityName),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetCityIDByName"))

	query := `
        SELECT id
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        LIMIT 1
    `

	rows, err := r.pgpool.Query(ctx, query, cityName)
	if err != nil {
		l.ErrorContext(ctx, "Failed to get city ID by name",
			slog.Any("error", err),
			slog.String("city_name", cityName))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return uuid.Nil, fmt.Errorf("failed to get city ID by name '%s': %w", cityName, err)
	}

	cityRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[idRow])
	if err != nil {
		if err == pgx.ErrNoRows {
			l.WarnContext(ctx, "City not found", slog.String("city_name", cityName))
			span.SetStatus(codes.Error, "City not found")
			return uuid.Nil, fmt.Errorf("city not found: %s", cityName)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return uuid.Nil, fmt.Errorf("failed to get city ID by name '%s': %w", cityName, err)
	}

	l.InfoContext(ctx, "City ID retrieved successfully",
		slog.String("city_name", cityName),
		slog.String("city_id", cityRow.ID.String()))
	span.SetAttributes(
		attribute.String("city.name", cityName),
		attribute.String("city.id", cityRow.ID.String()),
	)
	span.SetStatus(codes.Ok, "City ID retrieved")

	return cityRow.ID, nil
}

// cityWithSimilarity is used for FindSimilarCities query which includes similarity_score
type cityWithSimilarity struct {
	ID              uuid.UUID `db:"id"`
	Name            string    `db:"name"`
	Country         string    `db:"country"`
	StateProvince   string    `db:"state_province"`
	AiSummary       string    `db:"ai_summary"`
	CenterLatitude  *float64  `db:"center_latitude"`
	CenterLongitude *float64  `db:"center_longitude"`
	SimilarityScore float64   `db:"similarity_score"`
}

// FindSimilarCities finds cities similar to the provided query embedding using cosine similarity
func (r *RepositoryImpl) FindSimilarCities(ctx context.Context, queryEmbedding []float32, limit int) ([]locitypes.CityDetail, error) {
	ctx, span := otel.Tracer("CityRepository").Start(ctx, "FindSimilarCities", trace.WithAttributes(
		attribute.Int("embedding.dimension", len(queryEmbedding)),
		attribute.Int("limit", limit),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "FindSimilarCities"))

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
            country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude,
            1 - (embedding <=> $1::vector) AS similarity_score
        FROM cities
        WHERE embedding IS NOT NULL
        ORDER BY embedding <=> $1::vector
        LIMIT $2
    `

	l.DebugContext(ctx, "Executing city similarity search query",
		slog.String("query", query),
		slog.Int("embedding_dim", len(queryEmbedding)),
		slog.Int("limit", limit))

	rows, err := r.pgpool.Query(ctx, query, embeddingStr, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query similar cities", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to search similar cities: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[cityWithSimilarity])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect similar city rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect similar city rows: %w", err)
	}

	cities := make([]locitypes.CityDetail, len(dbRows))
	for i, row := range dbRows {
		cities[i] = locitypes.CityDetail{
			ID:              row.ID,
			Name:            row.Name,
			Country:         row.Country,
			StateProvince:   row.StateProvince,
			AiSummary:       row.AiSummary,
			CenterLatitude:  row.CenterLatitude,
			CenterLongitude: row.CenterLongitude,
		}
	}

	l.InfoContext(ctx, "Similar cities found", slog.Int("count", len(cities)))
	span.SetAttributes(attribute.Int("results.count", len(cities)))
	span.SetStatus(codes.Ok, "Similar cities found")

	return cities, nil
}

// UpdateCityEmbedding updates the embedding vector for a specific city
func (r *RepositoryImpl) UpdateCityEmbedding(ctx context.Context, cityID uuid.UUID, embedding []float32) error {
	ctx, span := otel.Tracer("CityRepository").Start(ctx, "UpdateCityEmbedding", trace.WithAttributes(
		attribute.String("city.id", cityID.String()),
		attribute.Int("embedding.dimension", len(embedding)),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "UpdateCityEmbedding"))

	// Convert []float32 to pgvector format string
	embeddingStr := fmt.Sprintf("[%v]", strings.Join(func() []string {
		strs := make([]string, len(embedding))
		for i, v := range embedding {
			strs[i] = fmt.Sprintf("%f", v)
		}
		return strs
	}(), ","))

	query := `
        UPDATE cities
        SET embedding = $1::vector, embedding_generated_at = NOW()
        WHERE id = $2
    `

	result, err := r.pgpool.Exec(ctx, query, embeddingStr, cityID)
	if err != nil {
		l.ErrorContext(ctx, "Failed to update city embedding",
			slog.Any("error", err),
			slog.String("city_id", cityID.String()))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database update failed")
		return fmt.Errorf("failed to update city embedding: %w", err)
	}

	if result.RowsAffected() == 0 {
		err := fmt.Errorf("no city found with ID %s", cityID.String())
		l.WarnContext(ctx, "No city found for embedding update", slog.String("city_id", cityID.String()))
		span.RecordError(err)
		span.SetStatus(codes.Error, "City not found")
		return err
	}

	l.InfoContext(ctx, "City embedding updated successfully",
		slog.String("city_id", cityID.String()),
		slog.Int("embedding_dimension", len(embedding)))
	span.SetAttributes(
		attribute.String("city.id", cityID.String()),
		attribute.Int("embedding.dimension", len(embedding)),
	)
	span.SetStatus(codes.Ok, "City embedding updated")

	return nil
}

// GetCitiesWithoutEmbeddings retrieves cities that don't have embeddings generated yet
func (r *RepositoryImpl) GetCitiesWithoutEmbeddings(ctx context.Context, limit int) ([]locitypes.CityDetail, error) {
	ctx, span := otel.Tracer("CityRepository").Start(ctx, "GetCitiesWithoutEmbeddings", trace.WithAttributes(
		attribute.Int("limit", limit),
	))
	defer span.End()

	l := r.logger.With(slog.String("method", "GetCitiesWithoutEmbeddings"))

	query := `
        SELECT
            id,
            name,
            country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE embedding IS NULL
        ORDER BY created_at ASC
        LIMIT $1
    `

	rows, err := r.pgpool.Query(ctx, query, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query cities without embeddings", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query cities without embeddings: %w", err)
	}

	cities, err := pgx.CollectRows(rows, pgx.RowToStructByName[locitypes.CityDetail])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect city rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect city rows: %w", err)
	}

	l.InfoContext(ctx, "Cities without embeddings found", slog.Int("count", len(cities)))
	span.SetAttributes(attribute.Int("results.count", len(cities)))
	span.SetStatus(codes.Ok, "Cities without embeddings retrieved")

	return cities, nil
}

// GetAllCities retrieves all cities from the database with their coordinates
func (r *RepositoryImpl) GetAllCities(ctx context.Context) ([]locitypes.CityDetail, error) {
	ctx, span := otel.Tracer("CityRepository").Start(ctx, "GetAllCities")
	defer span.End()

	l := r.logger.With(slog.String("method", "GetAllCities"))

	query := `
        SELECT
            id,
            name,
            country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        ORDER BY name ASC
    `

	rows, err := r.pgpool.Query(ctx, query)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query all cities", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return nil, fmt.Errorf("failed to query all cities: %w", err)
	}

	cities, err := pgx.CollectRows(rows, pgx.RowToStructByName[locitypes.CityDetail])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect city rows", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to collect city rows: %w", err)
	}

	l.InfoContext(ctx, "All cities retrieved", slog.Int("count", len(cities)))
	span.SetAttributes(attribute.Int("results.count", len(cities)))
	span.SetStatus(codes.Ok, "All cities retrieved")

	return cities, nil
}

// determineCityID finds the city ID and name closest to the given latitude and longitude
func (r *RepositoryImpl) GetCity(ctx context.Context, lat, lon float64) (uuid.UUID, string, error) {
	// Start OpenTelemetry tracing
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "determineCityID", trace.WithAttributes(
		attribute.Float64("lat", lat),
		attribute.Float64("lon", lon),
	))
	defer span.End()

	// Log the request
	r.logger.DebugContext(ctx, "Determining city ID for coordinates",
		slog.Float64("lat", lat),
		slog.Float64("lon", lon))

	// Create a POINT geometry from longitude and latitude
	point := fmt.Sprintf("POINT(%f %f)", lon, lat)

	// SQL query to find the closest city based on center_location
	query := `
        SELECT id, name
        FROM cities
        ORDER BY ST_Distance(center_location, ST_GeomFromText($1, 4326)) ASC
        LIMIT 1
    `

	rows, err := r.pgpool.Query(ctx, query, point)
	if err != nil {
		if err == pgx.ErrNoRows {
			r.logger.WarnContext(ctx, "No city found for the given coordinates")
			span.SetStatus(codes.Error, "No city found")
			return uuid.Nil, "", fmt.Errorf("no city found for coordinates (%f, %f)", lat, lon)
		}
		r.logger.ErrorContext(ctx, "Failed to determine city ID", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return uuid.Nil, "", fmt.Errorf("failed to determine city ID: %w", err)
	}

	cityRow, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[idNameRow])
	if err != nil {
		if err == pgx.ErrNoRows {
			r.logger.WarnContext(ctx, "No city found for the given coordinates")
			span.SetStatus(codes.Error, "No city found")
			return uuid.Nil, "", fmt.Errorf("no city found for coordinates (%f, %f)", lat, lon)
		}
		r.logger.ErrorContext(ctx, "Failed to determine city ID", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Database query failed")
		return uuid.Nil, "", fmt.Errorf("failed to determine city ID: %w", err)
	}

	// Log success and set tracing attributes
	r.logger.InfoContext(ctx, "City determined",
		slog.String("city_id", cityRow.ID.String()),
		slog.String("city_name", cityRow.Name))
	span.SetAttributes(
		attribute.String("city.id", cityRow.ID.String()),
		attribute.String("city.name", cityRow.Name),
	)
	span.SetStatus(codes.Ok, "City determined")

	return cityRow.ID, cityRow.Name, nil
}
