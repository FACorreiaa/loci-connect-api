package poi

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Funchal centre, and Cabo Girão ~13 km west of it — the POI whose absence
// started this.
const (
	funchalLat, funchalLon = 32.6486, -16.9084
	giraoLat, giraoLon     = 32.6564, -17.0086
)

func fusedTestService(repo *MockPOIRepository) *ServiceImpl {
	return &ServiceImpl{
		poiRepository: repo,
		logger:        slog.New(slog.DiscardHandler),
	}
}

func lexicalHit(name string, lat, lon float64) LexicalHit {
	return LexicalHit{
		POI: locitypes.POIDetailedInfo{
			ID: uuid.New(), Name: name, Latitude: lat, Longitude: lon,
		},
		ExactName: true,
		TextRank:  1,
	}
}

// The whole point of E: a query for a named place must return that place.
//
// Before this, search_pois called SearchPOIsHybrid alone, which has no lexical
// arm — the query string never reached SQL, only its embedding — so the name
// could not participate in matching at all.
func TestAQueryForANamedPlaceReturnsThatPlace(t *testing.T) {
	repo := new(MockPOIRepository)
	girao := lexicalHit("Cabo Girão", giraoLat, giraoLon)

	repo.On("SearchPOIsLexical", mock.Anything, uuid.Nil, "Cabo Girão", retrieval.MaxSearchResults).
		Return([]LexicalHit{girao}, nil)

	svc := fusedTestService(repo)
	// Radius wide enough to include it — 25 km is the new default for exactly
	// this reason; the old 5 km excluded it before ranking could run.
	got, err := svc.SearchPOIsFused(context.Background(), locitypes.POIFilter{
		Location: locitypes.GeoPoint{Latitude: funchalLat, Longitude: funchalLon},
		Radius:   25,
	}, "Cabo Girão", 0.6)
	if err != nil {
		t.Fatalf("SearchPOIsFused: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("got %d results, want Cabo Girão", len(got))
	}
	if got[0].POI.Name != "Cabo Girão" {
		t.Errorf("got %q", got[0].POI.Name)
	}
	if got[0].Reason != retrieval.MatchLexical {
		t.Errorf("match reason = %q, want %q — it matched by name, and saying "+
			"'both' would claim a semantic match that did not happen",
			got[0].Reason, retrieval.MatchLexical)
	}
	// The distance is recomputed from the search centre, because the lexical
	// lane does not measure one.
	if got[0].POI.Distance < 8 || got[0].POI.Distance > 20 {
		t.Errorf("distance = %.1f km, want ~13", got[0].POI.Distance)
	}
}

// The radius is a hard bound on both lanes. A name match on the far side of the
// country is not a result for a search centred here.
func TestTheRadiusBoundsTheLexicalLaneToo(t *testing.T) {
	repo := new(MockPOIRepository)
	repo.On("SearchPOIsLexical", mock.Anything, uuid.Nil, "Cabo Girão", retrieval.MaxSearchResults).
		Return([]LexicalHit{lexicalHit("Cabo Girão", giraoLat, giraoLon)}, nil)

	svc := fusedTestService(repo)
	got, err := svc.SearchPOIsFused(context.Background(), locitypes.POIFilter{
		Location: locitypes.GeoPoint{Latitude: funchalLat, Longitude: funchalLon},
		Radius:   5, // the old default
	}, "Cabo Girão", 0.6)
	if err != nil {
		t.Fatalf("SearchPOIsFused: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a POI 13 km away survived a 5 km radius: %+v", got)
	}
}

// One lane failing must not fail the search — the other can still answer.
// The semantic lane fails here because embeddingService is nil.
func TestOneWorkingLaneStillAnswers(t *testing.T) {
	repo := new(MockPOIRepository)
	repo.On("SearchPOIsLexical", mock.Anything, uuid.Nil, "levada", retrieval.MaxSearchResults).
		Return([]LexicalHit{lexicalHit("Levada dos Balcões", 32.7594, -16.8994)}, nil)

	svc := fusedTestService(repo)
	got, err := svc.SearchPOIsFused(context.Background(), locitypes.POIFilter{
		Location: locitypes.GeoPoint{Latitude: 32.7594, Longitude: -16.8994},
		Radius:   25,
	}, "levada", 0.6)
	if err != nil {
		t.Fatalf("the lexical lane alone should answer: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want the levada", len(got))
	}
}

// Both lanes failing is a real error, not an empty result. An agent must be
// able to tell "nothing matched" from "search is broken".
func TestBothLanesFailingIsAnError(t *testing.T) {
	repo := new(MockPOIRepository)
	repo.On("SearchPOIsLexical", mock.Anything, uuid.Nil, "anything", retrieval.MaxSearchResults).
		Return([]LexicalHit(nil), errors.New("relation does not exist"))

	svc := fusedTestService(repo)
	_, err := svc.SearchPOIsFused(context.Background(), locitypes.POIFilter{
		Location: locitypes.GeoPoint{Latitude: funchalLat, Longitude: funchalLon},
		Radius:   25,
	}, "anything", 0.6)
	if err == nil {
		t.Fatal("both lanes failed and the search reported success")
	}
}

// Neither lane matching is an answer, not an error.
func TestNoMatchesIsNotAnError(t *testing.T) {
	repo := new(MockPOIRepository)
	repo.On("SearchPOIsLexical", mock.Anything, uuid.Nil, "zzzz", retrieval.MaxSearchResults).
		Return([]LexicalHit{}, nil)

	svc := fusedTestService(repo)
	got, err := svc.SearchPOIsFused(context.Background(), locitypes.POIFilter{
		Location: locitypes.GeoPoint{Latitude: funchalLat, Longitude: funchalLon},
		Radius:   25,
	}, "zzzz", 0.6)
	// The semantic lane errors on a nil embedding service, so this exercises
	// "lexical worked and found nothing" rather than "both failed".
	if err != nil {
		t.Fatalf("an empty result set is not an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results for a nonsense query", len(got))
	}
}

// The length limit the status tool advertises and nothing was enforcing.
func TestOverlongQueriesAreRefused(t *testing.T) {
	svc := fusedTestService(new(MockPOIRepository))

	long := make([]byte, retrieval.MaxQueryChars+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := svc.SearchPOIsFused(context.Background(), locitypes.POIFilter{}, string(long), 0.6); err == nil {
		t.Errorf("a %d-character query was accepted; status advertises a %d limit",
			len(long), retrieval.MaxQueryChars)
	}
	if _, err := svc.SearchPOIsFused(context.Background(), locitypes.POIFilter{}, "   ", 0.6); err == nil {
		t.Error("a whitespace-only query was accepted")
	}
}
