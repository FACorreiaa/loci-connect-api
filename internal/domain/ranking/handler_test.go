package ranking

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	rankingv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/ranking"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func TestListUserRankings_StrangerIsPermissionDenied(t *testing.T) {
	h := NewHandler(NewService(&fakeRepo{}, &fakeFriends{}, nil, nil, nil), nil)
	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, uuid.NewString())
	_, err := h.ListUserRankings(ctx, connect.NewRequest(&rankingv1.ListUserRankingsRequest{UserId: uuid.NewString()}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

func TestRatePlace_RequiresSignIn(t *testing.T) {
	h := NewHandler(NewService(&fakeRepo{}, nil, nil, nil, nil), nil)
	_, err := h.RatePlace(context.Background(), connect.NewRequest(&rankingv1.RatePlaceRequest{PoiId: uuid.NewString()}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", connect.CodeOf(err))
	}
}

func TestComparisonToProto(t *testing.T) {
	if comparisonToProto(nil) != nil {
		t.Fatal("nil comparison must stay unset so clients know ranking is done")
	}
	got := comparisonToProto(&Comparison{POIID: "a", Name: "Sé"})
	if got.GetPoiId() != "a" || got.GetName() != "Sé" {
		t.Fatalf("comparison = %+v", got)
	}
}
