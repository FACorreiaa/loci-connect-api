package tripaction

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func comparedStays() []trip.TripStay {
	lat, lon, note := 38.72, -9.14, "385 euros with breakfast"
	return []trip.TripStay{
		{ID: uuid.New(), CityName: "lisboa?", Name: "Hotel Avenida", StarRating: "4", Address: "Av. da Liberdade 129", Lat: &lat, Lon: &lon, PriceNote: &note},
		{CityName: "Lisbon", Name: "  "},
		{CityName: "Lisbon", Name: "Verdi 254"},
	}
}

func TestProposeStayChoice_OffersTheComparedHotels(t *testing.T) {
	f := newFixture(t, `{"actions":[]}`, nil)
	sid := uuid.New()
	p, err := f.svc.ProposeStayChoice(context.Background(), f.uid, f.tid, sid, " lisbon ", "report-1", comparedStays())
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, KindChooseStay, p.Action.Kind)
	require.Equal(t, "Lisbon", p.Action.City, "the trip's own spelling")
	require.Equal(t, "report-1", p.Action.ReportID)
	require.Equal(t, "Pick the one to stay at in Lisbon.", p.Summary)
	require.Len(t, p.Options, 2, "a stay with no name is not offered")

	first := p.Options[0]
	require.Equal(t, "Hotel Avenida · 4★", first.Label)
	require.Equal(t, "385 euros with breakfast · Av. da Liberdade 129", first.Detail)
	require.Equal(t, "Lisbon", first.Stay.CityName, "saved under the trip's city, whatever the report called it")
	require.Equal(t, uuid.Nil, first.Stay.ID)
	require.NotNil(t, first.Stay.Lat)
	require.Equal(t, "Verdi 254", p.Options[1].Label)
	require.Empty(t, p.Options[1].Detail)

	// The card is a pick: applying it with a choice sets that stay.
	_, _, err = f.svc.Apply(context.Background(), f.uid, p.ID, nil, 3)
	require.ErrorIs(t, err, ErrNoOption)
	one := 1
	_, msg, err := f.svc.Apply(context.Background(), f.uid, p.ID, &one, 3)
	require.NoError(t, err)
	require.Equal(t, []string{"stay:Verdi 254"}, f.plans.applied)
	require.Equal(t, "Staying at Verdi 254 in Lisbon.", msg.Content)

	proto := ToProto(p)
	require.Equal(t, "Lisbon", proto.GetAction().GetChooseStay().GetCityName())
	require.Equal(t, "report-1", proto.GetAction().GetChooseStay().GetReportId())
	require.InDelta(t, 38.72, proto.GetOptions()[0].GetStay().GetLatitude(), 1e-9)
}

func TestProposeStayChoice_NothingToOffer(t *testing.T) {
	f := newFixture(t, `{"actions":[]}`, nil)
	ctx := context.Background()

	p, err := f.svc.ProposeStayChoice(ctx, f.uid, f.tid, uuid.Nil, "Barcelona", "r", comparedStays())
	require.NoError(t, err)
	require.Nil(t, p, "the city is not on the trip")

	p, err = f.svc.ProposeStayChoice(ctx, f.uid, f.tid, uuid.Nil, "Lisbon", "r", []trip.TripStay{{Name: ""}})
	require.NoError(t, err)
	require.Nil(t, p, "no stay fit to save")

	_, err = f.svc.ProposeStayChoice(ctx, uuid.New(), f.tid, uuid.Nil, "Lisbon", "r", comparedStays())
	require.ErrorIs(t, err, trip.ErrNotFound, "someone else's trip")
	require.Empty(t, f.store.byID)
}

// A model must never be able to name a hotel to save: the kind is refused
// wherever a model's or a client's action is vetted.
func TestChooseStayCannotComeFromAModel(t *testing.T) {
	f := newFixture(t, `{"actions":[{"kind":"choose_stay","city":"Lisbon","report_id":"x"}]}`, nil)
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.Nil, "stay at the Ritz")
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = f.svc.ProposeActions(context.Background(), f.uid, f.tid, uuid.Nil, []Action{{Kind: KindChooseStay, City: "Lisbon"}})
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, f.store.byID)
}

func TestHotelOptionsCarryWhereTheHotelIs(t *testing.T) {
	hotels := []locitypes.POIDetailedInfo{
		{ID: uuid.New(), Name: "Pestana Palace", StarRating: "5", Rating: 4.8, Latitude: 38.7, Longitude: -9.18, Address: "Rua Jau 54"},
		{ID: uuid.New(), Name: "Nowhere Inn", StarRating: "5", Rating: 4.1},
	}
	f := newFixture(t, `{"actions":[{"kind":"search_hotels","city":"Lisbon","min_stars":5,"max_stars":5}]}`, hotels)
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.Nil, "5 stars")
	require.NoError(t, err)
	require.Len(t, got[0].Options, 2)
	placed, unplaced := got[0].Options[0].Stay, got[0].Options[1].Stay
	require.True(t, placed.HasPin())
	require.Equal(t, "Rua Jau 54", placed.Address)
	require.False(t, unplaced.HasPin(), "a hotel with no position gets none, not 0,0")
}
