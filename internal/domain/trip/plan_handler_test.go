package trip

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

func TestPlanRPCs_UnimplementedWithoutService(t *testing.T) {
	h := NewHandler(&memTrips{}, "", nil, nil)
	_, err := h.SetTripDates(authed(context.Background(), uuid.New()), connect.NewRequest(&tripv1.SetTripDatesRequest{
		TripId: uuid.NewString(), StartDate: "2026-11-12", EndDate: "2026-11-13",
	}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestPlanRPCs_ErrorCodes(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	h := NewHandler(mem, "", nil, nil).WithPlan(svc)
	ctx := authed(context.Background(), uid)

	_, err := h.SetTripDates(ctx, connect.NewRequest(&tripv1.SetTripDatesRequest{
		TripId: tid.String(), StartDate: "2026-11-12", EndDate: "2026-11-13", BaseVersion: 1,
	}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "stale version")

	_, err = h.SetStay(ctx, connect.NewRequest(&tripv1.SetStayRequest{
		TripId: tid.String(), BaseVersion: 3, Stay: &tripv1.TripStay{CityName: "Porto", Name: "X"},
	}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "city not on trip")

	_, err = h.RemoveFlight(ctx, connect.NewRequest(&tripv1.RemoveFlightRequest{
		TripId: tid.String(), BaseVersion: 3, FlightId: "not-a-uuid",
	}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = h.SetTripDates(context.Background(), connect.NewRequest(&tripv1.SetTripDatesRequest{
		TripId: tid.String(), StartDate: "2026-11-12", EndDate: "2026-11-13", BaseVersion: 3,
	}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "no user")
}

func TestPlanRPCs_SetDatesAndAddFlight(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	h := NewHandler(mem, "", nil, nil).WithPlan(svc)
	ctx := authed(context.Background(), uid)

	res, err := h.SetTripDates(ctx, connect.NewRequest(&tripv1.SetTripDatesRequest{
		TripId: tid.String(), StartDate: "2026-11-12", EndDate: "2026-11-13", BaseVersion: 3,
	}))
	require.NoError(t, err)
	require.Equal(t, "2026-11-12", res.Msg.GetStartDate())
	require.Equal(t, "2026-11-13", res.Msg.GetDays()[1].GetDate().AsTime().Format("2006-01-02"))

	iata := "LIS"
	res, err = h.AddFlight(ctx, connect.NewRequest(&tripv1.AddFlightRequest{
		TripId: tid.String(), BaseVersion: res.Msg.GetVersion(),
		Flight: &tripv1.TripFlight{
			Origin:      &tripv1.FlightPlace{Name: "Porto"},
			Destination: &tripv1.FlightPlace{Name: "Lisbon", Iata: &iata},
			DepartDate:  "2026-11-12", Passengers: 1,
			Links: []*tripv1.FlightLink{{Provider: "evil", Url: "https://phish.example"}},
		},
	}))
	require.NoError(t, err)
	require.Len(t, res.Msg.GetFlights(), 1)
	require.Equal(t, "google_flights", res.Msg.GetFlights()[0].GetLinks()[0].GetProvider())
	require.NotEmpty(t, res.Msg.GetFlights()[0].GetId())
	require.Equal(t, "LIS", res.Msg.GetFlights()[0].GetDestination().GetIata())
	require.Nil(t, res.Msg.GetFlights()[0].GetOrigin().Iata, "no code stays absent, not empty")
}

func TestBuildFlightLinks(t *testing.T) {
	svc, mem, uid, _ := newServiceFixture()
	h := NewHandler(mem, "", nil, nil).WithPlan(svc)
	ret := "2026-11-17"
	res, err := h.BuildFlightLinks(authed(context.Background(), uid), connect.NewRequest(&tripv1.BuildFlightLinksRequest{
		Origin:      &tripv1.FlightPlace{Name: "New York"},
		Destination: &tripv1.FlightPlace{Name: "Lisbon"},
		DepartDate:  "2026-11-10", ReturnDate: &ret, Passengers: 1,
	}))
	require.NoError(t, err)
	require.Len(t, res.Msg.GetLinks(), 1, "names only: Google, no Skyscanner")
}

func TestRedactForViewer_HidesPlanDetails(t *testing.T) {
	booking, notes, price, no, carrier := "https://hotel.example", "door code 1234", "€420", "TP 202", "TAP"
	p := tripToProto(&Trip{
		Stays: []TripStay{{CityName: "Lisbon", Name: "Hotel Avenida", BookingURL: &booking}},
		Flights: []TripFlight{{
			Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"),
			Notes: &notes, PriceText: &price, FlightNo: &no, Carrier: &carrier,
		}},
	})
	got := redactForViewer(p, false, nil)
	require.Nil(t, got.GetStays()[0].BookingUrl)
	f := got.GetFlights()[0]
	require.Nil(t, f.Notes)
	require.Nil(t, f.PriceText)
	require.Nil(t, f.FlightNo)
	require.Nil(t, f.Carrier)
	require.Equal(t, "Hotel Avenida", got.GetStays()[0].GetName(), "the stay itself is still shown")

	kept := redactForViewer(tripToProto(&Trip{
		Flights: []TripFlight{{Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"), Notes: &notes}},
	}), true, nil)
	require.Equal(t, notes, kept.GetFlights()[0].GetNotes(), "share_details on keeps them")
}
