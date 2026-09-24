package itinerarylist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ensure RepositoryImpl implements the Repository interface
var _ Repository = (*RepositoryImpl)(nil)

// PgxPool abstracts pgxpool.Pool for testing.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

var _ PgxPool = (*pgxpool.Pool)(nil)

// listColumns is the projection every lists read uses, so listRow always
// matches. description and image_url are nullable TEXT, and city_id is NULL
// for a list with no city; all three read back as zero values.
const listColumns = `l.id, l.user_id, l.name,
               COALESCE(l.description, '') AS description,
               COALESCE(l.image_url, '') AS image_url,
               l.is_public, l.is_itinerary,
               COALESCE(l.parent_list_id, '00000000-0000-0000-0000-000000000000') AS parent_list_id,
               COALESCE(l.city_id, '00000000-0000-0000-0000-000000000000') AS city_id,
               l.item_count, l.view_count, l.save_count, l.created_at, l.updated_at`

// listItemColumns is the projection every list_items read uses.
const listItemColumns = `list_id, item_id, content_type, position,
               COALESCE(notes, '') AS notes,
               COALESCE(day_number, -1) AS day_number,
               COALESCE(time_slot, TIMESTAMPTZ '0001-01-01 00:00:00+00') AS time_slot,
               COALESCE(duration, -1) AS duration,
               COALESCE(source_llm_interaction_id, '00000000-0000-0000-0000-000000000000') AS source_llm_interaction_id,
               COALESCE(item_ai_description, '') AS item_ai_description,
               created_at, updated_at`

// nullableUUID maps uuid.Nil to NULL, for the foreign-key columns a zero id
// would violate.
func nullableUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// errListNotFound is what every read or write of a missing list returns.
func errListNotFound(listID uuid.UUID) error {
	return fmt.Errorf("list %s: %w", listID, locitypes.ErrNotFound)
}

// errListItemNotFound is what a read or delete of a missing item returns.
func errListItemNotFound(listID, itemID uuid.UUID) error {
	return fmt.Errorf("item %s in list %s: %w", itemID, listID, locitypes.ErrNotFound)
}

// classifyListItemWriteError maps the constraint failures an insert can hit to
// the domain errors the handler turns into Connect codes: a duplicate item is
// AlreadyExists, and validate_list_item_content_type's RAISE (the place does
// not exist) is InvalidArgument.
func classifyListItemWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("item is already in this list: %w", locitypes.ErrConflict)
		case "P0001", "23503":
			return fmt.Errorf("%s: %w", pgErr.Message, locitypes.ErrBadRequest)
		}
	}
	return err
}

