package handler

import (
	"context"
	"errors"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// nearbyService records the distance search and fails loudly if a query-less
// request ever reaches a semantic or hybrid search (those embed the query, and
// an empty one is meaningless). Unused methods panic via the nil interface.
type nearbyService struct {
	poi.Service
	gotFilter *locitypes.POIFilter
	result    []locitypes.POIDetailedInfo
}

func (s *nearbyService) SearchPOIs(_ context.Context, f locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error) {
	s.gotFilter = &f
	return s.result, nil
}

func (s *nearbyService) SearchPOIsHybrid(context.Context, locitypes.POIFilter, string, float64) ([]locitypes.POIDetailedInfo, error) {
	return nil, errors.New("hybrid search must not run without a query")
}

func (s *nearbyService) SearchPOIsSemantic(context.Context, string, int) ([]locitypes.POIDetailedInfo, error) {
	return nil, errors.New("semantic search must not run without a query")
}

// Web's useNearbyPOIs sends a location and no query. query used to be
// min_len 1, so every call failed validation; now it is a nearby listing.
func TestSearchPOIRequest_QueryIsOptional(t *testing.T) {
	if err := protovalidate.Validate(&poiv1.SearchPOIRequest{Latitude: 38.7, Longitude: -9.1}); err != nil {
		t.Fatalf("query-less nearby request rejected: %v", err)
	}
	long := make([]byte, 501)
	for i := range long {
		long[i] = 'x'
	}
	if err := protovalidate.Validate(&poiv1.SearchPOIRequest{Query: string(long)}); err == nil {
		t.Fatal("an over-long query should still be rejected")
	}
}

func TestSearchPOI_EmptyQueryListsNearby(t *testing.T) {
	pois := make([]locitypes.POIDetailedInfo, 70)
	for i := range pois {
		pois[i] = locitypes.POIDetailedInfo{ID: uuid.New(), Name: "p", Latitude: 38.7, Longitude: -9.1}
	}
	svc := &nearbyService{result: pois}
	h := NewPOIHandler(svc)
	radius := 10.0
	hybrid := "hybrid"

	resp, err := h.SearchPOI(context.Background(), connect.NewRequest(&poiv1.SearchPOIRequest{
		Query: "  ", Latitude: 38.7, Longitude: -9.1, RadiusKm: &radius, SearchType: &hybrid,
		CityName: "Lisbon", SearchTags: []string{"restaurant"},
	}))
	if err != nil {
		t.Fatalf("SearchPOI: %v", err)
	}
	if svc.gotFilter == nil {
		t.Fatal("distance search was not called")
	}
	f := svc.gotFilter
	if f.Location.Latitude != 38.7 || f.Location.Longitude != -9.1 || f.Radius != 10 || f.Category != "restaurant" {
		t.Fatalf("filter = %+v", *f)
	}
	if got := len(resp.Msg.GetPois()); got != nearbyListLimit {
		t.Fatalf("got %d POIs, want the %d cap", got, nearbyListLimit)
	}
}

func TestSearchPOI_EmptyQueryDefaultsRadiusAndCategory(t *testing.T) {
	svc := &nearbyService{}
	if _, err := NewPOIHandler(svc).SearchPOI(context.Background(), connect.NewRequest(&poiv1.SearchPOIRequest{
		Latitude: 41.15, Longitude: -8.61,
	})); err != nil {
		t.Fatalf("SearchPOI: %v", err)
	}
	if svc.gotFilter.Radius != defaultHybridRadiusKm || svc.gotFilter.Category != "" {
		t.Fatalf("filter = %+v", *svc.gotFilter)
	}
}

func TestSearchPOI_EmptyQueryWithoutLocationIsInvalid(t *testing.T) {
	svc := &nearbyService{}
	_, err := NewPOIHandler(svc).SearchPOI(context.Background(), connect.NewRequest(&poiv1.SearchPOIRequest{CityName: "Lisbon"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
	if svc.gotFilter != nil {
		t.Fatal("no search should run")
	}
}
