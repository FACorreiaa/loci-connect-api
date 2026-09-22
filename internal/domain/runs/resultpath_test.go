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
		{"itinerary", "/itinerary?sessionId=" + sid.String() + "&cityName=Crete&domain=itinerary&tripId=" + trip.String(), "itinerary", trip},
	}
	for _, c := range cases {
		got, route, _ := ResultPath(c.domain, sid, "Crete", c.trip)
		require.Equal(t, c.want, got, c.domain)
		require.Equal(t, c.route, route, c.domain)
	}
	got, _, _ := ResultPath("itinerary", sid, "Rio de Janeiro", uuid.Nil)
	require.Contains(t, got, "cityName=Rio+de+Janeiro")
}
