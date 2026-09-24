package itinerarylist

import (
	"context"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

var listRowColumns = []string{"id", "user_id", "name", "description", "image_url", "is_public", "is_itinerary", "parent_list_id", "city_id", "item_count", "view_count", "save_count", "created_at", "updated_at"}

// GetAllUserLists returns both kinds, and a list with no city (city_id NULL,
// read back as the nil uuid) is not dropped.
func TestGetAllUserLists(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + listColumns + ` FROM lists l
        WHERE l.user_id = $1
        ORDER BY l.created_at DESC, l.id`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows(listRowColumns).
				AddRow(uuid.New(), userID, "Favourites", "", "", false, false, uuid.Nil, uuid.Nil, 2, 0, 0, now, now).
				AddRow(uuid.New(), userID, "Porto", "", "", false, true, uuid.Nil, uuid.New(), 5, 0, 0, now, now),
		)

	got, err := NewRepository(mock, slog.Default()).GetAllUserLists(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetAllUserLists: %v", err)
	}
	if len(got) != 2 || got[0].IsItinerary || !got[1].IsItinerary {
		t.Fatalf("expected a custom list and an itinerary, got %+v", got)
	}
	if got[0].CityID != uuid.Nil || got[0].ItemCount != 2 {
		t.Fatalf("unexpected custom list: %+v", got[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetList(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	listID := uuid.New()
	userID := uuid.New()
	parentID := uuid.New()
	cityID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + listColumns + ` FROM lists l WHERE l.id = $1`)).
		WithArgs(listID).
		WillReturnRows(
			pgxmock.NewRows(listRowColumns).
				AddRow(listID, userID, "Trip", "Desc", "img", true, false, parentID, cityID, 3, 10, 5, now, now),
		)

	repo := NewRepository(mock, slog.Default())
	got, err := repo.GetList(context.Background(), listID)
	if err != nil {
		t.Fatalf("GetList: %v", err)
	}
	if got.ID != listID || got.UserID != userID || got.ParentListID == nil || *got.ParentListID != parentID {
		t.Fatalf("unexpected list: %+v", got)
	}
	if got.ItemCount != 3 || got.CityID != cityID {
		t.Fatalf("item count / city not mapped: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetSubLists(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	parentID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + listColumns + ` FROM lists l WHERE l.parent_list_id = $1`)).
		WithArgs(parentID).
		WillReturnRows(
			pgxmock.NewRows(listRowColumns).
				AddRow(uuid.New(), uuid.New(), "Child1", "Desc1", "img1", true, false, parentID, uuid.New(), 0, 1, 2, now, now).
				AddRow(uuid.New(), uuid.New(), "Child2", "Desc2", "img2", true, false, parentID, uuid.New(), 0, 3, 4, now, now),
		)

	repo := NewRepository(mock, slog.Default())
	got, err := repo.GetSubLists(context.Background(), parentID)
	if err != nil {
		t.Fatalf("GetSubLists: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sublists, got %d", len(got))
	}
	for _, l := range got {
		if l.ParentListID == nil || *l.ParentListID != parentID {
			t.Fatalf("unexpected parent id for list %+v", l)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetListItems(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	listID := uuid.New()
	itemID := uuid.New()
	sourceID := uuid.New()
	now := time.Now()
	dayNumber := int32(2)
	duration := int32(45)

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT ` + listItemColumns + `
        FROM list_items
        WHERE list_id = $1
        ORDER BY position
    `)).
		WithArgs(listID).
		WillReturnRows(
			pgxmock.NewRows([]string{"list_id", "item_id", "content_type", "position", "notes", "day_number", "time_slot", "duration", "source_llm_interaction_id", "item_ai_description", "created_at", "updated_at"}).
				AddRow(listID, itemID, locitypes.ContentTypePOI, 1, "note", dayNumber, now, duration, sourceID, "ai desc", now, now),
		)

	repo := NewRepository(mock, slog.Default())
	got, err := repo.GetListItems(context.Background(), listID)
	if err != nil {
		t.Fatalf("GetListItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	item := got[0]
	if item.DayNumber == nil || *item.DayNumber != int(dayNumber) {
		t.Fatalf("expected day number %d, got %+v", dayNumber, item.DayNumber)
	}
	if item.Duration == nil || *item.Duration != int(duration) {
		t.Fatalf("expected duration %d, got %+v", duration, item.Duration)
	}
	if item.SourceLlmInteractionID == nil || *item.SourceLlmInteractionID != sourceID {
		t.Fatalf("unexpected source interaction: %+v", item.SourceLlmInteractionID)
	}
	if item.ItemAIDescription != "ai desc" {
		t.Fatalf("unexpected AI description: %s", item.ItemAIDescription)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
