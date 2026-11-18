package placecheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

func ptr[T any](v T) *T { return &v }

// 2026-04-14 is a Tuesday.
var tuesday = time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC)

func matched(hours string) *Verification {
	return &Verification{
		POIID: "p1", Matched: true, Source: SourceOSM, ExternalID: "way/11",
		Lat: 38.7251, Lon: -9.1137, HoursRaw: hours,
		CheckedAt: tuesday.Add(-24 * time.Hour), ExpiresAt: tuesday.Add(29 * 24 * time.Hour),
	}
}

func TestEvaluate(t *testing.T) {
	at := func(h, m int32) *int32 { return ptr(h*60 + m) }
	tests := []struct {
		name  string
		v     *Verification
		visit Visit
		want  Check
	}{
		{
			name: "never looked up",
			v:    nil, visit: Visit{Date: &tuesday, StartMinute: at(10, 0)},
			want: Check{Status: StatusUnchecked},
		},
		{
			name: "looked up and not found",
			v:    &Verification{POIID: "p1", CheckedAt: tuesday}, visit: Visit{Date: &tuesday},
			want: Check{Status: StatusNotFound, CheckedAt: tuesday},
		},
		{
			name: "found, the map has no hours",
			v:    matched(""), visit: Visit{Date: &tuesday, StartMinute: at(10, 0)},
			want: Check{Status: StatusChecked, Hours: HoursNotListed},
		},
		{
			name: "found, hours in a form that is not read",
			v:    matched("Apr-Oct: Mo-Su 10:00-18:00"), visit: Visit{Date: &tuesday, StartMinute: at(10, 0)},
			want: Check{Status: StatusChecked, Hours: HoursUnreadable},
		},
		{
			name: "found, the plan has no dates",
			v:    matched("Tu-Su 10:00-18:00"), visit: Visit{StartMinute: at(10, 0)},
			want: Check{Status: StatusChecked, Hours: HoursListed},
		},
		{
			name: "open for the whole visit",
			v:    matched("Tu-Su 10:00-18:00"), visit: Visit{Date: &tuesday, StartMinute: at(11, 0), DurationMinutes: ptr(int32(90))},
			want: Check{Status: StatusChecked, Hours: HoursOpen, OpensMinute: 600, ClosesMinute: 1080},
		},
		{
			name: "shut that day",
			v:    matched("We-Su 10:00-18:00"), visit: Visit{Date: &tuesday, StartMinute: at(11, 0)},
			want: Check{Status: StatusChecked, Hours: HoursShutThatDay},
		},
		{
			name: "opens after the planned start",
			v:    matched("Tu-Su 10:00-18:00"), visit: Visit{Date: &tuesday, StartMinute: at(9, 30), DurationMinutes: ptr(int32(60))},
			want: Check{Status: StatusChecked, Hours: HoursOpensLater, OpensMinute: 600, ClosesMinute: 1080},
		},
		{
			name: "closes before the visit ends",
			v:    matched("Tu-Su 10:00-18:00"), visit: Visit{Date: &tuesday, StartMinute: at(17, 0), DurationMinutes: ptr(int32(120))},
			want: Check{Status: StatusChecked, Hours: HoursClosesEarly, OpensMinute: 600, ClosesMinute: 1080},
		},
		{
			name: "already shut by the planned start",
			v:    matched("Tu-Su 10:00-18:00"), visit: Visit{Date: &tuesday, StartMinute: at(19, 0)},
			want: Check{Status: StatusChecked, Hours: HoursShutByThen},
		},
		{
			name: "no start time, opens that day",
			v:    matched("Tu-Su 10:00-18:00"), visit: Visit{Date: &tuesday},
			want: Check{Status: StatusChecked, Hours: HoursOpenThatDay},
		},
		{
			name: "no start time, shut that day",
			v:    matched("We-Su 10:00-18:00"), visit: Visit{Date: &tuesday},
			want: Check{Status: StatusChecked, Hours: HoursShutThatDay},
		},
		{
			name: "no start time, open only late: still opens that day",
			v:    matched("Tu-Su 19:00-23:00"), visit: Visit{Date: &tuesday},
			want: Check{Status: StatusChecked, Hours: HoursOpenThatDay},
		},
		{
			name: "no start time, only the tail of the night before: does not open that day",
			v:    matched("Mo 19:00-02:00"), visit: Visit{Date: &tuesday},
			want: Check{Status: StatusChecked, Hours: HoursShutThatDay},
		},
		{
			name: "holiday the hours close for",
			v:    matched("Tu-Su 10:00-18:00; PH off"), visit: Visit{Date: &tuesday, StartMinute: at(11, 0), Holiday: "Dia X"},
			want: Check{Status: StatusChecked, Hours: HoursShutThatDay, Holiday: "Dia X"},
		},
		{
			name: "holiday the hours say nothing about",
			v:    matched("Tu-Su 10:00-18:00"), visit: Visit{Date: &tuesday, StartMinute: at(11, 0), Holiday: "Dia X"},
			want: Check{Status: StatusChecked, Hours: HoursOpen, OpensMinute: 600, ClosesMinute: 1080, Holiday: "Dia X", HolidayHoursUnknown: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.v, tt.visit)
			// Position and check time are carried through from the
			// verification; compare the rest.
			if tt.v != nil && tt.v.Matched {
				if got.Lat != tt.v.Lat || got.Lon != tt.v.Lon || !got.CheckedAt.Equal(tt.v.CheckedAt) {
					t.Errorf("position or check time not carried: %+v", got)
				}
				got.Lat, got.Lon, got.CheckedAt = 0, 0, time.Time{}
			}
			if got != tt.want {
				t.Errorf("Evaluate = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestVerdict(t *testing.T) {
	var d Verdict
	for _, c := range []Check{
		{Status: StatusChecked, Hours: HoursOpen},
		{Status: StatusChecked, Hours: HoursNotListed},
		{Status: StatusChecked, Hours: HoursShutThatDay},
		{Status: StatusNotFound},
		{Status: StatusUnchecked},
	} {
		d.Tally(c)
	}
	if d != (Verdict{Checked: 3, Total: 5, NeedAttention: 1}) {
		t.Errorf("verdict = %+v", d)
	}
}

type fakeStore struct {
	rows   map[string]Verification
	saved  [][]Verification
	getErr error
}

func (f *fakeStore) Get(_ context.Context, ids []string) (map[string]Verification, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := map[string]Verification{}
	for _, id := range ids {
		if v, ok := f.rows[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func (f *fakeStore) Save(_ context.Context, vs []Verification) error {
	f.saved = append(f.saved, vs)
	for _, v := range vs {
		f.rows[v.POIID] = v
	}
	return nil
}

type fakeFinder struct {
	answers map[string]transit.PlaceMatch
	asked   [][]string
	// venues holds the names asked for as a business.
	venues map[string]bool
	err    error
}

func (f *fakeFinder) FindPlaces(_ context.Context, asks []transit.PlaceQuery, _ transit.LatLon) ([]transit.PlaceMatch, error) {
	names := make([]string, len(asks))
	out := make([]transit.PlaceMatch, len(asks))
	for i, a := range asks {
		names[i] = a.Name
		if a.Venue {
			if f.venues == nil {
				f.venues = map[string]bool{}
			}
			f.venues[a.Name] = true
		}
	}
	f.asked = append(f.asked, names)
	if f.err != nil {
		return out, f.err
	}
	for i, n := range names {
		out[i] = f.answers[n]
	}
	return out, nil
}

var lisbon = transit.LatLon{Lat: 38.7223, Lon: -9.1393}

func newService(store *fakeStore, finder *fakeFinder) *Service {
	s := NewService(finder, store)
	s.now = func() time.Time { return tuesday }
	return s
}

func TestCheckPOIs(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{
		"fresh": {POIID: "fresh", Matched: true, ExpiresAt: tuesday.Add(time.Hour)},
		"stale": {POIID: "stale", Matched: true, HoursRaw: "old", ExpiresAt: tuesday.Add(-time.Hour)},
	}}
	finder := &fakeFinder{answers: map[string]transit.PlaceMatch{
		"Torre de Belém": {Found: true, Place: transit.MapPlace{
			OSMType: "way", OSMID: 20, Name: "Torre de Belém", Lat: 38.6916, Lon: -9.2160,
			Kind: "tower", OpeningHours: "Tu-Su 09:30-18:00", Wikidata: "Q215003", Confidence: 1,
		}},
		"Museu do Fado": {Found: true, Place: transit.MapPlace{OSMType: "node", OSMID: 60, Name: "Museu do Fado", Lat: 38.715, Lon: -9.13, Confidence: 1}},
	}}
	got, err := newService(store, finder).CheckPOIs(context.Background(), lisbon, []POI{
		{ID: "fresh", Name: "Already Known"},
		{ID: "stale", Name: "Museu do Fado"},
		{ID: "new", Name: "  Torre de Belém "},
		{ID: "new", Name: "Torre de Belém"},
		{ID: "gone", Name: "Nowhere Café"},
		{ID: "nameless", Name: "  "},
		{ID: "", Name: "No Place Behind It"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Only what is missing or stale is asked for, each place once.
	if len(finder.asked) != 1 || len(finder.asked[0]) != 3 {
		t.Fatalf("asked = %v", finder.asked)
	}
	if finder.asked[0][0] != "Museu do Fado" || finder.asked[0][1] != "Torre de Belém" || finder.asked[0][2] != "Nowhere Café" {
		t.Errorf("asked = %v", finder.asked[0])
	}

	belem := got["new"]
	if !belem.Matched || belem.ExternalID != "way/20" || belem.WikidataID != "Q215003" || belem.HoursRaw != "Tu-Su 09:30-18:00" ||
		belem.Lat != 38.6916 || belem.Source != SourceOSM || !belem.CheckedAt.Equal(tuesday) || !belem.ExpiresAt.Equal(tuesday.Add(ttlMatched)) {
		t.Errorf("belem = %+v", belem)
	}
	if fado := got["stale"]; fado.HoursRaw != "" || fado.ExternalID != "node/60" {
		t.Errorf("stale row not replaced: %+v", fado)
	}
	if gone := got["gone"]; gone.Matched || !gone.ExpiresAt.Equal(tuesday.Add(ttlUnmatched)) {
		t.Errorf("gone = %+v", gone)
	}
	if _, ok := got["fresh"]; !ok {
		t.Error("fresh row missing from the answer")
	}
	if _, ok := got["nameless"]; ok {
		t.Error("a place with no name was given an answer")
	}
	if len(store.saved) != 1 || len(store.saved[0]) != 3 {
		t.Errorf("saved = %+v", store.saved)
	}

	// Everything is fresh now: the map is not asked again.
	if _, err := newService(store, finder).CheckPOIs(context.Background(), lisbon, []POI{{ID: "new", Name: "Torre de Belém"}, {ID: "gone", Name: "Nowhere Café"}}); err != nil {
		t.Fatal(err)
	}
	if len(finder.asked) != 1 {
		t.Errorf("map asked again for fresh places: %v", finder.asked)
	}
}

func TestCheckPOIsMapDownKeepsWhatIsHeld(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{
		"stale": {POIID: "stale", Matched: true, HoursRaw: "Tu-Su 10:00-18:00", ExpiresAt: tuesday.Add(-time.Hour)},
	}}
	finder := &fakeFinder{err: transit.ErrUnavailable}
	got, err := newService(store, finder).CheckPOIs(context.Background(), lisbon, []POI{{ID: "stale", Name: "Museu"}, {ID: "new", Name: "Torre"}})
	if !errors.Is(err, transit.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if v, ok := got["stale"]; !ok || v.HoursRaw != "Tu-Su 10:00-18:00" {
		t.Errorf("stale answer dropped: %+v", got)
	}
	if _, ok := got["new"]; ok {
		t.Error("an unanswered place was given an answer")
	}
	if len(store.saved) != 0 {
		t.Errorf("a failed lookup was saved: %+v", store.saved)
	}
}

func TestCheckPOIsNothingToDo(t *testing.T) {
	finder := &fakeFinder{}
	got, err := newService(&fakeStore{rows: map[string]Verification{}}, finder).CheckPOIs(context.Background(), lisbon, nil)
	if err != nil || len(got) != 0 || len(finder.asked) != 0 {
		t.Errorf("got %v, %v, asked %v", got, err, finder.asked)
	}
}

func TestCheckPOIsStoreError(t *testing.T) {
	boom := errors.New("boom")
	finder := &fakeFinder{}
	_, err := newService(&fakeStore{getErr: boom}, finder).CheckPOIs(context.Background(), lisbon, []POI{{ID: "a", Name: "A"}})
	if !errors.Is(err, boom) || len(finder.asked) != 0 {
		t.Errorf("err = %v, asked = %v", err, finder.asked)
	}
}

func TestVenueCategory(t *testing.T) {
	for category, want := range map[string]bool{
		"Restaurant":      true,
		"Fine Dining":     true,
		"Cafe":            true,
		"Café":            true,
		"Bar":             true,
		"Wine Bar":        true,
		"Hotel":           true,
		"Shopping":        true,
		"Museum":          false,
		"Historical Site": false,
		"Park":            false,
		"Barcelona":       false, // "bar" is a word here, not a prefix
		"Cultural":        false,
		"":                false,
	} {
		if got := venueCategory(category); got != want {
			t.Errorf("venueCategory(%q) = %v, want %v", category, got, want)
		}
	}
}

// A plan's category decides whether a business on the map can answer for the
// place, so it has to reach the map with the name.
func TestCheckPOIsAsksForBusinessesAsBusinesses(t *testing.T) {
	finder := &fakeFinder{}
	svc := newService(&fakeStore{rows: map[string]Verification{}}, finder)
	_, err := svc.CheckPOIs(context.Background(), lisbon, []POI{
		{ID: "a", Name: "Tasca do Chico", Category: "Restaurant"},
		{ID: "b", Name: "Plaza Mayor", Category: "Historical Site"},
		{ID: "c", Name: "Somewhere"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !finder.venues["Tasca do Chico"] || finder.venues["Plaza Mayor"] || finder.venues["Somewhere"] {
		t.Errorf("asked as businesses: %v", finder.venues)
	}
}