type listRow struct {
	ID          uuid.UUID `db:"id"`
	UserID      uuid.UUID `db:"user_id"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	ImageURL    string    `db:"image_url"`
	IsPublic    bool      `db:"is_public"`
	IsItinerary bool      `db:"is_itinerary"`
	ParentList  uuid.UUID `db:"parent_list_id"`
	CityID      uuid.UUID `db:"city_id"`
	ItemCount   int       `db:"item_count"`
	ViewCount   int       `db:"view_count"`
	SaveCount   int       `db:"save_count"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

type listItemRow struct {
	ListID               uuid.UUID             `db:"list_id"`
	ItemID               uuid.UUID             `db:"item_id"`
	ContentType          locitypes.ContentType `db:"content_type"`
	Position             int                   `db:"position"`
	Notes                string                `db:"notes"`
	DayNumber            int32                 `db:"day_number"`
	TimeSlot             time.Time             `db:"time_slot"`
	Duration             int32                 `db:"duration"`
	SourceLlmInteraction uuid.UUID             `db:"source_llm_interaction_id"`
	ItemAIDescription    string                `db:"item_ai_description"`
	CreatedAt            time.Time             `db:"created_at"`
	UpdatedAt            time.Time             `db:"updated_at"`
}

// RepositoryImpl struct holds the logger and database connection pool
type RepositoryImpl struct {
	logger *slog.Logger
	pgpool PgxPool
}

// Repository defines the interface for list and list item operations
type Repository interface {
	CreateList(ctx context.Context, list locitypes.List) error
	GetList(ctx context.Context, listID uuid.UUID) (locitypes.List, error)
	UpdateList(ctx context.Context, list locitypes.List) error
	GetSubLists(ctx context.Context, parentListID uuid.UUID) ([]*locitypes.List, error)
	GetListItems(ctx context.Context, listID uuid.UUID) ([]*locitypes.ListItem, error)

	// Generic list item methods (support all content types)
	GetListItemByID(ctx context.Context, listID, itemID uuid.UUID) (locitypes.ListItem, error)
	DeleteListItemByID(ctx context.Context, listID, itemID uuid.UUID) error

	// Saved Lists functionality
	SaveList(ctx context.Context, userID, listID uuid.UUID) error
	UnsaveList(ctx context.Context, userID, listID uuid.UUID) error
	GetUserSavedLists(ctx context.Context, userID uuid.UUID) ([]*locitypes.List, error)

	// Content type specific methods
	GetListItemsByContentType(ctx context.Context, listID uuid.UUID, contentType locitypes.ContentType) ([]*locitypes.ListItem, error)

	// Search and filtering
	SearchLists(ctx context.Context, searchTerm, category, contentType, theme string, cityID *uuid.UUID) ([]*locitypes.List, error)

	// Legacy POI-specific methods (for backward compatibility)
	GetListItem(ctx context.Context, listID, itemID uuid.UUID, contentType string) (locitypes.ListItem, error)
	AddListItem(ctx context.Context, item locitypes.ListItem) error
	UpdateListItem(ctx context.Context, item locitypes.ListItem) error
	DeleteListItem(ctx context.Context, listID, itemID uuid.UUID, contentType string) error
	DeleteList(ctx context.Context, listID uuid.UUID) error
	GetUserLists(ctx context.Context, userID uuid.UUID, isItinerary bool) ([]*locitypes.List, error)
	// GetAllUserLists returns every list the user owns, both kinds, newest first.
	GetAllUserLists(ctx context.Context, userID uuid.UUID) ([]*locitypes.List, error)
	// GetPlaceSummaries reads the stored place behind each item id, keyed by id.
	// Ids with no stored place are absent from the map.
	GetPlaceSummaries(ctx context.Context, itemIDs []uuid.UUID) (map[uuid.UUID]locitypes.POIDetailedInfo, error)
	CountUserLists(ctx context.Context, userID uuid.UUID) (int, error)
	CountUserListItems(ctx context.Context, userID uuid.UUID) (int, error)
}

func NewRepository(pgpool PgxPool, logger *slog.Logger) *RepositoryImpl {
	return &RepositoryImpl{
		logger: logger,
		pgpool: pgpool,
	}
}

// CreateList inserts a new list into the lists table
func (r *RepositoryImpl) CreateList(ctx context.Context, list locitypes.List) error {
	query := `
        INSERT INTO lists (
            id, user_id, name, description, image_url, is_public, is_itinerary,
            parent_list_id, city_id, view_count, save_count, created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
        )
    `
	_, err := r.pgpool.Exec(ctx, query,
		list.ID, list.UserID, list.Name, list.Description, list.ImageURL, list.IsPublic, list.IsItinerary,
		list.ParentListID, nullableUUID(list.CityID), list.ViewCount, list.SaveCount, list.CreatedAt, list.UpdatedAt,
	)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to create list", slog.Any("error", err))
		return fmt.Errorf("failed to create list: %w", err)
	}
	return nil
}

