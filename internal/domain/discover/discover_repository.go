package discover

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type Repository interface {
	// Get trending discoveries
	GetTrendingDiscoveries(ctx context.Context, limit int) ([]locitypes.TrendingDiscovery, error)

	// Get featured collections
	GetFeaturedCollections(ctx context.Context, limit int) ([]locitypes.FeaturedCollection, error)

	// Get user's recent discoveries
	GetRecentDiscoveriesByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]locitypes.ChatSession, int, error)

	// Get POIs by category
	GetPOIsByCategory(ctx context.Context, category, cityName string, limit, offset int) ([]locitypes.DiscoverResult, error)

	// Get trending searches today
	GetTrendingSearchesToday(ctx context.Context, limit int) ([]locitypes.TrendingSearch, error)

	// Track a discover search
	TrackSearch(ctx context.Context, userID uuid.UUID, query, cityName, source string, resultCount int) error
}

// PgxPool abstracts pgxpool.Pool for easier testing.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ PgxPool = (*pgxpool.Pool)(nil)

type RepositoryImpl struct {
	db     PgxPool
	logger *slog.Logger
}

const repoDefaultPageSize = 10

func NewRepositoryImpl(db PgxPool, logger *slog.Logger) *RepositoryImpl {
	return &RepositoryImpl{
		db:     db,
		logger: logger,
	}
}

// trendingDiscoveryRow is a row struct for GetTrendingDiscoveries query
type trendingDiscoveryRow struct {
	CityName    string    `db:"city_name"`
	SearchCount int       `db:"search_count"`
	LastSearch  time.Time `db:"last_search"`
}

// GetTrendingDiscoveries retrieves trending discoveries based on recent search activity
func (r *RepositoryImpl) GetTrendingDiscoveries(ctx context.Context, limit int) ([]locitypes.TrendingDiscovery, error) {
	l := r.logger.With(slog.String("repository", "GetTrendingDiscoveries"))
	l.DebugContext(ctx, "Fetching trending discoveries", slog.Int("limit", limit))

	query := `
		SELECT
			city_name,
			COUNT(*) as search_count,
			MAX(created_at) as last_search
		FROM chat_sessions
		WHERE
			created_at >= NOW() - INTERVAL '7 days'
			AND city_name IS NOT NULL
			AND city_name != ''
		GROUP BY city_name
		ORDER BY search_count DESC, last_search DESC
		LIMIT $1
	`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query trending discoveries", slog.Any("error", err))
		return nil, fmt.Errorf("failed to query trending discoveries: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[trendingDiscoveryRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect trending discoveries", slog.Any("error", err))
		return nil, fmt.Errorf("error reading trending discoveries: %w", err)
	}

	trending := make([]locitypes.TrendingDiscovery, 0, len(dbRows))
	for _, row := range dbRows {
		trending = append(trending, locitypes.TrendingDiscovery{
			CityName:    row.CityName,
			SearchCount: row.SearchCount,
			Emoji:       getCityEmoji(row.CityName),
		})
	}

	l.InfoContext(ctx, "Successfully fetched trending discoveries", slog.Int("count", len(trending)))
	return trending, nil
}

// featuredCollectionRow is a row struct for GetFeaturedCollections query
type featuredCollectionRow struct {
	Category    string    `db:"category"`
	ItemCount   int       `db:"item_count"`
	LastUpdated time.Time `db:"last_updated"`
}

