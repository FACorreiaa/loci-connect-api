package poi

import (
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestNamesQuery(t *testing.T) {
	hit := func(name string, exact bool) []LexicalHit {
		return []LexicalHit{{POI: locitypes.POIDetailedInfo{Name: name}, ExactName: exact}}
	}
	cases := []struct {
		name  string
		hits  []LexicalHit
		query string
		want  bool
	}{
		{"nothing found", nil, "El Rinconcillo", false},
		{"exact name", hit("El Rinconcillo", true), "el rinconcillo", true},
		{"name contains the query", hit("Bar El Rinconcillo", false), " Rinconcillo ", true},
		{"a kind of place, not a name", hit("Quiet Corner Cafe", false), "a quiet cafe", false},
		{"too short to be a name", hit("Barrio Santa Cruz", false), "bar", false},
	}
	for _, c := range cases {
		if got := namesQuery(c.hits, c.query); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLexicalPOIsKeepsOrder(t *testing.T) {
	if lexicalPOIs(nil) != nil {
		t.Fatal("no hits must be nil so the caller falls through")
	}
	got := lexicalPOIs([]LexicalHit{
		{POI: locitypes.POIDetailedInfo{Name: "a"}},
		{POI: locitypes.POIDetailedInfo{Name: "b"}},
	})
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("got %+v", got)
	}
}
