package favorites

import (
	"context"
	"log/slog"
	"time"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository interface for favorites operations
type Repository interface {
	AddFavorite(ctx context.Context, fav *locitypes.FavoriteItem) (*locitypes.FavoriteItem, error)
	RemoveFavorite(ctx context.Context, userID, itemID uuid.UUID, contentType string) error
	GetFavorites(ctx context.Context, userID uuid.UUID, contentType string, limit, offset int) ([]locitypes.FavoriteItem, int, error)
	IsFavorited(ctx context.Context, userID, itemID uuid.UUID, contentType string) (bool, error)
	GetFavoritesCount(ctx context.Context, userID uuid.UUID, contentType string) (int, error)
}

// RepositoryImpl implements Repository
type RepositoryImpl struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

// NewRepository creates a new favorites repository
func NewRepository(db *pgxpool.Pool, logger *slog.Logger) Repository {
	return &RepositoryImpl{
		db:     db,
		logger: logger.With(slog.String("component", "favorites-repository")),
	}
}

// AddFavorite adds an item to favorites
func (r *RepositoryImpl) AddFavorite(ctx context.Context, fav *locitypes.FavoriteItem) (*locitypes.FavoriteItem, error) {
	l := r.logger.With(slog.String("method", "AddFavorite"))

	// For POIs, we first need to ensure the POI exists in poi_details
	// For now, we'll use the unified approach of storing in a general favorites table
	// that doesn't require FK constraints on dynamic POI data

	// Generate new ID
	fav.ID = uuid.New()
	fav.AddedAt = time.Now()

	// Use a general favorites table approach
	query := `
		INSERT INTO user_favorites (
			id, user_id, item_id, item_name, content_type, notes, description,
			city_name, latitude, longitude, rating, category, added_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (user_id, item_id, content_type) DO UPDATE
		SET notes = EXCLUDED.notes,
		    description = EXCLUDED.description,
		    added_at = EXCLUDED.added_at
		RETURNING id, added_at
	`

	err := r.db.QueryRow(ctx, query,
		fav.ID,
		fav.UserID,
		fav.ItemID,
		fav.ItemName,
		fav.ContentType,
		fav.Notes,
		fav.Description,
		fav.CityName,
		fav.Latitude,
		fav.Longitude,
		fav.Rating,
		fav.Category,
		fav.AddedAt,
	).Scan(&fav.ID, &fav.AddedAt)

	if err != nil {
		l.ErrorContext(ctx, "failed to add favorite", slog.Any("error", err))
		return nil, err
	}

	l.InfoContext(ctx, "added favorite",
		slog.String("user_id", fav.UserID.String()),
		slog.String("item_id", fav.ItemID),
		slog.String("content_type", fav.ContentType))

	return fav, nil
}

// RemoveFavorite removes an item from favorites
func (r *RepositoryImpl) RemoveFavorite(ctx context.Context, userID, itemID uuid.UUID, contentType string) error {
	l := r.logger.With(slog.String("method", "RemoveFavorite"))

	query := `
		DELETE FROM user_favorites 
		WHERE user_id = $1 AND item_id = $2 AND content_type = $3
	`

	result, err := r.db.Exec(ctx, query, userID, itemID.String(), contentType)
	if err != nil {
		l.ErrorContext(ctx, "failed to remove favorite", slog.Any("error", err))
		return err
	}

	l.InfoContext(ctx, "removed favorite",
		slog.String("user_id", userID.String()),
		slog.String("item_id", itemID.String()),
		slog.Int64("rows_affected", result.RowsAffected()))

	return nil
}

// GetFavorites retrieves favorites for a user
func (r *RepositoryImpl) GetFavorites(ctx context.Context, userID uuid.UUID, contentType string, limit, offset int) ([]locitypes.FavoriteItem, int, error) {
	l := r.logger.With(slog.String("method", "GetFavorites"))

	// Build query with optional content type filter
	baseQuery := `
		SELECT id, user_id, item_id, item_name, content_type, notes, description,
		       city_name, latitude, longitude, rating, category, added_at
		FROM user_favorites
		WHERE user_id = $1
	`
	countQuery := `SELECT COUNT(*) FROM user_favorites WHERE user_id = $1`

	args := []interface{}{userID}
	countArgs := []interface{}{userID}

	if contentType != "" && contentType != "unspecified" {
		baseQuery += ` AND content_type = $2`
		countQuery += ` AND content_type = $2`
		args = append(args, contentType)
		countArgs = append(countArgs, contentType)
	}

	baseQuery += ` ORDER BY added_at DESC`

	if limit > 0 {
		baseQuery += ` LIMIT $` + string(rune('0'+len(args)+1))
		args = append(args, limit)
	}
	if offset > 0 {
		baseQuery += ` OFFSET $` + string(rune('0'+len(args)+1))
		args = append(args, offset)
	}

	// Get total count
	var totalCount int
	err := r.db.QueryRow(ctx, countQuery, countArgs...).Scan(&totalCount)
	if err != nil {
		l.ErrorContext(ctx, "failed to get favorites count", slog.Any("error", err))
		return nil, 0, err
	}

	// Get favorites
	rows, err := r.db.Query(ctx, baseQuery, args...)
	if err != nil {
		l.ErrorContext(ctx, "failed to get favorites", slog.Any("error", err))
		return nil, 0, err
	}
	defer rows.Close()

	var favorites []locitypes.FavoriteItem
	for rows.Next() {
		var fav locitypes.FavoriteItem
		err := rows.Scan(
			&fav.ID,
			&fav.UserID,
			&fav.ItemID,
			&fav.ItemName,
			&fav.ContentType,
			&fav.Notes,
			&fav.Description,
			&fav.CityName,
			&fav.Latitude,
			&fav.Longitude,
			&fav.Rating,
			&fav.Category,
			&fav.AddedAt,
		)
		if err != nil {
			l.ErrorContext(ctx, "failed to scan favorite", slog.Any("error", err))
			continue
		}
		favorites = append(favorites, fav)
	}

	l.InfoContext(ctx, "retrieved favorites",
		slog.String("user_id", userID.String()),
		slog.Int("count", len(favorites)))

	return favorites, totalCount, nil
}

// IsFavorited checks if an item is favorited
func (r *RepositoryImpl) IsFavorited(ctx context.Context, userID, itemID uuid.UUID, contentType string) (bool, error) {
	l := r.logger.With(slog.String("method", "IsFavorited"))

	query := `
		SELECT EXISTS(
			SELECT 1 FROM user_favorites 
			WHERE user_id = $1 AND item_id = $2 AND content_type = $3
		)
	`

	var exists bool
	err := r.db.QueryRow(ctx, query, userID, itemID.String(), contentType).Scan(&exists)
	if err != nil {
		l.ErrorContext(ctx, "failed to check favorite", slog.Any("error", err))
		return false, err
	}

	return exists, nil
}

// GetFavoritesCount returns the count of favorites
func (r *RepositoryImpl) GetFavoritesCount(ctx context.Context, userID uuid.UUID, contentType string) (int, error) {
	l := r.logger.With(slog.String("method", "GetFavoritesCount"))

	query := `SELECT COUNT(*) FROM user_favorites WHERE user_id = $1`
	args := []interface{}{userID}

	if contentType != "" && contentType != "unspecified" {
		query += ` AND content_type = $2`
		args = append(args, contentType)
	}

	var count int
	err := r.db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		l.ErrorContext(ctx, "failed to get favorites count", slog.Any("error", err))
		return 0, err
	}

	return count, nil
}