// GetList retrieves a list by its ID from the lists table
func (r *RepositoryImpl) GetList(ctx context.Context, listID uuid.UUID) (locitypes.List, error) {
	query := `SELECT ` + listColumns + ` FROM lists l WHERE l.id = $1`
	rows, err := r.pgpool.Query(ctx, query, listID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get list", slog.Any("error", err))
		return locitypes.List{}, fmt.Errorf("failed to get list: %w", err)
	}

	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[listRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return locitypes.List{}, errListNotFound(listID)
		}
		r.logger.ErrorContext(ctx, "Failed to read list row", slog.Any("error", err))
		return locitypes.List{}, fmt.Errorf("failed to get list: %w", err)
	}

	return mapListRow(row), nil
}

// GetSubLists retrieves all sub-lists with a given parent_list_id
func (r *RepositoryImpl) GetSubLists(ctx context.Context, parentListID uuid.UUID) ([]*locitypes.List, error) {
	query := `SELECT ` + listColumns + ` FROM lists l WHERE l.parent_list_id = $1`
	rows, err := r.pgpool.Query(ctx, query, parentListID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get sub-lists", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get sub-lists: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[listRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to collect sub-list rows", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get sub-lists: %w", err)
	}

	subLists := make([]*locitypes.List, 0, len(dbRows))
	for _, row := range dbRows {
		list := mapListRow(*row)
		subLists = append(subLists, &list)
	}

	return subLists, nil
}

// GetListItems retrieves all items associated with a specific list, ordered by position
func (r *RepositoryImpl) GetListItems(ctx context.Context, listID uuid.UUID) ([]*locitypes.ListItem, error) {
	query := `
        SELECT ` + listItemColumns + `
        FROM list_items
        WHERE list_id = $1
        ORDER BY position
    `
	rows, err := r.pgpool.Query(ctx, query, listID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get list items", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get list items: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[listItemRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to collect list item rows", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get list items: %w", err)
	}

	items := make([]*locitypes.ListItem, 0, len(dbRows))
	for _, row := range dbRows {
		item := mapListItemRow(row)
		items = append(items, &item)
	}

	return items, nil
}

// AddListItem inserts a new item into the list_items table
func (r *RepositoryImpl) AddListItem(ctx context.Context, item locitypes.ListItem) error {
	var poiID *uuid.UUID
	// Only set poi_id for POI content type to avoid foreign key constraint violations
	if item.ContentType == locitypes.ContentTypePOI {
		poiID = &item.ItemID
	}

	query := `
        INSERT INTO list_items (list_id, item_id, content_type, position, notes, day_number, time_slot,
            duration, source_llm_interaction_id, item_ai_description, created_at, updated_at, poi_id)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
    `
	_, err := r.pgpool.Exec(ctx, query,
		item.ListID, item.ItemID, item.ContentType, item.Position, item.Notes,
		item.DayNumber, item.TimeSlot, item.Duration, item.SourceLlmInteractionID,
		item.ItemAIDescription, item.CreatedAt, item.UpdatedAt, poiID,
	)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to add list item", slog.Any("error", err))
		return fmt.Errorf("failed to add list item: %w", classifyListItemWriteError(err))
	}
	return nil
}

// DeleteListItem deletes a specific item from the list_items table using list_id, item_id, and content_type
func (r *RepositoryImpl) DeleteListItem(ctx context.Context, listID, itemID uuid.UUID, contentType string) error {
	query := `DELETE FROM list_items WHERE list_id = $1 AND item_id = $2 AND content_type = $3`
	result, err := r.pgpool.Exec(ctx, query, listID, itemID, contentType)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to delete list item", slog.Any("error", err))
		return fmt.Errorf("failed to delete list item: %w", err)
	}
	if result.RowsAffected() == 0 {
		return errListItemNotFound(listID, itemID)
	}
	return nil
}

// DeleteList deletes a list by its ID from the lists table
func (r *RepositoryImpl) DeleteList(ctx context.Context, listID uuid.UUID) error {
	query := `DELETE FROM lists WHERE id = $1`
	result, err := r.pgpool.Exec(ctx, query, listID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to delete list", slog.Any("error", err))
		return fmt.Errorf("failed to delete list: %w", err)
	}
	if result.RowsAffected() == 0 {
		return errListNotFound(listID)
	}
	return nil
}

