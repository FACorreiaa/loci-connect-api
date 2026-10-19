package handler

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type onePOIService struct {
	poi.Service
	poi locitypes.POIDetailedInfo
}

func (s *onePOIService) GetPOI(context.Context, uuid.UUID) (*locitypes.POIDetailedInfo, error) {
	return &s.poi, nil
}

func (s *onePOIService) SearchPOIs(context.Context, locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error) {
	return []locitypes.POIDetailedInfo{s.poi}, nil
}

type tasteScorer struct{ match preference.Match }

func (s tasteScorer) ScorePOIs(_ context.Context, _ uuid.UUID, ids []string) (map[string]preference.Match, error) {
	out := map[string]preference.Match{}
	for _, id := range ids {
		out[id] = s.match
	}
	return out, nil
}

func tasteHandler() (*POIHandler, uuid.UUID) {
	id := uuid.New()
	svc := &onePOIService{poi: locitypes.POIDetailedInfo{ID: id, Name: "Café"}}
	h := NewPOIHandler(svc).WithTasteScorer(tasteScorer{match: preference.Match{Score: 0.8, Reasons: []string{"You tend to like cafe"}}})
	return h, id
}

func signedIn() context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, uuid.NewString())
}

func TestGetPOI_AttachesTasteMatch(t *testing.T) {
	h, id := tasteHandler()
	resp, err := h.GetPOI(signedIn(), connect.NewRequest(&poiv1.GetPOIRequest{PoiId: id.String()}))
	if err != nil {
		t.Fatalf("GetPOI: %v", err)
	}
	if got := resp.Msg.GetPoi().GetTasteMatch(); got.GetScore() != 0.8 || len(got.GetReasons()) != 1 {
		t.Fatalf("taste match = %+v", got)
	}

	anon, err := h.GetPOI(context.Background(), connect.NewRequest(&poiv1.GetPOIRequest{PoiId: id.String()}))
	if err != nil {
		t.Fatalf("GetPOI anonymous: %v", err)
	}
	if anon.Msg.GetPoi().TasteMatch != nil {
		t.Fatal("an anonymous caller must not get a match")
	}
}

func TestListNearby_AttachesTasteMatch(t *testing.T) {
	h, _ := tasteHandler()
	resp, err := h.SearchPOI(signedIn(), connect.NewRequest(&poiv1.SearchPOIRequest{Latitude: 38.7, Longitude: -9.1}))
	if err != nil {
		t.Fatalf("SearchPOI: %v", err)
	}
	if len(resp.Msg.GetPois()) != 1 || resp.Msg.GetPois()[0].GetTasteMatch().GetScore() != 0.8 {
		t.Fatalf("pois = %+v", resp.Msg.GetPois())
	}
}
