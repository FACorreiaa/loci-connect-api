package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// validGastronomy is output the parser accepts and the validator keeps.
const validGastronomy = "```json\n" + `{
  "gastronomy": {
    "city_name": "Porto",
    "country": "Portugal",
    "overview": "Hearty northern cooking built on pork, bread and port wine.",
    "culinary_traditions": ["Sunday lunch is long."],
    "dishes": [
      {"name": "Francesinha", "category": "main", "is_signature": true,
       "places": [{"name": "Café Santiago", "latitude": 41.1456, "longitude": -8.6110}]},
      {"name": "Tripas à moda do Porto", "category": "main",
       "places": [{"name": "Casa Aleixo", "latitude": 0, "longitude": 0}]},
      {"name": "Bifana", "category": "street_food",
       "places": [{"name": "Conga"}, {"name": "  "}]},
      {"name": "", "places": [{"name": "Nowhere"}]}
    ],
    "dining_tips": ["Lunch is from 12:30."]
  }
}` + "\n```"

func TestParseGastronomyNormalises(t *testing.T) {
	g, err := parseGastronomy(validGastronomy)
	if err != nil {
		t.Fatalf("parseGastronomy: %v", err)
	}
	if len(g.Dishes) != 3 {
		t.Fatalf("dishes = %d, want 3 (the unnamed dish is dropped)", len(g.Dishes))
	}
	if g.Dishes[0].Places[0].Latitude == nil {
		t.Error("real coordinates were dropped")
	}
	if g.Dishes[1].Places[0].Latitude != nil {
		t.Error("0,0 coordinates were kept")
	}
	if len(g.Dishes[2].Places) != 1 {
		t.Errorf("places = %+v, want the blank-named place dropped", g.Dishes[2].Places)
	}
	if !g.Usable() {
		t.Error("a complete answer is not usable")
	}
}

func TestValidateGastronomyPart(t *testing.T) {
	if !validateGeneratedPart(partGastronomy, validGastronomy, gastronomyPlaceBudget) {
		t.Error("valid gastronomy rejected")
	}
	thin := `{"gastronomy": {"overview": "x", "dishes": [{"name": "A", "places": [{"name": "P"}]}]}}`
	if validateGeneratedPart(partGastronomy, thin, gastronomyPlaceBudget) {
		t.Error("a one-dish answer was kept")
	}
	if validateGeneratedPart(partGastronomy, `{"dishes": []}`, gastronomyPlaceBudget) {
		t.Error("an answer without the envelope was kept")
	}
}

// The gastronomy key names only the city: every query, domain and user about
// Porto shares one entry, and the standalone lookup finds it.
func TestGastronomyKeyIsCityScoped(t *testing.T) {
	cityID := uuid.New()
	base := generationKeyInput{
		Part: partGastronomy, CityID: cityID, CityName: "Porto", ModelID: "m",
		Domain: locitypes.DomainItinerary, Query: "3 days in porto", UserID: uuid.New(),
		SnapshotHash: "abc", POITarget: 30, TripDays: 3,
	}
	other := base
	other.Domain = locitypes.DomainGeneral
	other.Query = "what to see in porto"
	other.UserID = uuid.New()
	other.SnapshotHash = "def"
	other.POITarget = 10

	if buildGenerationKey(base) != buildGenerationKey(other) {
		t.Error("gastronomy key varies with query, domain or user")
	}
	if buildGenerationKey(base) != gastronomyCacheKey(cityID, "Porto", "m") {
		t.Error("chat key and standalone key differ")
	}
	if gastronomyCacheKey(cityID, "Porto", "m") == gastronomyCacheKey(uuid.New(), "Porto", "m") {
		t.Error("different cities share a key")
	}
	if gastronomyCacheKey(cityID, "Porto", "m") == gastronomyCacheKey(cityID, "Porto", "other-model") {
		t.Error("different models share a key")
	}
}

func TestPlanIncludesGastronomyOnlyForItineraryAndGeneral(t *testing.T) {
	l := newPlanService(t, nil)
	for domain, want := range map[locitypes.DomainType]bool{
		locitypes.DomainItinerary:     true,
		locitypes.DomainGeneral:       true,
		locitypes.DomainDining:        false,
		locitypes.DomainAccommodation: false,
		locitypes.DomainActivities:    false,
	} {
		cc := testChatContext(t, "porto")
		cc.Domain = domain
		var got *partPlan
		for _, p := range l.planGeneration(cc) {
			if p.Part == partGastronomy {
				got = &p
			}
		}
		if (got != nil) != want {
			t.Errorf("%s: gastronomy planned = %v, want %v", domain, got != nil, want)
			continue
		}
		if got == nil {
			continue
		}
		if got.POITarget != gastronomyPlaceBudget {
			t.Errorf("%s: POITarget = %d, want the fixed gastronomy budget", domain, got.POITarget)
		}
		if prompt := got.Prompt(nil); !strings.Contains(prompt, `"gastronomy"`) || !strings.Contains(prompt, cc.CityName) {
			t.Errorf("%s: prompt does not ask for the gastronomy envelope for %s", domain, cc.CityName)
		}
	}
}