// UpdateList updates a list in the lists table
func (r *RepositoryImpl) UpdateList(ctx context.Context, list locitypes.List) error {
	query := `
        UPDATE lists
        SET name = $1, description = $2, image_url = $3, is_public = $4,
            city_id = $5, updated_at = $6, is_itinerary = $7
        WHERE id = $8
    `
	result, err := r.pgpool.Exec(ctx, query,
		list.Name, list.Description, list.ImageURL, list.IsPublic,
		nullableUUID(list.CityID), list.UpdatedAt, list.IsItinerary, list.ID,
	)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to update list", slog.Any("error", err))
		return fmt.Errorf("failed to update list: %w", err)
	}
	if result.RowsAffected() == 0 {
		return errListNotFound(list.ID)
	}
	return nil
}

// GetListItem retrieves a specific item from the list_items table using list_id, item_id, and content_type
func (r *RepositoryImpl) GetListItem(ctx context.Context, listID, itemID uuid.UUID, contentType string) (locitypes.ListItem, error) {
	query := `
        SELECT ` + listItemColumns + `
        FROM list_items
        WHERE list_id = $1 AND item_id = $2 AND content_type = $3
    `
	rows, err := r.pgpool.Query(ctx, query, listID, itemID, contentType)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get list item", slog.Any("error", err))
		return locitypes.ListItem{}, fmt.Errorf("failed to get list item: %w", err)
	}

	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[listItemRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return locitypes.ListItem{}, errListItemNotFound(listID, itemID)
		}
		r.logger.ErrorContext(ctx, "Failed to collect list item row", slog.Any("error", err))
		return locitypes.ListItem{}, fmt.Errorf("failed to get list item: %w", err)
	}

	return mapListItemRow(row), nil
}

// UpdateListItem updates an item in the list_items table (supports new generic structure)
func (r *RepositoryImpl) UpdateListItem(ctx context.Context, item locitypes.ListItem) error {
	query := `
        UPDATE list_items
        SET item_id = $1, content_type = $2, position = $3, notes = $4, day_number = $5,
            time_slot = $6, duration = $7, source_llm_interaction_id = $8,
            item_ai_description = $9, updated_at = $10
        WHERE list_id = $11 AND item_id = $12
    `
	result, err := r.pgpool.Exec(ctx, query,
		item.ItemID, item.ContentType, item.Position, item.Notes, item.DayNumber,
		item.TimeSlot, item.Duration, item.SourceLlmInteractionID, item.ItemAIDescription,
		item.UpdatedAt, item.ListID, item.ItemID,
	)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to update list item", slog.Any("error", err))
		return fmt.Errorf("failed to update list item: %w", err)
	}
	if result.RowsAffected() == 0 {
		return errListItemNotFound(item.ListID, item.ItemID)
	}
	return nil
}

// GetUserLists retrieves all lists for a user, optionally filtered by isItinerary
func (r *RepositoryImpl) GetUserLists(ctx context.Context, userID uuid.UUID, isItinerary bool) ([]*locitypes.List, error) {
	query := `SELECT ` + listColumns + ` FROM lists l
        WHERE l.user_id = $1 AND l.is_itinerary = $2
        ORDER BY l.created_at DESC`
	return r.queryLists(ctx, "get user lists", query, userID, isItinerary)
}

// GetAllUserLists retrieves every list the user owns, custom lists and
// itineraries alike, newest first.
func (r *RepositoryImpl) GetAllUserLists(ctx context.Context, userID uuid.UUID) ([]*locitypes.List, error) {
	query := `SELECT ` + listColumns + ` FROM lists l
        WHERE l.user_id = $1
        ORDER BY l.created_at DESC, l.id`
	return r.queryLists(ctx, "get all user lists", query, userID)
}

