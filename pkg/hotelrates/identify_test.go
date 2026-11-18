package hotelrates

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestIdentifyByCityAndCountry(t *testing.T) {
	f, p := newFake(t)
	f.hotels[1].Address, f.hotels[1].Stars = "Passeig de Gràcia 132", 5
	f.hotels[1].Rating, f.hotels[1].ReviewCount = 9.1, 1840

	h, ok, err := p.Identify(context.Background(), Query{Name: "Casa Fuster", City: "Barcelona", CountryCode: "es"})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if h.ID != "lp2" || h.Name != "Casa Fuster Hotel" || h.Address != "Passeig de Gràcia 132" || h.Stars != 5 {
		t.Errorf("hotel = %+v", h)
	}
	if h.Lat != 41.3980 || h.Lon != 2.1560 || h.CountryCode != "ES" {
		t.Errorf("position = %+v", h)
	}
	if h.Rating != 9.1 || h.ReviewCount != 1840 {
		t.Errorf("rating = %v over %d", h.Rating, h.ReviewCount)
	}

	// Asked again, it is served from the cache.
	hits := f.hotelHits.Load()
	if _, ok, _ := p.Identify(context.Background(), Query{Name: "Casa Fuster", City: "Barcelona", CountryCode: "ES"}); !ok {
		t.Fatal("cached identity lost")
	}
	if f.hotelHits.Load() != hits {
		t.Errorf("identify hit the partner again")
	}
}

func TestIdentifyNeverGuesses(t *testing.T) {
	f, p := newFake(t)
	cases := []struct {
		name string
		q    Query
	}{
		{"unknown name", Query{Name: "Verdi 254", City: "Barcelona", CountryCode: "ES"}},
		{"no city and no position", Query{Name: "Casa Fuster", CountryCode: "ES"}},
		{"only generic words", Query{Name: "Hotel Apartments", City: "Barcelona", CountryCode: "ES"}},
		{"right name, wrong city", Query{Name: "Casa Fuster", City: "Madrid", CountryCode: "ES"}},
		// A flat listed by its address is not the hotel on the same street.
		{"address is not the hotel sharing its street name", Query{Name: "Fuster 254", City: "Barcelona", CountryCode: "ES"}},
	}
	for _, c := range cases {
		if h, ok, err := p.Identify(context.Background(), c.q); ok || err != nil {
			t.Errorf("%s: got %+v ok=%v err=%v", c.name, h, ok, err)
		}
	}
	_ = f
}

func TestIdentifyRatingNeedsReviews(t *testing.T) {
	f, p := newFake(t)
	f.hotels[1].Rating = 9.1 // a score with nobody behind it
	h, ok, _ := p.Identify(context.Background(), Query{Name: "Casa Fuster", City: "Barcelona", CountryCode: "ES"})
	if !ok || h.Rating != 0 || h.ReviewCount != 0 {
		t.Errorf("hotel = %+v", h)
	}
}

func TestIdentifyProviderDownIsUnavailable(t *testing.T) {
	f, p := newFake(t)
	f.status = http.StatusBadGateway
	_, ok, err := p.Identify(context.Background(), Query{Name: "Casa Fuster", City: "Barcelona", CountryCode: "ES"})
	if ok || !errors.Is(err, ErrUnavailable) {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}

func TestFacilities(t *testing.T) {
	f, p := newFake(t)
	f.details = map[string]string{
		"lp2": `{"data":{"hotelFacilities":["Free WiFi","Lift"," ","free wifi"],"facilities":[{"name":"Air conditioning"},{"name":"Lift"}]}}`,
	}
	got, err := p.Facilities(context.Background(), "lp2", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "Free WiFi" || got[1] != "Lift" {
		t.Errorf("facilities = %q", got)
	}
	all, _ := p.Facilities(context.Background(), "lp2", 10)
	if len(all) != 3 || all[2] != "Air conditioning" {
		t.Errorf("all = %q", all)
	}
	if f.detailHits.Load() != 1 {
		t.Errorf("detail hits = %d, want 1 (cached)", f.detailHits.Load())
	}
	if _, err := p.Facilities(context.Background(), "nope", 5); !errors.Is(err, ErrUnavailable) {
		t.Errorf("unknown hotel err = %v", err)
	}
}
