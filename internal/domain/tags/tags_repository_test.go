package tags

import (
	"context"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
)

func TestGetAll(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            g.id,
            g.name,
            g.description,
            g.tag_type,
            'global' AS source,
			CASE WHEN 'global' = 'global' THEN false ELSE g.active END AS active,
            g.created_at
        FROM global_tags g
        WHERE g.active = TRUE

        UNION ALL

        -- Select User Personal Tags
        SELECT
            upt.id,
            upt.name,
            NULL AS description,
            upt.tag_type,
            'personal' AS source,
			active,
            upt.created_at
        FROM user_personal_tags upt
        WHERE upt.user_id = $1

        ORDER BY name`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{"id", "name", "description", "tag_type", "source", "active", "created_at"}).
				AddRow(uuid.New(), "A", nil, "type1", "global", true, now).
				AddRow(uuid.New(), "B", nil, "type2", "personal", true, now),
		)

	repo := NewRepositoryImpl(mock, slog.Default())
	got, err := repo.GetAll(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(got))
	}
}

func TestGetTagByName(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	now := time.Now()
	tagID := uuid.New()
	name := "test"

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, name, description, tag_type, created_at
        FROM global_tags
        WHERE name = $1 AND active = TRUE`)).
		WithArgs(name).
		WillReturnRows(
			pgxmock.NewRows([]string{"id", "name", "description", "tag_type", "created_at"}).
				AddRow(tagID, name, nil, "type1", now),
		)

	repo := NewRepositoryImpl(mock, slog.Default())
	got, err := repo.GetTagByName(context.Background(), name)
	if err != nil {
		t.Fatalf("GetTagByName: %v", err)
	}
	if got.ID != tagID || got.Name != name {
		t.Fatalf("unexpected tag: %+v", got)
	}
}
