package trip

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxChecklistItems caps a trip's checklist (packing items and expenses
// together). The same cap bounds dismissed suggestions, so neither table can be
// grown without limit by one trip.
const MaxChecklistItems = 500

// ErrChecklistFull is returned when adding an item (or a dismissal) would take a
// trip past MaxChecklistItems. Replacing an existing item never hits it.
var ErrChecklistFull = fmt.Errorf("trip checklist is limited to %d items", MaxChecklistItems)

// ChecklistKind mirrors the proto ChecklistItemKind; persisted as SMALLINT.
type ChecklistKind int16

const (
	ChecklistKindPacking ChecklistKind = 1
	ChecklistKindExpense ChecklistKind = 2
)

// ChecklistItem is one packing item or expense on a trip.
type ChecklistItem struct {
	ID          uuid.UUID
	Kind        ChecklistKind
	Text        string
	Done        bool
	AmountMinor int64
	Currency    string
	Position    int32
	UpdatedAt   time.Time
}

// Checklist is a trip's items plus the packing suggestions the user dismissed.
type Checklist struct {
	Items     []ChecklistItem
	Dismissed []string
}

// ChecklistRepository persists trip checklists. Every method takes the caller's
// user id and returns ErrNotFound when the trip is missing or someone else's,
// exactly like GetTrip, so a checklist never reveals that a trip exists.
type ChecklistRepository interface {
	GetChecklist(ctx context.Context, tripID, userID uuid.UUID) (*Checklist, error)
	UpsertItem(ctx context.Context, tripID, userID uuid.UUID, item ChecklistItem) (*ChecklistItem, error)
	DeleteItem(ctx context.Context, tripID, userID, itemID uuid.UUID) error
	DismissSuggestion(ctx context.Context, tripID, userID uuid.UUID, text string) error
}

type checklistRepository struct {
	db *pgxpool.Pool
}

// NewChecklistRepository returns the Postgres checklist store.
func NewChecklistRepository(db *pgxpool.Pool) ChecklistRepository {
	return &checklistRepository{db: db}
}

// normalizeDismissal is how a dismissed suggestion is stored and compared.
func normalizeDismissal(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

// lockOwnedTrip confirms the trip belongs to the user and locks its row for the
// rest of the transaction, so two concurrent inserts cannot both pass the cap.
func lockOwnedTrip(ctx context.Context, tx pgx.Tx, tripID, userID uuid.UUID) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM trips WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		tripID, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock trip: %w", err)
	}
	return nil
}

func (r *checklistRepository) ownsTrip(ctx context.Context, tripID, userID uuid.UUID) error {
	var ok bool
	if err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM trips WHERE id = $1 AND user_id = $2)`,
		tripID, userID).Scan(&ok); err != nil {
		return fmt.Errorf("check trip owner: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (r *checklistRepository) GetChecklist(ctx context.Context, tripID, userID uuid.UUID) (*Checklist, error) {
	if err := r.ownsTrip(ctx, tripID, userID); err != nil {
		return nil, err
	}

	out := &Checklist{Items: []ChecklistItem{}, Dismissed: []string{}}
	rows, err := r.db.Query(ctx, `
		SELECT id, kind, text, done, amount_minor, currency, position, updated_at
		FROM trip_checklist_items
		WHERE trip_id = $1
		ORDER BY kind, position, created_at, id`, tripID)
	if err != nil {
		return nil, fmt.Errorf("list checklist items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it ChecklistItem
		if err := rows.Scan(&it.ID, &it.Kind, &it.Text, &it.Done, &it.AmountMinor,
			&it.Currency, &it.Position, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan checklist item: %w", err)
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list checklist items: %w", err)
	}

	drows, err := r.db.Query(ctx, `
		SELECT text_lower FROM trip_packing_dismissed
		WHERE trip_id = $1 ORDER BY created_at, text_lower`, tripID)
	if err != nil {
		return nil, fmt.Errorf("list dismissed suggestions: %w", err)
	}
	defer drows.Close()
	for drows.Next() {
		var s string
		if err := drows.Scan(&s); err != nil {
			return nil, fmt.Errorf("scan dismissed suggestion: %w", err)
		}
		out.Dismissed = append(out.Dismissed, s)
	}
	if err := drows.Err(); err != nil {
		return nil, fmt.Errorf("list dismissed suggestions: %w", err)
	}
	return out, nil
}

func (r *checklistRepository) UpsertItem(ctx context.Context, tripID, userID uuid.UUID, item ChecklistItem) (*ChecklistItem, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockOwnedTrip(ctx, tx, tripID, userID); err != nil {
		return nil, err
	}

	// Only a new item counts against the cap; replaying an upsert must succeed
	// even on a full list, or an offline client could never sync its edits.
	var exists bool
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM trip_checklist_items WHERE trip_id = $1 AND id = $2),
		       (SELECT COUNT(*) FROM trip_checklist_items WHERE trip_id = $1)`,
		tripID, item.ID).Scan(&exists, &count); err != nil {
		return nil, fmt.Errorf("count checklist items: %w", err)
	}
	if !exists && count >= MaxChecklistItems {
		return nil, ErrChecklistFull
	}

	out := item
	if err := tx.QueryRow(ctx, `
		INSERT INTO trip_checklist_items
		    (trip_id, id, user_id, kind, text, done, amount_minor, currency, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (trip_id, id) DO UPDATE SET
		    kind = EXCLUDED.kind,
		    text = EXCLUDED.text,
		    done = EXCLUDED.done,
		    amount_minor = EXCLUDED.amount_minor,
		    currency = EXCLUDED.currency,
		    position = EXCLUDED.position,
		    updated_at = NOW()
		RETURNING updated_at`,
		tripID, item.ID, userID, item.Kind, item.Text, item.Done,
		item.AmountMinor, item.Currency, item.Position).Scan(&out.UpdatedAt); err != nil {
		return nil, fmt.Errorf("upsert checklist item: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &out, nil
}

func (r *checklistRepository) DeleteItem(ctx context.Context, tripID, userID, itemID uuid.UUID) error {
	if err := r.ownsTrip(ctx, tripID, userID); err != nil {
		return err
	}
	// Deleting an item that is already gone succeeds: a replayed delete from an
	// offline client is not an error.
	if _, err := r.db.Exec(ctx,
		`DELETE FROM trip_checklist_items WHERE trip_id = $1 AND id = $2`,
		tripID, itemID); err != nil {
		return fmt.Errorf("delete checklist item: %w", err)
	}
	return nil
}

func (r *checklistRepository) DismissSuggestion(ctx context.Context, tripID, userID uuid.UUID, text string) error {
	norm := normalizeDismissal(text)
	if norm == "" {
		return errEmptyDismissal
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockOwnedTrip(ctx, tx, tripID, userID); err != nil {
		return err
	}

	var exists bool
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM trip_packing_dismissed WHERE trip_id = $1 AND text_lower = $2),
		       (SELECT COUNT(*) FROM trip_packing_dismissed WHERE trip_id = $1)`,
		tripID, norm).Scan(&exists, &count); err != nil {
		return fmt.Errorf("count dismissed suggestions: %w", err)
	}
	if exists {
		return nil
	}
	if count >= MaxChecklistItems {
		return ErrChecklistFull
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO trip_packing_dismissed (trip_id, text_lower) VALUES ($1, $2)
		ON CONFLICT (trip_id, text_lower) DO NOTHING`, tripID, norm); err != nil {
		return fmt.Errorf("dismiss suggestion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// errEmptyDismissal is returned for a suggestion that is only whitespace.
var errEmptyDismissal = errors.New("suggestion text is empty")
