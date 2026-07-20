package preference

import (
	"context"
	"math"
	"sort"
	"testing"

	"github.com/google/uuid"
)

// Fixture: foodie saves/favorites → dining POIs outrank museums under the same
// neutral "things to do" query after preference blend.
func TestFoodiePrefsRankDiningOverMuseum(t *testing.T) {
	// Toy 3-d space: dim0=dining, dim1=museum, dim2=noise.
	restaurant := []float32{1, 0, 0}
	cafe := []float32{0.9, 0.1, 0}
	museum := []float32{0, 1, 0}
	gallery := []float32{0.1, 0.9, 0}

	// Foodie feedback: favorited restaurant + saved cafe.
	pref, err := WeightedAverage(
		[][]float32{restaurant, cafe},
		[]float32{EventMultiplier(EventFavorited), EventMultiplier(EventSaved)},
	)
	if err != nil {
		t.Fatal(err)
	}

	uid := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	reader := stubReader{emb: pref, ok: true}

	// Neutral discover query (equal dining/museum interest).
	query := []float32{0.5, 0.5, 0}
	personalized := PersonalizeQuery(context.Background(), reader, uid, query)

	type cand struct {
		name string
		emb  []float32
	}
	cands := []cand{
		{"museum", museum},
		{"gallery", gallery},
		{"restaurant", restaurant},
		{"cafe", cafe},
	}

	type scored struct {
		name string
		sim  float64
	}
	baseline := make([]scored, len(cands))
	ranked := make([]scored, len(cands))
	for i, c := range cands {
		baseline[i] = scored{c.name, cosine(query, c.emb)}
		ranked[i] = scored{c.name, cosine(personalized, c.emb)}
	}
	sort.Slice(baseline, func(i, j int) bool { return baseline[i].sim > baseline[j].sim })
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].sim > ranked[j].sim })

	// Without prefs, museum-ish and dining can interleave; with foodie prefs,
	// top-2 must be dining venues.
	if ranked[0].name != "restaurant" && ranked[0].name != "cafe" {
		t.Fatalf("expected dining on top after personalize, got %#v", ranked)
	}
	if ranked[1].name != "restaurant" && ranked[1].name != "cafe" {
		t.Fatalf("expected dining 1-2 after personalize, got %#v", ranked)
	}
	diningTop := ranked[0].sim+ranked[1].sim
	museumTop := 0.0
	for _, s := range ranked {
		if s.name == "museum" || s.name == "gallery" {
			museumTop += s.sim
		}
	}
	if diningTop <= museumTop {
		t.Fatalf("foodie blend should favor dining mass: dining=%v museum=%v ranked=%#v baseline=%#v",
			diningTop, museumTop, ranked, baseline)
	}
}

func TestMuseumBaselineUnaffectedWithoutPrefs(t *testing.T) {
	query := []float32{0.2, 0.8, 0} // museum-leaning query
	museum := []float32{0, 1, 0}
	restaurant := []float32{1, 0, 0}
	uid := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	got := PersonalizeQuery(context.Background(), stubReader{ok: false}, uid, query)
	if cosine(got, museum) <= cosine(got, restaurant) {
		t.Fatal("museum-leaning query without prefs should still prefer museum")
	}
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na < 1e-12 || nb < 1e-12 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
