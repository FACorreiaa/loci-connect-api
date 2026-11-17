package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/hotelcompare"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

func comparisonEvent() hotelcompare.Event {
	lat, lon, note, page, one := 41.4105, 2.156, "385 euros with breakfast", "https://www.booking.com/hotel/es/verdi-254.html", 0
	return hotelcompare.Event{
		StayProposalID: "prop-1",
		Report: &hotelcompare.Report{
			ID: uuid.New(), City: "Barcelona", Verdict: "Take the first.", Recommended: &one,
			GeneratedAt: time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC),
			Hotels: []hotelcompare.Hotel{{
				Listing: hotelcompare.Listing{
					Link:   hotelcompare.Link{URL: "https://www.booking.com/Share-gpcrrRv", Note: note},
					Source: "booking.com", Resolved: true, Name: "Verdi 254",
				},
				Located:    true,
				Stay:       trip.TripStay{CityName: "Barcelona", Name: "Verdi 254", Lat: &lat, Lon: &lon, PriceNote: &note, BookingURL: &page},
				Stops:      []transit.Stop{{Name: "Lesseps", Mode: transit.ModeMetro, Lines: []string{"L3"}, DistanceM: 520}},
				Highlights: []transit.Highlight{{Kind: "eat", Count: 14, NearestName: "Cafe Flanders", NearestM: 30}},
				Pros:       []string{"Breakfast included"},
			}},
			Rows:    []hotelcompare.Row{{Label: "You said", Values: []string{note}}},
			Caveats: []string{"Walking times are estimates."},
			Sources: []hotelcompare.Source{{Title: "Staying in Gràcia", URL: "https://a.example/gracia", Publisher: "a.example"}},
		},
	}
}

func checkComparison(t *testing.T, resp *chatv1.StreamEvent) {
	t.Helper()
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_HOTEL_COMPARISON, resp.GetEventType())
	rep := resp.GetHotelComparison().GetReport()
	require.Equal(t, "prop-1", rep.GetStayProposalId())
	require.Equal(t, "Barcelona", rep.GetCityName())
	require.EqualValues(t, 0, rep.GetRecommendedIndex())
	require.NotNil(t, rep.RecommendedIndex)
	require.Equal(t, transit.Attribution, rep.GetAttribution())

	h := rep.GetHotels()[0]
	require.False(t, h.GetIdentified())
	require.Equal(t, "385 euros with breakfast", h.GetUserNote())
	require.Equal(t, "https://www.booking.com/Share-gpcrrRv", h.GetSourceUrl())
	stay := h.GetStay()
	require.Equal(t, "Verdi 254", stay.GetName())
	require.InDelta(t, 41.4105, stay.GetLatitude(), 1e-9)
	require.Equal(t, "385 euros with breakfast", stay.GetPriceNote())
	require.Equal(t, "https://www.booking.com/hotel/es/verdi-254.html", stay.GetBookingUrl())

	stop := h.GetNearbyStops()[0]
	require.Equal(t, tripv1.TransitMode_TRANSIT_MODE_METRO, stop.GetMode())
	require.Equal(t, []string{"L3"}, stop.GetLines())
	require.EqualValues(t, 9, stop.GetWalkMinutes())
	require.False(t, stop.GetRouted(), "a report's walking times are estimates")
	require.Equal(t, "places to eat", h.GetNearby()[0].GetLabel())
	require.EqualValues(t, 14, h.GetNearby()[0].GetCount())

	require.Equal(t, "You said", rep.GetRows()[0].GetLabel())
	require.Equal(t, "https://a.example/gracia", rep.GetSources()[0].GetUrl())
}

func TestMapEventToProto_HotelComparison(t *testing.T) {
	resp, err := (&ChatHandler{}).mapEventToProto(context.Background(),
		locitypes.StreamEvent{Type: locitypes.EventTypeHotelComparison, EventID: "e1", Data: comparisonEvent()}, uuid.New())
	require.NoError(t, err)
	checkComparison(t, resp)
}

// A resumed stream replays events from a buffer that stored them as JSON:
// the report has to survive that trip unchanged.
func TestMapEventToProto_HotelComparisonSurvivesTheResumeBuffer(t *testing.T) {
	raw, err := json.Marshal(comparisonEvent())
	require.NoError(t, err)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored))
	resp, err := (&ChatHandler{}).mapEventToProto(context.Background(),
		locitypes.StreamEvent{Type: locitypes.EventTypeHotelComparison, EventID: "e1", Data: stored}, uuid.New())
	require.NoError(t, err)
	checkComparison(t, resp)
}

func TestMapEventToProto_HotelComparisonWithoutAReportIsAnError(t *testing.T) {
	_, err := (&ChatHandler{}).mapEventToProto(context.Background(),
		locitypes.StreamEvent{Type: locitypes.EventTypeHotelComparison, EventID: "e1", Data: map[string]any{}}, uuid.New())
	require.Error(t, err)
}

func TestHotelComparisonProgressReadsAfterTheAssistantsName(t *testing.T) {
	for status, want := range map[string]string{
		hotelcompare.StageLinks: "opening the links", hotelcompare.StageLookup: "finding the hotels", hotelcompare.StageWrite: "comparing the hotels",
	} {
		got := progressStage(locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: map[string]any{"status": status}})
		require.Equal(t, want, got)
		require.LessOrEqual(t, len(got), maxProgressStageLen)
	}
}