// GetFeaturedCollections retrieves featured collections
func (r *RepositoryImpl) GetFeaturedCollections(ctx context.Context, limit int) ([]locitypes.FeaturedCollection, error) {
	l := r.logger.With(slog.String("repository", "GetFeaturedCollections"))
	l.DebugContext(ctx, "Fetching featured collections", slog.Int("limit", limit))

	query := `
		SELECT
			category,
			COUNT(*) as item_count,
			MAX(created_at) as last_updated
		FROM poi_detailed_info
		WHERE
			rating >= 4.0
			AND category IS NOT NULL
			AND category != ''
		GROUP BY category
		ORDER BY item_count DESC
		LIMIT $1
	`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query featured collections", slog.Any("error", err))
		return nil, fmt.Errorf("failed to query featured collections: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[featuredCollectionRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect featured collections", slog.Any("error", err))
		return nil, fmt.Errorf("error reading featured collections: %w", err)
	}

	featured := make([]locitypes.FeaturedCollection, 0, len(dbRows))
	for _, row := range dbRows {
		featured = append(featured, locitypes.FeaturedCollection{
			Category:  row.Category,
			ItemCount: row.ItemCount,
			Title:     getCategoryTitle(row.Category),
			Emoji:     getCategoryEmoji(row.Category),
		})
	}

	l.InfoContext(ctx, "Successfully fetched featured collections", slog.Int("count", len(featured)))
	return featured, nil
}

// chatSessionRow is a row struct for GetRecentDiscoveriesByUserID query
type chatSessionRow struct {
	ID                  uuid.UUID  `db:"id"`
	UserID              uuid.UUID  `db:"user_id"`
	ProfileID           *uuid.UUID `db:"profile_id"`
	CityName            string     `db:"city_name"`
	ConversationHistory []byte     `db:"conversation_history"`
	SessionContext      []byte     `db:"session_context"`
	CreatedAt           time.Time  `db:"created_at"`
	UpdatedAt           time.Time  `db:"updated_at"`
	ExpiresAt           time.Time  `db:"expires_at"`
	Status              string     `db:"status"`
}

// GetRecentDiscoveriesByUserID retrieves user's recent discover searches with pagination
func (r *RepositoryImpl) GetRecentDiscoveriesByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]locitypes.ChatSession, int, error) {
	l := r.logger.With(slog.String("repository", "GetRecentDiscoveriesByUserID"))
	l.DebugContext(ctx, "Fetching recent discoveries",
		slog.String("user_id", userID.String()),
		slog.Int("limit", limit),
		slog.Int("offset", offset))

	if limit <= 0 {
		limit = repoDefaultPageSize
	}
	if offset < 0 {
		offset = 0
	}

	// Get count
	var count int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) as count FROM chat_sessions WHERE user_id = $1`, userID).Scan(&count)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query count", slog.Any("error", err))
		return nil, 0, fmt.Errorf("failed to count recent discoveries: %w", err)
	}

	query := `
		SELECT
			id,
			user_id,
			profile_id,
			city_name,
			conversation_history,
			session_context,
			created_at,
			updated_at,
			expires_at,
			status
		FROM chat_sessions
		WHERE
			user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query recent discoveries", slog.Any("error", err))
		return nil, 0, fmt.Errorf("failed to query recent discoveries: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[chatSessionRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect chat sessions", slog.Any("error", err))
		return nil, 0, fmt.Errorf("error reading recent discoveries: %w", err)
	}

	sessions := make([]locitypes.ChatSession, 0, len(dbRows))
	for _, row := range dbRows {
		session := locitypes.ChatSession{
			ID:        row.ID,
			UserID:    row.UserID,
			CityName:  row.CityName,
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
			ExpiresAt: row.ExpiresAt,
			Status:    locitypes.SessionStatus(row.Status),
		}
		if row.ProfileID != nil {
			session.ProfileID = *row.ProfileID
		}
		// Parse conversation history JSON
		if len(row.ConversationHistory) > 0 {
			session.ConversationHistory = []locitypes.ConversationMessage{}
		}
		// Parse session context JSON
		if len(row.SessionContext) > 0 {
			session.SessionContext = locitypes.SessionContext{}
		}
		sessions = append(sessions, session)
	}

	l.InfoContext(ctx, "Successfully fetched recent discoveries",
		slog.String("user_id", userID.String()),
		slog.Int("count", len(sessions)),
		slog.Int("total", count))
	return sessions, count, nil
}

