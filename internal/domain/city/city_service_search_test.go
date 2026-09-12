package city

import (
	"context"
	"errors"
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
	"github.com/google/uuid"
)

func storedCity(name, country string) locitypes.CityDetail {
	return locitypes.CityDetail{
		ID: uuid.New(), Name: name, Country: country,
		CenterLatitude: ptr(38.7), CenterLongitude: ptr(-9.1),
	}
}

// The picker is useless while the table is near-empty, which is why the client's
// city search module was dead code.
func TestSearchCities_TopsUpThinResultsFromGeocoder(t *testing.T) {
	repo := &fakeRepo{searchResult: []locitypes.CityDetail{storedCity("Porto", "Portugal")}}
	fwd := &fakeForward{places: []geocode.Place{portoPT, portoBR}}
	svc := NewCityService(repo, quietLogger()).WithGeocoder(fwd)

	got, err := svc.SearchCities(context.Background(), "Porto", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected the stored city plus the Brazilian one, got %d: %+v", len(got), got)
	}
	// The stored row is already Porto, Portugal, so only Brazil is new.
	if got[1].Country != "Brazil" {
		t.Errorf("second result: got %q, want Brazil", got[1].Country)
	}
	// A geocoded suggestion has no row yet, and says so with an empty id.
	if got[1].ID != uuid.Nil {
		t.Errorf("geocoded suggestion should carry no id, got %v", got[1].ID)
	}
	if got[1].CenterLatitude == nil {
		t.Error("a suggestion without coordinates cannot be used to skip resolution")
	}
}

// Dedupe has to bridge the two spellings: stored rows carry a country name, the
// geocoder a country code.
func TestSearchCities_DeduplicatesAcrossCountrySpellings(t *testing.T) {
	repo := &fakeRepo{searchResult: []locitypes.CityDetail{storedCity("Porto", "PT")}}
	fwd := &fakeForward{places: []geocode.Place{portoPT}}
	svc := NewCityService(repo, quietLogger()).WithGeocoder(fwd)

	got, err := svc.SearchCities(context.Background(), "Porto", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected the duplicate to be dropped, got %d: %+v", len(got), got)
	}
}

// A keystroke-driven field must not write to the database.
func TestSearchCities_NeverPersists(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewCityService(repo, quietLogger()).WithGeocoder(&fakeForward{places: []geocode.Place{portoPT}})

	if _, err := svc.SearchCities(context.Background(), "Porto", 8); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.savedCount() != 0 {
		t.Errorf("search wrote %d rows", repo.savedCount())
	}
	if repo.enrichedCount() != 0 {
		t.Errorf("search enriched %d rows", repo.enrichedCount())
	}
}

// The mount-time browse must not spend a provider request.
func TestSearchCities_EmptyQuerySkipsGeocoder(t *testing.T) {
	fwd := &fakeForward{places: []geocode.Place{portoPT}}
	svc := NewCityService(&fakeRepo{}, quietLogger()).WithGeocoder(fwd)

	if _, err := svc.SearchCities(context.Background(), "", 8); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fwd.callCount() != 0 {
		t.Errorf("geocoder called %d times for a browse", fwd.callCount())
	}
}

// A healthy set of stored matches is the common case and must stay one query.
func TestSearchCities_RichResultsSkipGeocoder(t *testing.T) {
	repo := &fakeRepo{searchResult: []locitypes.CityDetail{
		storedCity("Porto", "Portugal"), storedCity("Portimão", "Portugal"),
		storedCity("Portalegre", "Portugal"), storedCity("Porto Covo", "Portugal"),
		storedCity("Porto Santo", "Portugal"),
	}}
	fwd := &fakeForward{places: []geocode.Place{portoBR}}
	svc := NewCityService(repo, quietLogger()).WithGeocoder(fwd)

	got, err := svc.SearchCities(context.Background(), "Porto", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fwd.callCount() != 0 {
		t.Errorf("geocoder called %d times despite 5 stored matches", fwd.callCount())
	}
	if len(got) != 5 {
		t.Errorf("expected the stored results untouched, got %d", len(got))
	}
}

// A provider hiccup must not empty a picker that already had something to show.
func TestSearchCities_GeocoderFailureKeepsStoredResults(t *testing.T) {
	repo := &fakeRepo{searchResult: []locitypes.CityDetail{storedCity("Porto", "Portugal")}}
	fwd := &fakeForward{err: errors.New("provider down")}
	svc := NewCityService(repo, quietLogger()).WithGeocoder(fwd)

	got, err := svc.SearchCities(context.Background(), "Porto", 8)
	if err != nil {
		t.Fatalf("a geocoder failure must not fail city search: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected the stored result to survive, got %d", len(got))
	}
}

func TestSearchCities_NilGeocoderIsDatabaseOnly(t *testing.T) {
	repo := &fakeRepo{searchResult: []locitypes.CityDetail{storedCity("Porto", "Portugal")}}
	svc := NewCityService(repo, quietLogger())

	got, err := svc.SearchCities(context.Background(), "Porto", 8)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d results, err %v", len(got), err)
	}
}

// The limit is the picker's dropdown size; topping up must not overflow it.
func TestSearchCities_RespectsLimit(t *testing.T) {
	many := make([]geocode.Place, 0, 10)
	for i := 0; i < 10; i++ {
		many = append(many, geocode.Place{
			Name: string(rune('A'+i)) + "town", Country: "Portugal", CountryCode: "PT",
			FeatureCode: "PPL", Lat: 38, Lon: -9,
		})
	}
	svc := NewCityService(&fakeRepo{}, quietLogger()).WithGeocoder(&fakeForward{places: many})

	got, err := svc.SearchCities(context.Background(), "town", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(got))
	}
}
