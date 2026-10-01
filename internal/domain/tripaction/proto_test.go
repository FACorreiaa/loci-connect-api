package tripaction

import (
	"testing"
	"time"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

func TestToProto(t *testing.T) {
	p := &Proposal{
		ID: uuid.New(), TripID: uuid.New(), Summary: "4★ hotels in Lisbon",
		Action:    Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 4, MaxStars: 4},
		Options:   []Option{{Label: "Hotel Avenida · 4★", Stay: &trip.TripStay{CityName: "Lisbon", Name: "Hotel Avenida"}}},
		ExpiresAt: time.Now(),
	}
	got := ToProto(p)
	require.Equal(t, p.ID.String(), got.GetId())
	require.EqualValues(t, 4, got.GetAction().GetSearchHotels().GetMinStars())
	require.Equal(t, "Hotel Avenida", got.GetOptions()[0].GetStay().GetName())

	f := ToProto(&Proposal{Action: Action{Kind: KindSearchFlights, Origin: Place{Name: "NYC"}, Destination: Place{Name: "Lisbon", IATA: "LIS"}, Depart: "2026-11-12", Passengers: 2, Cabin: "business"}})
	sf := f.GetAction().GetSearchFlights()
	require.Nil(t, sf.GetOrigin().Iata)
	require.Equal(t, "LIS", sf.GetDestination().GetIata())
	require.Nil(t, sf.ReturnDate)
	require.Equal(t, tripv1.FlightCabin_FLIGHT_CABIN_BUSINESS, sf.GetCabin())
}
