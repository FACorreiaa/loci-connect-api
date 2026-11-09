package flights

import (
	"testing"
)

func airports(t *testing.T) *Airports {
	t.Helper()
	a, err := LoadAirports()
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func iatas(opts []PlaceOption) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.IATA
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestSearchRanksExactIATAFirst(t *testing.T) {
	got := airports(t).Search("lis", 8)
	if len(got) == 0 || got[0].IATA != "LIS" || got[0].Kind != KindAirport {
		t.Fatalf("Search(lis) = %v, want LIS first", iatas(got))
	}
}

func TestSearchFoldsAccentsAndCase(t *testing.T) {
	got := airports(t).Search(" são paulo ", 8)
	if !contains(iatas(got), "GRU") {
		t.Fatalf("Search(são paulo) = %v, want GRU", iatas(got))
	}
	if got2 := airports(t).Search("SAO PAULO", 8); !contains(iatas(got2), "GRU") {
		t.Fatalf("Search(SAO PAULO) = %v, want GRU", iatas(got2))
	}
}

func TestSearchShortQueryEmpty(t *testing.T) {
	a := airports(t)
	if got := a.Search("l", 8); got != nil {
		t.Errorf("Search(l) = %v, want nil", iatas(got))
	}
	if got := a.Search("  ", 8); got != nil {
		t.Errorf("Search(blank) = %v, want nil", iatas(got))
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	if got := airports(t).Search("san", 3); len(got) != 3 {
		t.Fatalf("Search(san, 3) returned %d", len(got))
	}
}

func TestSearchIncludesMetro(t *testing.T) {
	got := airports(t).Search("london", 8)
	if len(got) == 0 {
		t.Fatal("no results")
	}
	want := PlaceOption{IATA: "LON", Name: "London (all airports)", City: "London", CountryCode: "GB", Kind: KindMetro}
	if g := got[0]; g.IATA != want.IATA || g.Name != want.Name || g.Kind != want.Kind || g.CountryCode != want.CountryCode {
		t.Fatalf("Search(london)[0] = %+v, want %+v", g, want)
	}
	if !contains(iatas(got), "LHR") {
		t.Errorf("Search(london) = %v, want LHR among results", iatas(got))
	}
}

func TestResolveUsesCountry(t *testing.T) {
	a := airports(t)
	if o, ok := a.Resolve("Valencia", "ES", nil, nil); !ok || o.IATA != "VLC" {
		t.Errorf("Valencia ES = %+v %v, want VLC", o, ok)
	}
	if o, ok := a.Resolve("Valencia", "ve", nil, nil); !ok || o.IATA != "VLN" {
		t.Errorf("Valencia VE = %+v %v, want VLN", o, ok)
	}
}

func TestResolveMetro(t *testing.T) {
	o, ok := airports(t).Resolve("London", "GB", nil, nil)
	if !ok || o.IATA != "LON" || o.Kind != KindMetro {
		t.Fatalf("London GB = %+v %v, want LON metro", o, ok)
	}
	// A metro is only offered in its own country: London, Ontario is not LON.
	if o, ok := airports(t).Resolve("London", "CA", nil, nil); ok && o.IATA == "LON" {
		t.Fatalf("London CA resolved to the UK metro")
	}
}

func TestResolvePrefersLargestThenNearest(t *testing.T) {
	lat, lon := 41.39, 2.17
	o, ok := airports(t).Resolve("Barcelona", "ES", &lat, &lon)
	if !ok || o.IATA != "BCN" {
		t.Fatalf("Barcelona = %+v %v, want BCN", o, ok)
	}
	if o, ok := airports(t).Resolve("barcelona", "", nil, nil); !ok || o.IATA != "BCN" {
		t.Fatalf("barcelona (no country) = %+v %v, want BCN", o, ok)
	}
}

func TestResolveUnknown(t *testing.T) {
	if o, ok := airports(t).Resolve("Atlantis", "", nil, nil); ok {
		t.Fatalf("Atlantis resolved to %+v", o)
	}
}

func TestResolveNameAmbiguous(t *testing.T) {
	a := airports(t)
	if code, ok := a.ResolveName("Valencia"); ok {
		t.Errorf("ResolveName(Valencia) = %q, want ambiguous", code)
	}
	if code, ok := a.ResolveName("Lisbon"); !ok || code != "LIS" {
		t.Errorf("ResolveName(Lisbon) = %q %v, want LIS", code, ok)
	}
	if code, ok := a.ResolveName("London"); !ok || code != "LON" {
		t.Errorf("ResolveName(London) = %q %v, want LON", code, ok)
	}
}

func TestPickAirportNearestAmongSameSize(t *testing.T) {
	near := airport{PlaceOption: PlaceOption{IATA: "NEA", Lat: 41.0, Lon: 2.0}, size: 2}
	far := airport{PlaceOption: PlaceOption{IATA: "FAR", Lat: 45.0, Lon: 9.0}, size: 2}
	small := airport{PlaceOption: PlaceOption{IATA: "SML", Lat: 41.0, Lon: 2.0}, size: 1}
	lat, lon := 41.01, 2.01
	for _, order := range [][]airport{{near, far, small}, {far, near, small}, {small, far, near}} {
		if got := pickAirport(order, &lat, &lon); got.IATA != "NEA" {
			t.Errorf("order %v: picked %s, want NEA", []string{order[0].IATA, order[1].IATA, order[2].IATA}, got.IATA)
		}
	}
	if got := pickAirport([]airport{small, far}, nil, nil); got.IATA != "FAR" {
		t.Errorf("no coords: picked %s, want the larger FAR", got.IATA)
	}
}
