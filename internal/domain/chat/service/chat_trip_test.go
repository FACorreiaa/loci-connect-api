package service

import (
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

func TestBuildTripFromCityResponse(t *testing.T) {
	cc := &common.ChatContext{
		UserID:    uuid.New(),
		SessionID: uuid.New(),
		CityName:  "Lisbon",
	}
	pois := make([]locitypes.POIDetailedInfo, 5)
	for i := range pois {
		pois[i] = locitypes.POIDetailedInfo{
			ID:       uuid.New(),
			Name:     "POI",
			Category: "museum",
			Rating:   4.5,
		}
	}
	data := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{PointsOfInterest: pois},
	}

	tr := buildTripFromCityResponse(cc, data)
	if tr == nil {
		t.Fatal("expected a trip")
	}
	if tr.UserID != cc.UserID || tr.CityName != "Lisbon" || tr.Title != "Trip to Lisbon" {
		t.Fatalf("core fields wrong: %+v", tr)
	}
	if tr.SourceSessionID == nil || *tr.SourceSessionID != cc.SessionID.String() {
		t.Fatalf("source session not set: %+v", tr.SourceSessionID)
	}
	// 5 POIs, 4 per day -> 2 days (4 + 1).
	if len(tr.Days) != 2 {
		t.Fatalf("expected 2 days, got %d", len(tr.Days))
	}
	if len(tr.Days[0].Stops) != 4 || len(tr.Days[1].Stops) != 1 {
		t.Fatalf("bad segmentation: %d + %d", len(tr.Days[0].Stops), len(tr.Days[1].Stops))
	}
	if tr.Days[0].DayNumber != 1 || tr.Days[1].DayNumber != 2 {
		t.Fatalf("day numbers wrong")
	}
	// Order indices reset per day.
	if tr.Days[0].Stops[3].OrderIndex != 3 || tr.Days[1].Stops[0].OrderIndex != 0 {
		t.Fatalf("order indices wrong")
	}
	if tr.Days[0].Stops[0].Notes == "" {
		t.Fatal("expected TrustSignals rationale in stop notes")
	}
}

func TestBuildTripFromCityResponse_Empty(t *testing.T) {
	cc := &common.ChatContext{UserID: uuid.New(), SessionID: uuid.New()}
	if tr := buildTripFromCityResponse(cc, &locitypes.AiCityResponse{}); tr != nil {
		t.Fatal("expected nil for no POIs")
	}
	if tr := buildTripFromCityResponse(cc, nil); tr != nil {
		t.Fatal("expected nil for nil data")
	}
}