// discoverResultRow is a row struct for GetPOIsByCategory query
type discoverResultRow struct {
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Latitude    float64   `db:"latitude"`
	Longitude   float64   `db:"longitude"`
	Category    string    `db:"category"`
	Description string    `db:"description_poi"`
	Address     string    `db:"address"`
	Website     *string   `db:"website"`
	PhoneNumber *string   `db:"phone_number"`
	PriceLevel  string    `db:"price_level"`
	Rating      float64   `db:"rating"`
	Tags        []string  `db:"tags"`
}

// GetPOIsByCategory retrieves POIs by category (optionally filtered by city) with pagination.
func (r *RepositoryImpl) GetPOIsByCategory(ctx context.Context, category, cityName string, limit, offset int) ([]locitypes.DiscoverResult, error) {
	l := r.logger.With(slog.String("repository", "GetPOIsByCategory"))
	l.DebugContext(ctx, "Fetching POIs by category", slog.String("category", category), slog.String("city_name", cityName), slog.Int("limit", limit), slog.Int("offset", offset))

	baseQuery := `
		SELECT
			id,
			name,
			latitude,
			longitude,
			category,
			description_poi,
			address,
			website,
			phone_number,
			price_level,
			rating,
			tags
		FROM poi_detailed_info
		WHERE
			LOWER(category) = LOWER($1)
			AND rating >= 3.5
	`

	args := []any{category}
	placeholder := 2
	if cityName != "" {
		baseQuery += `
			AND LOWER(city_name) = LOWER($2)
		`
		args = append(args, cityName)
		placeholder = 3
	}

	baseQuery += fmt.Sprintf(`
		ORDER BY rating DESC, name ASC
		LIMIT $%d OFFSET $%d
	`, placeholder, placeholder+1)
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, baseQuery, args...)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query POIs by category", slog.Any("error", err))
		return nil, fmt.Errorf("failed to query POIs by category: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[discoverResultRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect POIs", slog.Any("error", err))
		return nil, fmt.Errorf("error reading POIs: %w", err)
	}

	results := make([]locitypes.DiscoverResult, 0, len(dbRows))
	for _, row := range dbRows {
		results = append(results, locitypes.DiscoverResult{
			ID:          row.ID.String(),
			Name:        row.Name,
			Latitude:    row.Latitude,
			Longitude:   row.Longitude,
			Category:    row.Category,
			Description: row.Description,
			Address:     row.Address,
			Website:     row.Website,
			PhoneNumber: row.PhoneNumber,
			PriceLevel:  row.PriceLevel,
			Rating:      row.Rating,
			Tags:        row.Tags,
		})
	}

	l.InfoContext(ctx, "Successfully fetched POIs by category",
		slog.String("category", category),
		slog.Int("count", len(results)))
	return results, nil
}

// Helper functions

func getCityEmoji(cityName string) string {
	cityEmojiMap := map[string]string{
		"lisbon":        "🇵🇹",
		"porto":         "🇵🇹",
		"paris":         "🇫🇷",
		"london":        "🇬🇧",
		"tokyo":         "🇯🇵",
		"new york":      "🗽",
		"barcelona":     "🇪🇸",
		"amsterdam":     "🇳🇱",
		"rome":          "🇮🇹",
		"berlin":        "🇩🇪",
		"singapore":     "🇸🇬",
		"dubai":         "🇦🇪",
		"sydney":        "🇦🇺",
		"san francisco": "🌉",
	}

	if emoji, ok := cityEmojiMap[cityName]; ok {
		return emoji
	}
	return "🌍" // Default globe emoji
}

func getCategoryTitle(category string) string {
	titleMap := map[string]string{
		"restaurant": "Top Restaurants",
		"hotel":      "Best Hotels",
		"activity":   "Popular Activities",
		"attraction": "Must-See Attractions",
		"museum":     "Museums & Galleries",
		"park":       "Parks & Gardens",
		"beach":      "Beautiful Beaches",
		"nightlife":  "Nightlife Spots",
		"shopping":   "Shopping Destinations",
		"cultural":   "Cultural Experiences",
		"market":     "Local Markets",
		"adventure":  "Adventure Activities",
	}

	if title, ok := titleMap[category]; ok {
		return title
	}
	return fmt.Sprintf("Best %ss", category)
}

