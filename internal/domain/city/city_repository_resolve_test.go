package city

import (
	"context"
	"log/slog"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"
)

var resolveCols = []string{
	"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
}

// A city with both a coordinate-bearing row and a coordinate-less stub must
// resolve to the usable one every time. Before the ORDER BY, which row won was
// whatever the planner emitted first, so compare failed intermittently.
func TestRepoFindCityCandidates_ReturnsCoordinateRowsFirst(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	good, stub := uuid.New(), uuid.New()
	lat, lon := 41.14961, -8.61099

	mock.ExpectQuery(regexp.QuoteMeta(`ORDER BY
            (center_location IS NOT NULL) DESC,
            (LOWER(name) = LOWER($1)) DESC,
            similarity(name, $1) DESC,
            name ASC`)).
		WithArgs("Porto", 10).
		WillReturnRows(pgxmock.NewRows(resolveCols).
			AddRow(good, "Porto", "Portugal", "Porto", "", &lat, &lon).
			AddRow(stub, "Porto", "Unknown", "Unknown", "", nil, nil))

	repo := NewCityRepository(mock, slog.Default())

	cities, err := repo.FindCityCandidates(context.Background(), "Porto", 10)
	if err != nil {
		t.Fatalf("FindCityCandidates: %v", err)
	}
	if len(cities) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(cities))
	}
	if cities[0].ID != good {
		t.Errorf("first candidate should be the row with coordinates")
	}
	if cities[0].CenterLatitude == nil || *cities[0].CenterLatitude != lat {
		t.Errorf("coordinates did not survive: %+v", cities[0].CenterLatitude)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoFindCityCandidates_DefaultsLimit(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta("FROM cities")).
		WithArgs("Porto", 10).
		WillReturnRows(pgxmock.NewRows(resolveCols))

	repo := NewCityRepository(mock, slog.Default())
	if _, err := repo.FindCityCandidates(context.Background(), "Porto", 0); err != nil {
		t.Fatalf("FindCityCandidates: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoEnrichCity_UpdatesRow(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	id := uuid.New()
	// Longitude precedes latitude, matching ST_MakePoint's argument order. A
	// swap here would place every backfilled city in the wrong hemisphere.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE cities SET")).
		WithArgs(id, -8.61099, 41.14961, "Portugal", "Porto").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	repo := NewCityRepository(mock, slog.Default())
	if err := repo.EnrichCity(context.Background(), id, 41.14961, -8.61099, "Portugal", "Porto"); err != nil {
		t.Fatalf("EnrichCity: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// Promoting country from 'Unknown' to 'Portugal' can collide with an already
// canonical row, because uniqueness is (name, state_province, country). The
// coordinates are what unblock the comparison, so they must still land.
func TestRepoEnrichCity_UniqueViolationFallsBackToCoordinates(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	id := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE cities SET")).
		WithArgs(id, -8.61099, 41.14961, "Portugal", "Porto").
		WillReturnError(&pgconn.PgError{Code: uniqueViolation})

	mock.ExpectExec(regexp.QuoteMeta("UPDATE cities SET")).
		WithArgs(id, -8.61099, 41.14961).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	repo := NewCityRepository(mock, slog.Default())
	if err := repo.EnrichCity(context.Background(), id, 41.14961, -8.61099, "Portugal", "Porto"); err != nil {
		t.Fatalf("a name collision must not fail the backfill: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// Any other database error is real and must surface.
func TestRepoEnrichCity_OtherErrorsPropagate(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	id := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE cities SET")).
		WithArgs(id, -8.6, 41.1, "Portugal", "Porto").
		WillReturnError(&pgconn.PgError{Code: "08006"}) // connection failure

	repo := NewCityRepository(mock, slog.Default())
	if err := repo.EnrichCity(context.Background(), id, 41.1, -8.6, "Portugal", "Porto"); err == nil {
		t.Fatal("expected the error to propagate")
	}
}

func TestRepoFindCityNear_ReturnsNilWhenNothingInRadius(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	// Radius is passed in metres; ST_DWithin on geography works in metres.
	mock.ExpectQuery(regexp.QuoteMeta("ST_DWithin")).
		WithArgs(-8.6, 41.1, float64(25000)).
		WillReturnRows(pgxmock.NewRows(resolveCols))

	repo := NewCityRepository(mock, slog.Default())
	city, err := repo.FindCityNear(context.Background(), 41.1, -8.6, 25)
	if err != nil {
		t.Fatalf("no city in range is a valid answer, not an error: %v", err)
	}
	if city != nil {
		t.Fatalf("expected nil, got %+v", city)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoFindCityNear_ZeroRadiusDoesNotQuery(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	repo := NewCityRepository(mock, slog.Default())
	city, err := repo.FindCityNear(context.Background(), 41.1, -8.6, 0)
	if err != nil || city != nil {
		t.Fatalf("got %+v / %v", city, err)
	}
	// No query was registered, so any call would fail the expectations check.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
