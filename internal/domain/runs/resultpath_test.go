package runs

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestResultPath(t *testing.T) {
	sid := uuid.MustParse("3043fb3f-15e8-461f-86de-6426fb389df2")
	trip := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cases := []struct {
		domain, want, route string
		trip                uuid.UUID
	}{
		{"itinerary", "/itinerary?sessionId=" + sid.String() + "&cityName=Crete&domain=itinerary", "itinerary", uuid.Nil},
		{"general", "/itinerary?sessionId=" + sid.String() + "&cityName=Crete&domain=itinerary", "itinerary", uuid.Nil},
		{"accommodation", "/hotels?sessionId=" + sid.String() + "&cityName=Crete&domain=hotels", "hotels", uuid.Nil},
		{"dining", "/restaurants?sessionId=" + sid.String() + "&cityName=Crete&domain=restaurants", "restaurants", uuid.Nil},
		{"activities", "/activities?sessionId=" + sid.String() + "&cityName=Crete&domain=activities", "activities", uuid.Nil},
		{"nearby", "/nearme?sessionId=" + sid.String() + "&cityName=Crete&domain=nearme", "nearme", uuid.Nil},
		{"gastronomy", "/gastronomy?sessionId=" + sid.String() + "&cityName=Crete&domain=gastronomy", "gastronomy", uuid.Nil},
		{"accommodation", "/hotels?sessionId=" + sid.String() + "&cityName=Crete&domain=hotels", "hotels", trip},
		{"dining", "/restaurants?sessionId=" + sid.String() + "&cityName=Crete&domain=restaurants", "restaurants", trip},
		{"activities", "/activities?sessionId=" + sid.String() + "&cityName=Crete&domain=activities", "activities", trip},
		{"nearby", "/nearme?sessionId=" + sid.String() + "&cityName=Crete&domain=nearme", "nearme", trip},
		{"itinerary", "/itinerary?sessionId=" + sid.String() + "&cityName=Crete&domain=itinerary&tripId=" + trip.String(), "itinerary", trip},
	}
	for _, c := range cases {
		got, route, query := ResultPath(c.domain, sid, "Crete", c.trip)
		require.Equal(t, c.want, got, c.domain)
		require.Equal(t, c.route, route, c.domain)
		// Verify tripId only appears for itinerary routes
		if route != "itinerary" {
			require.NotContains(t, got, "tripId", "tripId should not appear for non-itinerary routes")
			_, hasTripID := query["tripId"]
			require.False(t, hasTripID, "tripId should not be in query map for non-itinerary routes")
		} else if c.trip != uuid.Nil {
			require.Contains(t, got, "tripId="+c.trip.String(), "itinerary route with tripId should include tripId in path")
			require.Equal(t, c.trip.String(), query["tripId"], "itinerary route with tripId should have tripId in query map")
		}
	}
	got, _, _ := ResultPath("itinerary", sid, "Rio de Janeiro", uuid.Nil)
	require.Contains(t, got, "cityName=Rio+de+Janeiro")
}