func (r *RepositoryImpl) queryLists(ctx context.Context, op, query string, args ...any) ([]*locitypes.List, error) {
	rows, err := r.pgpool.Query(ctx, query, args...)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to "+op, slog.Any("error", err))
		return nil, fmt.Errorf("failed to %s: %w", op, err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[listRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to collect rows for "+op, slog.Any("error", err))
		return nil, fmt.Errorf("failed to %s: %w", op, err)
	}

	lists := make([]*locitypes.List, 0, len(dbRows))
	for _, row := range dbRows {
		list := mapListRow(*row)
		lists = append(lists, &list)
	}
	return lists, nil
}

type placeSummaryRow struct {
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Latitude    float64   `db:"latitude"`
	Longitude   float64   `db:"longitude"`
	Category    string    `db:"category"`
	Description string    `db:"description"`
	Address     string    `db:"address"`
	Website     string    `db:"website"`
	Phone       string    `db:"phone_number"`
	Rating      float64   `db:"rating"`
}

// GetPlaceSummaries reads the stored place behind each list item. Items point
// at points_of_interest (what every client saves today); older restaurant and
// hotel items point at llm_suggested_pois, so that is the fallback.
func (r *RepositoryImpl) GetPlaceSummaries(ctx context.Context, itemIDs []uuid.UUID) (map[uuid.UUID]locitypes.POIDetailedInfo, error) {
	out := make(map[uuid.UUID]locitypes.POIDetailedInfo, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	const query = `
        SELECT p.id, p.name,
               ST_Y(p.location) AS latitude, ST_X(p.location) AS longitude,
               COALESCE(NULLIF(p.category, ''), p.poi_type, '') AS category,
               COALESCE(NULLIF(p.description, ''), p.ai_summary, '') AS description,
               COALESCE(p.address, '') AS address,
               COALESCE(p.website, '') AS website,
               COALESCE(p.phone_number, '') AS phone_number,
               COALESCE(p.average_rating, 0)::float8 AS rating
        FROM points_of_interest p
        WHERE p.id = ANY($1)
        UNION ALL
        SELECT s.id, s.name,
               COALESCE(s.latitude, ST_Y(s.location)) AS latitude,
               COALESCE(s.longitude, ST_X(s.location)) AS longitude,
               COALESCE(s.category, '') AS category,
               COALESCE(s.description, '') AS description,
               COALESCE(s.address, '') AS address,
               COALESCE(s.website, '') AS website,
               COALESCE(s.phone_number, '') AS phone_number,
               COALESCE(s.rating, 0)::float8 AS rating
        FROM llm_suggested_pois s
        WHERE s.id = ANY($1)
          AND NOT EXISTS (SELECT 1 FROM points_of_interest p WHERE p.id = s.id)
    `
	rows, err := r.pgpool.Query(ctx, query, itemIDs)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get place summaries", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get place summaries: %w", err)
	}
	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[placeSummaryRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to collect place summaries", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get place summaries: %w", err)
	}
	for _, row := range dbRows {
		out[row.ID] = locitypes.POIDetailedInfo{
			ID:          row.ID,
			Name:        row.Name,
			Latitude:    row.Latitude,
			Longitude:   row.Longitude,
			Category:    row.Category,
			Description: row.Description,
			Address:     row.Address,
			Website:     row.Website,
			PhoneNumber: row.Phone,
			Rating:      row.Rating,
		}
	}
	return out, nil
}

