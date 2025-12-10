package interests

import (
	"context"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
)

func TestRepoCreateInterest(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	name := "hiking"
	desc := "outdoors"
	userID := uuid.New().String()
	returnedID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
        INSERT INTO user_custom_interests (name, description, active, created_at, updated_at, user_id)
        VALUES ($1, $2, $3, Now(), Now(), $4)
        RETURNING id, name, description, active, created_at, updated_at`)).
		WithArgs(name, &desc, true, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "active", "created_at", "updated_at"}).
			AddRow(returnedID, name, &desc, true, time.Now(), time.Now()))

	repo := NewRepositoryImpl(mock, slog.Default())

	interest, err := repo.CreateInterest(context.Background(), name, &desc, true, userID)
	if err != nil {
		t.Fatalf("CreateInterest: %v", err)
	}
	if interest == nil || interest.ID != returnedID || interest.Name != name {
		t.Fatalf("unexpected interest: %+v", interest)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetAllInterests(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	active := true
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, name, description,
               CASE WHEN 'global' = 'global' THEN false ELSE active END AS active,
               created_at, updated_at, 'global' AS type
        FROM interests
        UNION
        SELECT id, name, description, active, created_at, updated_at, 'custom' AS type
        FROM user_custom_interests
        ORDER BY name`)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "active", "created_at", "updated_at", "type"}).
			AddRow(uuid.New(), "hiking", nil, &active, now, &now, "global"))

	repo := NewRepositoryImpl(mock, slog.Default())

	interests, err := repo.GetAllInterests(context.Background())
	if err != nil {
		t.Fatalf("GetAllInterests: %v", err)
	}
	if len(interests) != 1 || interests[0].Name != "hiking" {
		t.Fatalf("unexpected interests: %+v", interests)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetInterestsForProfile(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	profileID := uuid.New()
	desc := "desc"
	active := true

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT i.id, i.name, i.description, i.active
        FROM interests i
        JOIN user_profile_interests upi ON i.id = upi.interest_id
        WHERE upi.profile_id = $1`)).
		WithArgs(profileID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "active"}).
			AddRow(uuid.New(), "cycling", &desc, &active))

	repo := NewRepositoryImpl(mock, slog.Default())

	interests, err := repo.GetInterestsForProfile(context.Background(), profileID)
	if err != nil {
		t.Fatalf("GetInterestsForProfile: %v", err)
	}
	if len(interests) != 1 || interests[0].Name != "cycling" {
		t.Fatalf("unexpected interests: %+v", interests)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
