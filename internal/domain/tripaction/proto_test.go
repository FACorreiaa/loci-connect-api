package tripaction

import (
	"testing"
	"time"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
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

func TestStopEditsRoundTrip(t *testing.T) {
	day, stop := uuid.NewString(), uuid.NewString()
	for _, a := range []Action{
		{Kind: KindAddStop, DayID: day, Stop: &trip.TripStop{Name: "Pastéis de Belém"}},
		{Kind: KindRemoveStop, StopID: stop},
		{Kind: KindReplaceStop, StopID: stop, Stop: &trip.TripStop{Name: "LX Factory"}},
		{Kind: KindMoveStop, StopID: stop, ToDayID: day, ToIndex: 2},
	} {
		pa := actionToProto(a)
		got, err := ActionFromProto(pa)
		require.NoError(t, err, a.Kind)
		require.Equal(t, a.Kind, got.Kind)
		require.Equal(t, a.DayID, got.DayID)
		require.Equal(t, a.StopID, got.StopID)
		require.Equal(t, a.ToDayID, got.ToDayID)
		require.Equal(t, a.ToIndex, got.ToIndex)
		if a.Stop != nil {
			require.Equal(t, a.Stop.Name, got.Stop.Name)
			require.Empty(t, pa.GetAddStop().GetStop().GetId()+pa.GetReplaceStop().GetReplacement().GetId(), "a new stop has no id yet")
		}
	}
	_, err := ActionFromProto(actionToProto(Action{Kind: KindSetDates, StartDate: "2026-11-01", EndDate: "2026-11-02"}))
	require.ErrorIs(t, err, ErrInvalid)
}

func TestViewToProto(t *testing.T) {
	v := &View{
		Proposal: Proposal{ID: uuid.New(), TripID: uuid.New(), Status: StatusApplied, Source: SourceEditor, BaseVersion: 7, CreatedAt: time.Now()},
		Tally:    Tally{Up: 2, Down: 1, Mine: -1},
	}
	got := ViewToProto(v)
	require.Equal(t, chatv1.ProposalStatus_PROPOSAL_STATUS_APPLIED, got.GetStatus())
	require.Equal(t, chatv1.ProposalSource_PROPOSAL_SOURCE_EDITOR, got.GetSource())
	require.Equal(t, int64(7), got.GetBaseVersion())
	require.Equal(t, []int32{2, 1, -1}, []int32{got.GetUpVotes(), got.GetDownVotes(), got.GetMyReaction()})
	require.NotNil(t, got.GetCreatedAt())
}