// CountUserLists returns how many lists + itinerary-lists the user owns (matches enforceListLimit).
func (r *RepositoryImpl) CountUserLists(ctx context.Context, userID uuid.UUID) (int, error) {
	const q = `SELECT COUNT(*)::int FROM lists WHERE user_id = $1`
	var n int
	rows, err := r.pgpool.Query(ctx, q, userID)
	if err != nil {
		return 0, fmt.Errorf("count user lists: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, rows.Err()
	}
	if err := rows.Scan(&n); err != nil {
		return 0, fmt.Errorf("scan count lists: %w", err)
	}
	return n, nil
}

// CountUserListItems returns how many items the user has across all owned lists.
func (r *RepositoryImpl) CountUserListItems(ctx context.Context, userID uuid.UUID) (int, error) {
	const q = `
		SELECT COUNT(*)::int
		FROM list_items li
		INNER JOIN lists l ON l.id = li.list_id
		WHERE l.user_id = $1`
	var n int
	rows, err := r.pgpool.Query(ctx, q, userID)
	if err != nil {
		return 0, fmt.Errorf("count user list items: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, rows.Err()
	}
	if err := rows.Scan(&n); err != nil {
		return 0, fmt.Errorf("scan count: %w", err)
	}
	return n, nil
}

func mapListRow(row listRow) locitypes.List {
	list := locitypes.List{
		ID:          row.ID,
		UserID:      row.UserID,
		Name:        row.Name,
		Description: row.Description,
		ImageURL:    row.ImageURL,
		IsPublic:    row.IsPublic,
		IsItinerary: row.IsItinerary,
		CityID:      row.CityID,
		ItemCount:   row.ItemCount,
		ViewCount:   row.ViewCount,
		SaveCount:   row.SaveCount,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}

	if row.ParentList != uuid.Nil {
		list.ParentListID = &row.ParentList
	}

	return list
}

func mapListItemRow(row listItemRow) locitypes.ListItem {
	item := locitypes.ListItem{
		ListID:      row.ListID,
		ItemID:      row.ItemID,
		ContentType: row.ContentType,
		Position:    row.Position,
		Notes:       row.Notes,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.ContentType == locitypes.ContentTypePOI {
		item.PoiID = row.ItemID
	}

	if row.DayNumber >= 0 {
		v := int(row.DayNumber)
		item.DayNumber = &v
	}
	if !row.TimeSlot.IsZero() {
		item.TimeSlot = &row.TimeSlot
	}
	if row.Duration >= 0 {
		v := int(row.Duration)
		item.Duration = &v
	}
	if row.SourceLlmInteraction != uuid.Nil {
		item.SourceLlmInteractionID = &row.SourceLlmInteraction
	}

	item.ItemAIDescription = row.ItemAIDescription

	return item
}

// Generic list item methods (support all content types)

// GetListItemByID retrieves a specific item from a list using generic item_id
func (r *RepositoryImpl) GetListItemByID(ctx context.Context, listID, itemID uuid.UUID) (locitypes.ListItem, error) {
	query := `
        SELECT ` + listItemColumns + `
        FROM list_items
        WHERE list_id = $1 AND item_id = $2
    `
	rows, err := r.pgpool.Query(ctx, query, listID, itemID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get list item by ID", slog.Any("error", err))
		return locitypes.ListItem{}, fmt.Errorf("failed to get list item: %w", err)
	}

	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[listItemRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return locitypes.ListItem{}, errListItemNotFound(listID, itemID)
		}
		r.logger.ErrorContext(ctx, "Failed to collect list item row", slog.Any("error", err))
		return locitypes.ListItem{}, fmt.Errorf("failed to get list item: %w", err)
	}

	return mapListItemRow(row), nil
}

// DeleteListItemByID deletes a specific item from a list using generic item_id
func (r *RepositoryImpl) DeleteListItemByID(ctx context.Context, listID, itemID uuid.UUID) error {
	query := `DELETE FROM list_items WHERE list_id = $1 AND item_id = $2`
	result, err := r.pgpool.Exec(ctx, query, listID, itemID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to delete list item by ID", slog.Any("error", err))
		return fmt.Errorf("failed to delete list item: %w", err)
	}
	if result.RowsAffected() == 0 {
		return errListItemNotFound(listID, itemID)
	}
	return nil
}

// SaveList saves a list for a user (adds to saved_lists table)
func (r *RepositoryImpl) SaveList(ctx context.Context, userID, listID uuid.UUID) error {
	query := `
		INSERT INTO saved_lists (user_id, list_id, saved_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, list_id) DO NOTHING
	`
	_, err := r.pgpool.Exec(ctx, query, userID, listID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to save list", slog.Any("error", err))
		return fmt.Errorf("failed to save list: %w", err)
	}
	return nil
}

// UnsaveList removes a saved list for a user
func (r *RepositoryImpl) UnsaveList(ctx context.Context, userID, listID uuid.UUID) error {
	query := `DELETE FROM saved_lists WHERE user_id = $1 AND list_id = $2`
	result, err := r.pgpool.Exec(ctx, query, userID, listID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to unsave list", slog.Any("error", err))
		return fmt.Errorf("failed to unsave list: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("list %s was not saved by user: %w", listID, locitypes.ErrNotFound)
	}
	return nil
}

// GetUserSavedLists retrieves all lists saved by a user
func (r *RepositoryImpl) GetUserSavedLists(ctx context.Context, userID uuid.UUID) ([]*locitypes.List, error) {
	query := `
		SELECT ` + listColumns + `
		FROM lists l
		INNER JOIN saved_lists sl ON l.id = sl.list_id
		WHERE sl.user_id = $1
		ORDER BY sl.saved_at DESC
	`
	rows, err := r.pgpool.Query(ctx, query, userID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get user saved lists", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get user saved lists: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[listRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to collect saved lists", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get user saved lists: %w", err)
	}

	lists := make([]*locitypes.List, 0, len(dbRows))
	for _, row := range dbRows {
		list := mapListRow(*row)
		lists = append(lists, &list)
	}

	return lists, nil
}

// GetListItemsByContentType retrieves all items of a specific content type from a list
func (r *RepositoryImpl) GetListItemsByContentType(ctx context.Context, listID uuid.UUID, contentType locitypes.ContentType) ([]*locitypes.ListItem, error) {
	query := `
		SELECT ` + listItemColumns + `
		FROM list_items
		WHERE list_id = $1 AND content_type = $2
		ORDER BY position
	`
	rows, err := r.pgpool.Query(ctx, query, listID, contentType)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get list items by content type", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get list items by content type: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[listItemRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to collect list items", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get list items by content type: %w", err)
	}

	items := make([]*locitypes.ListItem, 0, len(dbRows))
	for _, row := range dbRows {
		item := mapListItemRow(row)
		items = append(items, &item)
	}

	return items, nil
}

// SearchLists searches for lists based on various criteria
func (r *RepositoryImpl) SearchLists(ctx context.Context, searchTerm, category, contentType, theme string, cityID *uuid.UUID) ([]*locitypes.List, error) {
	query := `
		SELECT DISTINCT ` + listColumns + `
		FROM lists l
		LEFT JOIN list_items li ON l.id = li.list_id
		WHERE l.is_public = true
	`

	var args []any
	argIndex := 1
	_ = category
	_ = theme

	if searchTerm != "" {
		query += fmt.Sprintf(" AND (l.name ILIKE $%d OR l.description ILIKE $%d)", argIndex, argIndex+1)
		args = append(args, "%"+searchTerm+"%", "%"+searchTerm+"%")
		argIndex += 2
	}

	if cityID != nil {
		query += fmt.Sprintf(" AND l.city_id = $%d", argIndex)
		args = append(args, *cityID)
		argIndex++
	}

	if contentType != "" {
		query += fmt.Sprintf(" AND li.content_type = $%d", argIndex)
		args = append(args, contentType)
	}

	query += " ORDER BY l.save_count DESC, l.created_at DESC"

	rows, err := r.pgpool.Query(ctx, query, args...)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to search lists", slog.Any("error", err))
		return nil, fmt.Errorf("failed to search lists: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[listRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to collect search results", slog.Any("error", err))
		return nil, fmt.Errorf("failed to search lists: %w", err)
	}

	lists := make([]*locitypes.List, 0, len(dbRows))
	for _, row := range dbRows {
		list := mapListRow(*row)
		lists = append(lists, &list)
	}

	return lists, nil
}
