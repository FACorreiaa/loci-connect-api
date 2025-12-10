package poi

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestFindPoiByNameAndCity(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	name := "Central Park"
	cityID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT name, description, ST_Y(location) as lat, ST_X(location) as lon, COALESCE(poi_type, '') AS category
        FROM points_of_interest
        WHERE name = $1 AND city_id = $2
    `)).
		WithArgs(name, cityID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "description", "lat", "lon", "category"}).
			AddRow(name, "A park", 40.0, -73.0, "park"))

	repo := NewRepository(mock, slog.Default())

	poi, err := repo.FindPoiByNameAndCity(context.Background(), name, cityID)
	if err != nil {
		t.Fatalf("FindPoiByNameAndCity: %v", err)
	}
	if poi == nil || poi.Name != name || poi.Category != "park" {
		t.Fatalf("unexpected poi: %+v", poi)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetPOIsByLocationAndDistance(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	cityID := uuid.New()
	userLocation := locitypes.UserLocation{UserLat: 10.0, UserLon: 20.0, SearchRadiusKm: 5}

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id,
            name,
            ST_X(location::geometry) AS longitude,
            ST_Y(location::geometry) AS latitude,
            COALESCE(poi_type, '') AS category,
            COALESCE(ai_summary, '') AS description_poi,
            ST_Distance(location::geography, ST_GeomFromText($1, 4326)::geography) AS distance
        FROM points_of_interest
        WHERE city_id = $2 AND ST_DWithin(location::geography, ST_GeomFromText($1, 4326)::geography, $3 * 1000)
        ORDER BY distance ASC
    `)).
		WithArgs(pgPoint(userLocation), cityID, userLocation.SearchRadiusKm).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "longitude", "latitude", "category", "description_poi", "distance",
		}).
			AddRow(uuid.New(), "Museum", 1.0, 2.0, "museum", "desc", 100.0))

	repo := NewRepository(mock, slog.Default())

	pois, err := repo.GetPOIsByCityAndDistance(context.Background(), cityID, userLocation)
	if err != nil {
		t.Fatalf("GetPOIsByCityAndDistance: %v", err)
	}
	if len(pois) != 1 || pois[0].Name != "Museum" || pois[0].Distance == 0 {
		t.Fatalf("unexpected pois: %+v", pois)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestSaveLlmInteraction(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	id := uuid.New()
	interaction := &locitypes.LlmInteraction{
		UserID:    uuid.New(),
		ModelName: "gpt",
		Prompt:    "hi",
		Response:  "hey",
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		INSERT INTO llm_interactions (user_id, model_name, prompt, response, latitude, longitude, distance)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`)).
		WithArgs(interaction.UserID, interaction.ModelName, interaction.Prompt, interaction.Response, interaction.Latitude, interaction.Longitude, interaction.Distance).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(id))

	repo := NewRepository(mock, slog.Default())

	got, err := repo.SaveLlmInteraction(context.Background(), interaction)
	if err != nil {
		t.Fatalf("SaveLlmInteraction: %v", err)
	}
	if got != id {
		t.Fatalf("expected %s, got %s", id, got)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func pgPoint(loc locitypes.UserLocation) string {
	return fmt.Sprintf("SRID=4326;POINT(%f %f)", loc.UserLon, loc.UserLat)
}
