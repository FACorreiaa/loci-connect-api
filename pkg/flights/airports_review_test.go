package flights

import "testing"

func f(v float64) *float64 { return &v }

// The clients send the city's coordinates and no country (theirs is model
// prose). Position must beat size, or Birmingham UK lands in Alabama.
func TestResolveCoordinatesBeatSizeAcrossCountries(t *testing.T) {
	a := airports(t)
	cases := []struct {
		city     string
		lat, lon float64
		want     string
	}{
		{"Birmingham", 52.48, -1.89, "BHX"},
		{"Manchester", 53.48, -2.24, "MAN"},
		{"Córdoba", 37.88, -4.78, "ODB"},
		{"Santiago", 42.88, -8.54, "SCQ"},
		{"London", 42.98, -81.25, "YXU"},
		{"London", 51.51, -0.13, "LON"},
		{"Venice", 45.44, 12.33, "VCE"},
	}
	for _, c := range cases {
		o, ok := a.Resolve(c.city, "", f(c.lat), f(c.lon))
		if !ok || o.IATA != c.want {
			t.Errorf("Resolve(%s @%.2f,%.2f) = %q %v, want %s", c.city, c.lat, c.lon, o.IATA, ok, c.want)
		}
	}
}

func TestResolveTooFarFromAnyAirportIsNoMatch(t *testing.T) {
	// Coordinates in the Atlantic: nothing named Lisbon is near, so no guess.
	if o, ok := airports(t).Resolve("Lisbon", "", f(30.0), f(-40.0)); ok {
		t.Fatalf("resolved to %s", o.IATA)
	}
}

func TestResolveNoCoordsPrefersTheCityOwnAirport(t *testing.T) {
	a := airports(t)
	for city, want := range map[string]string{"Dubai": "DXB", "Venice": "VCE"} {
		if o, ok := a.Resolve(city, "", nil, nil); !ok || o.IATA != want {
			t.Errorf("Resolve(%s) = %q %v, want %s", city, o.IATA, ok, want)
		}
	}
}

func TestResolveNoCoordsNoCountryAmbiguousIsNoMatch(t *testing.T) {
	if o, ok := airports(t).Resolve("Birmingham", "", nil, nil); ok {
		t.Fatalf("Birmingham with no country or position resolved to %s", o.IATA)
	}
}

func TestResolveNameSeesQualifiedMunicipalities(t *testing.T) {
	a := airports(t)
	for _, name := range []string{"Birmingham", "Manchester", "Santiago"} {
		if code, ok := a.ResolveName(name); ok {
			t.Errorf("ResolveName(%s) = %s, want ambiguous", name, code)
		}
	}
}

// SHA is Hongqiao's own code, not a city code: Shanghai is not a metro, and
// the city resolves to its main airport.
func TestShanghaiIsNotAMetro(t *testing.T) {
	a := airports(t)
	o, ok := a.Resolve("Shanghai", "CN", f(31.23), f(121.47))
	if !ok || o.Kind != KindAirport {
		t.Fatalf("Shanghai = %+v %v, want an airport", o, ok)
	}
	seen := map[string]int{}
	for _, opt := range a.Search("shanghai", 8) {
		seen[opt.IATA]++
		if seen[opt.IATA] > 1 {
			t.Fatalf("Search(shanghai) lists %s twice", opt.IATA)
		}
	}
}

func TestSearchExactCityBeforePrefix(t *testing.T) {
	got := airports(t).Search("porto", 8)
	if len(got) == 0 || got[0].IATA != "OPO" {
		t.Fatalf("Search(porto) = %v, want OPO first", iatas(got))
	}
}

// Skyscanner names metro areas with its own 4-letter codes; Google keeps
// the IATA city code.
func TestSkyscannerUsesItsCityCodeForMetros(t *testing.T) {
	q := Query{Origin: Place{Name: "Lisbon", IATA: "LIS"}, Destination: Place{Name: "London", IATA: "LON"}, Depart: date("2026-12-04"), Passengers: 1}
	links := DeepLinks{}.Links(q)
	if len(links) != 2 {
		t.Fatalf("links = %+v", links)
	}
	if want := "/lis/lond/261204/"; !containsStr(links[1].URL, want) {
		t.Errorf("skyscanner URL %s, want %s", links[1].URL, want)
	}
	if !containsStr(links[0].URL, "Flights+to+LON") {
		t.Errorf("google URL %s, want the IATA city code", links[0].URL)
	}
}

func containsStr(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Porto is OPO's municipality; Porto Alegre and Porto Seguro only share a
// word in their names and must not make the bare name ambiguous.
func TestResolveNameMunicipalityOutranksNameWords(t *testing.T) {
	if code, ok := airports(t).ResolveName("Porto"); !ok || code != "OPO" {
		t.Fatalf("ResolveName(Porto) = %q %v, want OPO", code, ok)
	}
}
