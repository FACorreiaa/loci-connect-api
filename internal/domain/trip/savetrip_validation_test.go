package trip

import (
	"testing"

	"buf.build/go/protovalidate"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
)

// iOS Compare's "save as trip" and AddToTrip's "create a trip" build a
// TripDraft with no user_id and no timestamps: the handler takes the owner
// from the token and the repository stamps the times. The contract used to
// require all three, so the validation interceptor rejected those creates
// before the handler ever ran.
func TestSaveTripRequest_CreateNeedsNoOwnerOrTimestamps(t *testing.T) {
	req := &tripv1.SaveTripRequest{Trip: &tripv1.TripDraft{
		CityName: "Lisbon",
		Title:    "Lisbon weekend",
		Days: []*tripv1.TripDay{{
			DayNumber: 1,
			Stops:     []*tripv1.TripStop{{PoiId: "3f1c2b1e-0000-4000-8000-000000000001", Name: "Belém Tower", OrderIndex: 0}},
		}},
	}}
	if err := protovalidate.Validate(req); err != nil {
		t.Fatalf("a create without user_id/created_at/updated_at must validate: %v", err)
	}

	// Still bounded when present.
	req.Trip.UserId = string(make([]byte, 101))
	if err := protovalidate.Validate(req); err == nil {
		t.Fatal("an over-long user_id should still be rejected")
	}
}
