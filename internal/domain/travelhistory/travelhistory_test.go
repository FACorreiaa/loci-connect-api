package travelhistory

import (
	"math"
	"testing"
	"time"

	travelhistoryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ptr[T any](v T) *T { return &v }

func TestSourceValid(t *testing.T) {
	for _, s := range []Source{SourceTrip, SourceVisitEvent, SourceManual, SourceBackfill} {
		if !s.Valid() {
			t.Errorf("Source(%q).Valid() = false, want true", s)
		}
	}
	for _, s := range []Source{"", "visited", "TRIP", "unknown"} {
		if s.Valid() {
			t.Errorf("Source(%q).Valid() = true, want false", s)
		}
	}
}

func TestVisitInputValidate(t *testing.T) {
	base := func() VisitInput {
		return VisitInput{CityName: "Porto", Latitude: 41.1579, Longitude: -8.6291}
	}

	tests := []struct {
		name    string
		mutate  func(*VisitInput)
		wantErr bool
	}{
		{"valid", func(*VisitInput) {}, false},
		{"empty city name", func(in *VisitInput) { in.CityName = "" }, true},
		{"lat too high", func(in *VisitInput) { in.Latitude = 90.1 }, true},
		{"lat too low", func(in *VisitInput) { in.Latitude = -90.1 }, true},
		{"lon too high", func(in *VisitInput) { in.Longitude = 180.1 }, true},
		{"lon too low", func(in *VisitInput) { in.Longitude = -180.1 }, true},
		{"lat NaN", func(in *VisitInput) { in.Latitude = math.NaN() }, true},
		{"lon NaN", func(in *VisitInput) { in.Longitude = math.NaN() }, true},
		// 0,0 is the Gulf of Guinea. It is almost always a missing coordinate,
		// and letting it through would put a dot on the globe for a place the
		// traveller has never been.
		{"null island rejected", func(in *VisitInput) { in.Latitude, in.Longitude = 0, 0 }, true},
		{"lat 0 alone is fine", func(in *VisitInput) { in.Latitude = 0 }, false},
		{"boundary lat 90", func(in *VisitInput) { in.Latitude = 90 }, false},
		{"boundary lon -180", func(in *VisitInput) { in.Longitude = -180 }, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mutate(&in)
			err := in.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestVisitInputNormalise(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	t.Run("trims and defaults", func(t *testing.T) {
		in := VisitInput{CityName: "  Porto  ", Country: " Portugal ", POIID: " p1 ", POIName: " Livraria "}
		in.Normalise(now)

		if in.CityName != "Porto" {
			t.Errorf("CityName = %q, want %q", in.CityName, "Porto")
		}
		if in.Country != "Portugal" {
			t.Errorf("Country = %q, want %q", in.Country, "Portugal")
		}
		if in.POIID != "p1" || in.POIName != "Livraria" {
			t.Errorf("POI fields not trimmed: %q / %q", in.POIID, in.POIName)
		}
		if in.Source != SourceManual {
			t.Errorf("Source = %q, want %q for an unset source", in.Source, SourceManual)
		}
		if !in.VisitedAt.Equal(now) {
			t.Errorf("VisitedAt = %v, want %v", in.VisitedAt, now)
		}
	})

	t.Run("keeps an explicit source and time", func(t *testing.T) {
		earlier := now.Add(-48 * time.Hour)
		in := VisitInput{CityName: "Porto", Source: SourceTrip, VisitedAt: earlier}
		in.Normalise(now)

		if in.Source != SourceTrip {
			t.Errorf("Source = %q, want %q", in.Source, SourceTrip)
		}
		if !in.VisitedAt.Equal(earlier) {
			t.Errorf("VisitedAt = %v, want %v", in.VisitedAt, earlier)
		}
	})

	t.Run("country code is upper-cased", func(t *testing.T) {
		in := VisitInput{CityName: "Porto", CountryCode: ptr(" pt ")}
		in.Normalise(now)
		if in.CountryCode == nil || *in.CountryCode != "PT" {
			t.Fatalf("CountryCode = %v, want PT", in.CountryCode)
		}
	})

	t.Run("malformed country code is dropped, not truncated", func(t *testing.T) {
		// A partial code would render as a wrong flag, which is worse than a
		// missing one — the whole point of this domain is no fabricated data.
		for _, bad := range []string{"P", "PRT", ""} {
			in := VisitInput{CityName: "Porto", CountryCode: ptr(bad)}
			in.Normalise(now)
			if in.CountryCode != nil {
				t.Errorf("CountryCode for %q = %v, want nil", bad, *in.CountryCode)
			}
		}
	})
}

func TestHaversineKm(t *testing.T) {
	tests := []struct {
		name                           string
		lat1, lon1, lat2, lon2, wantKm float64
		tolerance                      float64
	}{
		{"identical points", 41.1579, -8.6291, 41.1579, -8.6291, 0, 0.001},
		// Lisbon -> Tokyo, the long-arc case the globe renderer must not draw
		// as a straight chord.
		{"Lisbon to Tokyo", 38.7223, -9.1393, 35.6762, 139.6503, 11150, 60},
		// Auckland -> Santiago crosses the antimeridian; distance must not
		// blow up to the long way round.
		{"Auckland to Santiago", -36.8485, 174.7633, -33.4489, -70.6693, 9660, 60},
		{"Porto to Lisbon", 41.1579, -8.6291, 38.7223, -9.1393, 274, 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HaversineKm(tc.lat1, tc.lon1, tc.lat2, tc.lon2)
			if math.Abs(got-tc.wantKm) > tc.tolerance {
				t.Errorf("HaversineKm() = %.2f km, want %.2f +/- %.2f", got, tc.wantKm, tc.tolerance)
			}
		})
	}

	t.Run("symmetric", func(t *testing.T) {
		a := HaversineKm(41.1579, -8.6291, 35.6762, 139.6503)
		b := HaversineKm(35.6762, 139.6503, 41.1579, -8.6291)
		if math.Abs(a-b) > 0.001 {
			t.Errorf("distance not symmetric: %.4f vs %.4f", a, b)
		}
	})
}

func TestTotalDistanceKm(t *testing.T) {
	porto := &VisitedCity{Latitude: 41.1579, Longitude: -8.6291}
	lisbon := &VisitedCity{Latitude: 38.7223, Longitude: -9.1393}
	madrid := &VisitedCity{Latitude: 40.4168, Longitude: -3.7038}

	t.Run("empty and single", func(t *testing.T) {
		if got := TotalDistanceKm(nil); got != 0 {
			t.Errorf("TotalDistanceKm(nil) = %v, want 0", got)
		}
		if got := TotalDistanceKm([]*VisitedCity{porto}); got != 0 {
			t.Errorf("one city = %v, want 0", got)
		}
	})

	t.Run("sums consecutive hops", func(t *testing.T) {
		got := TotalDistanceKm([]*VisitedCity{porto, lisbon, madrid})
		want := HaversineKm(41.1579, -8.6291, 38.7223, -9.1393) +
			HaversineKm(38.7223, -9.1393, 40.4168, -3.7038)
		if math.Abs(got-want) > 0.001 {
			t.Errorf("TotalDistanceKm() = %.3f, want %.3f", got, want)
		}
	})

	t.Run("skips nils rather than panicking", func(t *testing.T) {
		got := TotalDistanceKm([]*VisitedCity{porto, nil, lisbon})
		if got != 0 {
			// Both hops touch the nil, so neither is counted.
			t.Errorf("TotalDistanceKm() with nil = %v, want 0", got)
		}
	})
}

func TestNopRecorder(t *testing.T) {
	// The contract is that a visit never fails the caller; a nil repo must
	// therefore be usable without a nil check at every call site.
	r := NewRecorder(nil, nil)
	if _, ok := r.(NopRecorder); !ok {
		t.Fatalf("NewRecorder(nil) = %T, want NopRecorder", r)
	}
	r.RecordVisit(t.Context(), uuid.New(), VisitInput{CityName: "Porto"})
}

func TestPageBounds(t *testing.T) {
	tests := []struct {
		name                  string
		page, size            int32
		wantLimit, wantOffset int
	}{
		{"defaults", 0, 0, 50, 0},
		{"first page", 1, 20, 20, 0},
		{"third page", 3, 20, 20, 40},
		{"negative page clamps to first", -5, 10, 10, 0},
		{"oversized page size clamps", 1, 5000, 500, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset := pageBounds(tc.page, tc.size)
			if limit != tc.wantLimit || offset != tc.wantOffset {
				t.Errorf("pageBounds(%d, %d) = (%d, %d), want (%d, %d)",
					tc.page, tc.size, limit, offset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}

func TestSourceToProto(t *testing.T) {
	cases := map[Source]travelhistoryv1.VisitSource{
		SourceTrip:       travelhistoryv1.VisitSource_VISIT_SOURCE_TRIP,
		SourceVisitEvent: travelhistoryv1.VisitSource_VISIT_SOURCE_VISIT_EVENT,
		SourceManual:     travelhistoryv1.VisitSource_VISIT_SOURCE_MANUAL,
		SourceBackfill:   travelhistoryv1.VisitSource_VISIT_SOURCE_BACKFILL,
		Source("nope"):   travelhistoryv1.VisitSource_VISIT_SOURCE_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := sourceToProto(in); got != want {
			t.Errorf("sourceToProto(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTimestampOrNilKeepsZeroAsNil(t *testing.T) {
	// A zero time must not become the Unix epoch: "no date" and
	// "1 January 1970" render very differently on a travel history.
	if got := timestampOrNil(time.Time{}); got != nil {
		t.Errorf("timestampOrNil(zero) = %v, want nil", got)
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if got := timestampOrNil(now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("timestampOrNil(%v) = %v, want the same instant", now, got)
	}
	if got := timestampPtrOrNil(nil); got != nil {
		t.Errorf("timestampPtrOrNil(nil) = %v, want nil", got)
	}
}

func TestVisitedCityToProto(t *testing.T) {
	id, cityID, tripID := uuid.New(), uuid.New(), uuid.New()
	first := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)

	got := visitedCityToProto(&VisitedCity{
		ID: id, CityID: &cityID, CityName: "Porto", Country: "Portugal",
		CountryCode: ptr("PT"), Latitude: 41.1579, Longitude: -8.6291,
		Source: SourceTrip, TripID: &tripID,
		FirstVisitAt: first, LastVisitAt: last, VisitCount: 3,
	})

	if got.GetId() != id.String() || got.GetCityId() != cityID.String() || got.GetTripId() != tripID.String() {
		t.Errorf("ids not mapped: %+v", got)
	}
	if got.GetCityName() != "Porto" || got.GetCountry() != "Portugal" || got.GetCountryCode() != "PT" {
		t.Errorf("names not mapped: %+v", got)
	}
	if got.GetVisitCount() != 3 || got.GetSource() != travelhistoryv1.VisitSource_VISIT_SOURCE_TRIP {
		t.Errorf("source/count not mapped: %+v", got)
	}

	if visitedCityToProto(nil) != nil {
		t.Error("visitedCityToProto(nil) should be nil")
	}

	t.Run("absent optional ids map to empty strings, not the nil UUID", func(t *testing.T) {
		bare := visitedCityToProto(&VisitedCity{ID: id, CityName: "Porto"})
		if bare.GetCityId() != "" || bare.GetTripId() != "" {
			t.Errorf("nil ids leaked: city=%q trip=%q", bare.GetCityId(), bare.GetTripId())
		}
	})
}

func TestSummaryToProtoCarriesPrevPeriod(t *testing.T) {
	// These fields are the whole reason the rail can show a real delta rather
	// than an invented trend arrow; a mapping slip here silently zeroes them.
	s := &Summary{
		CitiesVisited: 12, CountriesVisited: 5, POIsVisited: 40,
		DistanceKm: 1234.5, TripsCompleted: 3, PeriodDays: 365,
		CitiesVisitedPrev: 9, CountriesVisitedPrev: 4, POIsVisitedPrev: 30,
	}
	got := summaryToProto(s)

	if got.GetCitiesVisitedPrevPeriod() != 9 {
		t.Errorf("CitiesVisitedPrevPeriod = %d, want 9", got.GetCitiesVisitedPrevPeriod())
	}
	if got.GetCountriesVisitedPrevPeriod() != 4 {
		t.Errorf("CountriesVisitedPrevPeriod = %d, want 4", got.GetCountriesVisitedPrevPeriod())
	}
	if got.GetPoisVisitedPrevPeriod() != 30 {
		t.Errorf("PoisVisitedPrevPeriod = %d, want 30", got.GetPoisVisitedPrevPeriod())
	}
	if got.GetPeriodDays() != 365 {
		t.Errorf("PeriodDays = %d, want 365", got.GetPeriodDays())
	}
	if summaryToProto(nil) != nil {
		t.Error("summaryToProto(nil) should be nil")
	}
}

func TestVisitInputFromProto(t *testing.T) {
	cityID, tripID := uuid.New(), uuid.New()
	at := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	t.Run("maps a full request", func(t *testing.T) {
		in, err := visitInputFromProto(&travelhistoryv1.RecordVisitRequest{
			CityId: cityID.String(), CityName: "Porto",
			Latitude: 41.1579, Longitude: -8.6291,
			TripId: tripID.String(), PoiId: ptr("poi-1"), PoiName: ptr("Livraria Lello"),
			VisitedAt: timestamppb.New(at),
		})
		if err != nil {
			t.Fatalf("visitInputFromProto() error = %v", err)
		}
		if in.CityID == nil || *in.CityID != cityID {
			t.Errorf("CityID = %v, want %v", in.CityID, cityID)
		}
		if in.TripID == nil || *in.TripID != tripID {
			t.Errorf("TripID = %v, want %v", in.TripID, tripID)
		}
		if !in.VisitedAt.Equal(at) {
			t.Errorf("VisitedAt = %v, want %v", in.VisitedAt, at)
		}
		// A visit through the public RPC is the traveller telling us directly.
		if in.Source != SourceManual {
			t.Errorf("Source = %q, want %q", in.Source, SourceManual)
		}
	})

	t.Run("empty ids are absent, not errors", func(t *testing.T) {
		in, err := visitInputFromProto(&travelhistoryv1.RecordVisitRequest{
			CityName: "Porto", Latitude: 41.1579, Longitude: -8.6291,
		})
		if err != nil {
			t.Fatalf("visitInputFromProto() error = %v", err)
		}
		if in.CityID != nil || in.TripID != nil {
			t.Errorf("empty ids should map to nil, got city=%v trip=%v", in.CityID, in.TripID)
		}
	})

	t.Run("malformed ids are rejected", func(t *testing.T) {
		if _, err := visitInputFromProto(&travelhistoryv1.RecordVisitRequest{
			CityId: "not-a-uuid", CityName: "Porto",
		}); err == nil {
			t.Error("expected an error for a malformed city id")
		}
	})
}