// gastronomyResponse wraps text as a one-candidate model response.
func gastronomyResponse(text string) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		ModelVersion: "v1",
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: text}}},
		}},
	}
}

func TestGetCityGastronomyGeneratesThenServesFromCache(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)
	ai := new(MockAIClient)
	ai.On("Model").Return("").Maybe()
	ai.On("Generate", mock.Anything, mock.Anything, mock.Anything).
		Return(gastronomyResponse(validGastronomy), nil).Once()
	l.aiClient = ai

	g, cached, err := l.GetCityGastronomy(context.Background(), uuid.Nil, "Porto")
	if err != nil || cached {
		t.Fatalf("first call: cached=%v err=%v", cached, err)
	}
	if g.CityName != "Porto" || len(g.Dishes) != 3 {
		t.Fatalf("gastronomy = %+v", g)
	}
	if len(store.puts) != 1 || store.puts[0].Part != string(partGastronomy) {
		t.Fatalf("durable writes = %+v, want one gastronomy row", store.puts)
	}

	// Second call: memory hit, no model call (Once above would fail it).
	if _, cached, err = l.GetCityGastronomy(context.Background(), uuid.Nil, "porto "); err != nil || !cached {
		t.Fatalf("second call: cached=%v err=%v", cached, err)
	}

	// A cold pod: memory is gone, the durable row answers.
	l2 := newPlanService(t, store)
	l2.aiClient = ai
	if _, cached, err = l2.GetCityGastronomy(context.Background(), uuid.Nil, "Porto"); err != nil || !cached {
		t.Fatalf("durable hit: cached=%v err=%v", cached, err)
	}
	ai.AssertExpectations(t)
}

func TestGetCityGastronomyRejectsUnusableOutput(t *testing.T) {
	store := newFakeGenerationStore()
	l := newPlanService(t, store)
	ai := new(MockAIClient)
	ai.On("Model").Return("").Maybe()
	ai.On("Generate", mock.Anything, mock.Anything, mock.Anything).
		Return(gastronomyResponse(`{"gastronomy": {"overview": "", "dishes": []}}`), nil)
	l.aiClient = ai

	_, _, err := l.GetCityGastronomy(context.Background(), uuid.Nil, "Xyzzy")
	if !errors.Is(err, ErrGastronomyUnavailable) {
		t.Fatalf("err = %v, want ErrGastronomyUnavailable", err)
	}
	if len(store.puts) != 0 {
		t.Error("unusable output was cached")
	}
}

func TestGetCityGastronomyNeedsACity(t *testing.T) {
	l := newPlanService(t, nil)
	if _, _, err := l.GetCityGastronomy(context.Background(), uuid.Nil, "  "); !errors.Is(err, ErrGastronomyCityNotFound) {
		t.Fatalf("err = %v, want ErrGastronomyCityNotFound", err)
	}
	// An id with no city repository cannot be resolved.
	if _, _, err := l.GetCityGastronomy(context.Background(), uuid.New(), ""); !errors.Is(err, ErrGastronomyCityNotFound) {
		t.Fatalf("err = %v, want ErrGastronomyCityNotFound", err)
	}
}

// A failed gastronomy part is dropped, never surfaced: the itinerary turn
// still succeeds and the client sees no error event for it.
func TestAggregateOmitsUnusableGastronomy(t *testing.T) {
	l := newPlanService(t, nil)
	cc := testChatContext(t, "porto")
	data, err := l.aggregateAndParse(cc, map[string]string{
		string(partCityData):   validCityData,
		string(partGastronomy): `{"gastronomy": {"dishes": []}}`,
	})
	if err != nil {
		t.Fatalf("aggregateAndParse: %v", err)
	}
	if data.Gastronomy != nil {
		t.Errorf("unusable gastronomy kept: %+v", data.Gastronomy)
	}

	data, err = l.aggregateAndParse(cc, map[string]string{
		string(partGastronomy): validGastronomy,
	})
	if err != nil || data.Gastronomy == nil || len(data.Gastronomy.Dishes) != 3 {
		t.Fatalf("gastronomy = %+v, err = %v", data.Gastronomy, err)
	}
}

func TestIsOptionalPart(t *testing.T) {
	if !isOptionalPart(partGastronomy) {
		t.Error("gastronomy must be optional")
	}
	for _, p := range []generationPart{partCityData, partGeneralPOIs, partItinerary, partHotels, partRestaurants, partActivities} {
		if isOptionalPart(p) {
			t.Errorf("%s must not be optional", p)
		}
	}
}
