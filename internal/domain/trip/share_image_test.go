package trip

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
)

type fakeShareRepo struct {
	SharingRepository
	trips map[string]*Trip
}

func (f fakeShareRepo) TripByShareCode(_ context.Context, code string) (*Trip, error) {
	if t, ok := f.trips[code]; ok {
		return t, nil
	}
	return nil, ErrNotFound
}

type fakeShareGraph struct{ SocialGraph }

func (fakeShareGraph) PublicUsers(context.Context, []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error) {
	return nil, nil
}

func shareImages(trips map[string]*Trip) *ShareImages {
	return NewHandler(&fakeTripRepo{}, "", nil, nil).
		WithSharing(fakeShareRepo{trips: trips}, fakeShareGraph{}).
		ShareImages(nil)
}

func getImage(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestShareImageServesAPublicTripAsAPNG(t *testing.T) {
	lat, lon := 38.72, -9.14
	h := shareImages(map[string]*Trip{
		"open": {
			ID: uuid.New(), UserID: uuid.New(), Title: "Lisbon, slowly", CityName: "Lisbon",
			Visibility: VisibilityPublic, Version: 3,
			Days: []TripDay{{DayNumber: 1, Stops: []TripStop{{Name: "Torre de Belém", Lat: &lat, Lon: &lon}}}},
		},
	})

	rec := getImage(t, h, http.MethodGet, "/trip-img/open/og.png")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("cache control = %q", cc)
	}
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 1200 || b.Dy() != 630 {
		t.Errorf("size = %dx%d, want 1200x630", b.Dx(), b.Dy())
	}

	// A second request is served from the cache, and HEAD sends no body.
	if len(h.cache) != 1 {
		t.Errorf("cache holds %d images, want 1", len(h.cache))
	}
	head := getImage(t, h, http.MethodHead, "/trip-img/open/og.png")
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Errorf("HEAD = %d with %d bytes, want 200 and none", head.Code, head.Body.Len())
	}
}

func TestShareImageHidesWhatTheLinkDoesNotShow(t *testing.T) {
	h := shareImages(map[string]*Trip{
		"private": {ID: uuid.New(), UserID: uuid.New(), Title: "Mine", Visibility: VisibilityPrivate},
	})
	for name, path := range map[string]string{
		"a private trip":      "/trip-img/private/og.png",
		"an unknown code":     "/trip-img/nope/og.png",
		"another file":        "/trip-img/private/card.png",
		"no file":             "/trip-img/private",
		"a path under a code": "/trip-img/a/b/og.png",
	} {
		if rec := getImage(t, h, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, rec.Code)
		}
	}
	if rec := getImage(t, h, http.MethodPost, "/trip-img/private/og.png"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", rec.Code)
	}
}

func TestShareImageIsNotFoundWhenSharingIsOff(t *testing.T) {
	h := NewHandler(&fakeTripRepo{}, "", nil, nil).ShareImages(nil)
	if rec := getImage(t, h, http.MethodGet, "/trip-img/open/og.png"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestShareCardCountsOnlyWhatTheMapAnswered(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	check := func(s tripv1.StopCheckStatus) *tripv1.StopCheck { return &tripv1.StopCheck{Status: s} }
	card := shareCard(&tripv1.TripDraft{
		Title: "Lisbon", CityName: "Lisbon",
		Days: []*tripv1.TripDay{
			{Stops: []*tripv1.TripStop{
				{Latitude: f(38.70), Longitude: f(-9.20), Check: check(tripv1.StopCheckStatus_STOP_CHECK_STATUS_CHECKED)},
				{Latitude: f(38.72), Longitude: f(-9.14), Check: check(tripv1.StopCheckStatus_STOP_CHECK_STATUS_NOT_FOUND)},
			}},
			{Stops: []*tripv1.TripStop{{Name: "Not looked up, no position"}}},
		},
	})
	if card.Days != 2 || card.Stops != 3 || card.Checked != 1 || card.Answered != 2 || len(card.Route) != 2 {
		t.Errorf("card = %+v, want 2 days, 3 stops, 1 of 2 checked, 2 route points", card)
	}
}
