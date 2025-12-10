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
		list.ParentListID, list.CityID, list.ViewCount, list.SaveCount, list.CreatedAt, list.UpdatedAt,
	)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to create list", slog.Any("error", err))
		return fmt.Errorf("failed to create list: %w", err)
	}
	return nil
}

// GetList retrieves a list by its ID from the lists table
func (r *RepositoryImpl) GetList(ctx context.Context, listID uuid.UUID) (locitypes.List, error) {
	query := `
        SELECT id, user_id, name, description, image_url, is_public, is_itinerary,
               COALESCE(parent_list_id, '00000000-0000-0000-0000-000000000000') AS parent_list_id,
               city_id, view_count, save_count, created_at, updated_at
        FROM lists
        WHERE id = $1
    `
	rows, err := r.pgpool.Query(ctx, query, listID)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get list", slog.Any("error", err))
		return locitypes.List{}, fmt.Errorf("failed to get list: %w", err)
	}

	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[listRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return locitypes.List{}, fmt.Errorf("list not found: %w", err)
		}
		r.logger.ErrorContext(ctx, "Failed to read list row", slog.Any("error", err))
		return locitypes.List{}, fmt.Errorf("failed to get list: %w", err)
	}

	return mapListRow(row), nil
}

// GetSubLists retrieves all sub-lists with a given parent_list_id
func (r *RepositoryImpl) GetSubLists(ctx context.Context, parentListID uuid.UUID) ([]*locitypes.List, error) {
	query := `
        SELECT id, user_id, name, description, image_url, is_public, is_itinerary,
               COALESCE(parent_list_id, '00000000-0000-0000-0000-000000000000') AS parent_list_id,
               city_id, view_count, save_count, created_at, updated_at
        FROM lists
        WHERE parent_list_id = $1
    `
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
        SELECT list_id, item_id, content_type, position, notes,
               COALESCE(day_number, -1) AS day_number,
               COALESCE(time_slot, TIMESTAMPTZ '0001-01-01 00:00:00+00') AS time_slot,
               COALESCE(duration, -1) AS duration,
               COALESCE(source_llm_interaction_id, '00000000-0000-0000-0000-000000000000') AS source_llm_interaction_id,
               COALESCE(item_ai_description, '') AS item_ai_description,
               created_at, updated_at
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
		return fmt.Errorf("failed to add list item: %w", err)
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
		return fmt.Errorf("no list item found for list_id %s, item_id %s, and content_type %s", listID, itemID, contentType)
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
		return fmt.Errorf("no list found with ID %s", listID)
	}
	return nil
}

// UpdateList updates a list in the lists table
func (r *RepositoryImpl) UpdateList(ctx context.Context, list locitypes.List) error {
	query := `
        UPDATE lists
        SET name = $1, description = $2, image_url = $3, is_public = $4,
            city_id = $5, updated_at = $6
        WHERE id = $7
    `
	result, err := r.pgpool.Exec(ctx, query,
		list.Name, list.Description, list.ImageURL, list.IsPublic,
		list.CityID, list.UpdatedAt, list.ID,
	)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to update list", slog.Any("error", err))
		return fmt.Errorf("failed to update list: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("no list found with ID %s", list.ID)
	}
	return nil
}

// GetListItem retrieves a specific item from the list_items table using list_id, item_id, and content_type
func (r *RepositoryImpl) GetListItem(ctx context.Context, listID, itemID uuid.UUID, contentType string) (locitypes.ListItem, error) {
	query := `
        SELECT list_id, item_id, content_type, position, notes,
               COALESCE(day_number, -1) AS day_number,
               COALESCE(time_slot, TIMESTAMPTZ '0001-01-01 00:00:00+00') AS time_slot,
               COALESCE(duration, -1) AS duration,
               COALESCE(source_llm_interaction_id, '00000000-0000-0000-0000-000000000000') AS source_llm_interaction_id,
               COALESCE(item_ai_description, '') AS item_ai_description, created_at, updated_at
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
			return locitypes.ListItem{}, fmt.Errorf("list item not found: %w", err)
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
		return fmt.Errorf("no list item found for list_id %s and item_id %s", item.ListID, item.ItemID)
	}
	return nil
}

// GetUserLists retrieves all lists for a user, optionally filtered by isItinerary
func (r *RepositoryImpl) GetUserLists(ctx context.Context, userID uuid.UUID, isItinerary bool) ([]*locitypes.List, error) {
	query := `
        SELECT id, user_id, name, description, image_url, is_public, is_itinerary,
               COALESCE(parent_list_id, '00000000-0000-0000-0000-000000000000') AS parent_list_id,
               city_id, view_count, save_count, created_at, updated_at
        FROM lists
        WHERE user_id = $1 AND is_itinerary = $2
        ORDER BY created_at DESC
    `
	rows, err := r.pgpool.Query(ctx, query, userID, isItinerary)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to get user lists", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get user lists: %w", err)
	}

	dbRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[listRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to collect user lists", slog.Any("error", err))
		return nil, fmt.Errorf("failed to get user lists: %w", err)
	}

	lists := make([]*locitypes.List, 0, len(dbRows))
	for _, row := range dbRows {
		list := mapListRow(*row)
		lists = append(lists, &list)
	}

	return lists, nil
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
        SELECT list_id, item_id, content_type, position, notes,
               COALESCE(day_number, -1) AS day_number,
               COALESCE(time_slot, TIMESTAMPTZ '0001-01-01 00:00:00+00') AS time_slot,
               COALESCE(duration, -1) AS duration,
               COALESCE(source_llm_interaction_id, '00000000-0000-0000-0000-000000000000') AS source_llm_interaction_id,
               COALESCE(item_ai_description, '') AS item_ai_description,
               created_at, updated_at
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
			return locitypes.ListItem{}, fmt.Errorf("no list item found for list_id %s and item_id %s", listID, itemID)
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
		return fmt.Errorf("no list item found for list_id %s and item_id %s", listID, itemID)
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
		return fmt.Errorf("list was not saved by user")
	}
	return nil
}

// GetUserSavedLists retrieves all lists saved by a user
func (r *RepositoryImpl) GetUserSavedLists(ctx context.Context, userID uuid.UUID) ([]*locitypes.List, error) {
	query := `
		SELECT l.id, l.user_id, l.name, l.description, l.image_url, l.is_public, l.is_itinerary,
		       COALESCE(l.parent_list_id, '00000000-0000-0000-0000-000000000000') AS parent_list_id,
		       l.city_id, l.view_count, l.save_count, l.created_at, l.updated_at
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
		SELECT list_id, item_id, content_type, position, notes,
		       COALESCE(day_number, -1) AS day_number,
		       COALESCE(time_slot, TIMESTAMPTZ '0001-01-01 00:00:00+00') AS time_slot,
		       COALESCE(duration, -1) AS duration,
		       COALESCE(source_llm_interaction_id, '00000000-0000-0000-0000-000000000000') AS source_llm_interaction_id,
		       COALESCE(item_ai_description, '') AS item_ai_description,
		       created_at, updated_at
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
		SELECT DISTINCT l.id, l.user_id, l.name, l.description, l.image_url, l.is_public, l.is_itinerary,
		       COALESCE(l.parent_list_id, '00000000-0000-0000-0000-000000000000') AS parent_list_id,
		       l.city_id, l.view_count, l.save_count, l.created_at, l.updated_at
		FROM lists l
		LEFT JOIN list_items li ON l.id = li.list_id
		WHERE l.is_public = true
	`

	var args []interface{}
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