func getCategoryEmoji(category string) string {
	emojiMap := map[string]string{
		"restaurant":    "🍽️",
		"hotel":         "🏨",
		"activity":      "🎯",
		"attraction":    "🏛️",
		"museum":        "🎨",
		"park":          "🌳",
		"beach":         "🏖️",
		"nightlife":     "🌃",
		"shopping":      "🛍️",
		"cultural":      "🎭",
		"market":        "🏪",
		"adventure":     "⛰️",
		"cafe":          "☕",
		"bar":           "🍺",
		"entertainment": "🎪",
	}

	if emoji, ok := emojiMap[category]; ok {
		return emoji
	}
	return "📍"
}

// trendingSearchRow is a row struct for GetTrendingSearchesToday query
type trendingSearchRow struct {
	Query        string    `db:"query"`
	CityName     string    `db:"city_name"`
	SearchCount  int       `db:"search_count"`
	LastSearched time.Time `db:"last_searched"`
}

func (r *RepositoryImpl) GetTrendingSearchesToday(ctx context.Context, limit int) ([]locitypes.TrendingSearch, error) {
	l := r.logger.With(slog.String("repository", "GetTrendingSearchesToday"))
	l.DebugContext(ctx, "Fetching trending searches today", slog.Int("limit", limit))

	query := `
		SELECT
			query,
			city_name,
			COUNT(*) as search_count,
			MAX(created_at) as last_searched
		FROM discover_searches
		WHERE
			created_at >= NOW() - INTERVAL '24 hours'
			AND query IS NOT NULL
			AND query != ''
			AND city_name IS NOT NULL
			AND city_name != ''
		GROUP BY query, city_name
		ORDER BY search_count DESC, last_searched DESC
		LIMIT $1
	`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to query trending searches", slog.Any("error", err))
		return nil, fmt.Errorf("failed to query trending searches: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[trendingSearchRow])
	if err != nil {
		l.ErrorContext(ctx, "Failed to collect trending searches", slog.Any("error", err))
		return nil, fmt.Errorf("error reading trending searches: %w", err)
	}

	searches := make([]locitypes.TrendingSearch, 0, len(dbRows))
	for _, row := range dbRows {
		searches = append(searches, locitypes.TrendingSearch{
			Query:        row.Query,
			CityName:     row.CityName,
			SearchCount:  row.SearchCount,
			LastSearched: formatTimeAgo(row.LastSearched),
		})
	}

	l.InfoContext(ctx, "Successfully fetched trending searches", slog.Int("count", len(searches)))
	return searches, nil
}

// TrackSearch records a discover search for trending analysis
func (r *RepositoryImpl) TrackSearch(ctx context.Context, userID uuid.UUID, query, cityName, source string, resultCount int) error {
	l := r.logger.With(slog.String("repository", "TrackSearch"))

	insertQuery := `
		INSERT INTO discover_searches (
			user_id,
			query,
			city_name,
			search_type,
			result_count,
			source,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`

	// Use NULL for anonymous users
	var userIDParam interface{}
	if userID == uuid.Nil {
		userIDParam = nil
	} else {
		userIDParam = userID
	}

	_, err := r.db.Exec(ctx, insertQuery, userIDParam, query, cityName, "discover", resultCount, source)
	if err != nil {
		l.ErrorContext(ctx, "Failed to track search",
			slog.Any("error", err),
			slog.String("query", query),
			slog.String("city", cityName))
		// Don't fail the request if tracking fails
		return nil
	}

	l.DebugContext(ctx, "Search tracked successfully",
		slog.String("query", query),
		slog.String("city", cityName),
		slog.String("source", source),
		slog.Int("result_count", resultCount))

	return nil
}

// formatTimeAgo formats a time as a human-readable "time ago" string
func formatTimeAgo(t time.Time) string {
	duration := time.Since(t)

	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	case duration < 24*time.Hour:
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case duration < 7*24*time.Hour:
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2")
	}
}
