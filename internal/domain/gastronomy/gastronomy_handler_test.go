package gastronomy

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	chatservice "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"

	gastronomyv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gastronomy"
)

type fakeGenerator struct {
	g       *locitypes.CityGastronomy
	cached  bool
	err     error
	gotID   uuid.UUID
	gotName string
}

func (f *fakeGenerator) GetCityGastronomy(_ context.Context, cityID uuid.UUID, cityName string) (*locitypes.CityGastronomy, bool, error) {
	f.gotID, f.gotName = cityID, cityName
	return f.g, f.cached, f.err
}

func TestGetCityGastronomy(t *testing.T) {
	gen := &fakeGenerator{
		g:      &locitypes.CityGastronomy{CityName: "Porto", Dishes: []locitypes.Dish{{Name: "Francesinha"}}},
		cached: true,
	}
	h := NewHandler(gen, slog.Default())

	resp, err := h.GetCityGastronomy(context.Background(), connect.NewRequest(&gastronomyv1.GetCityGastronomyRequest{
		City: &gastronomyv1.GetCityGastronomyRequest_CityName{CityName: "Porto"},
	}))
	if err != nil {
		t.Fatalf("GetCityGastronomy: %v", err)
	}
	if gen.gotName != "Porto" || gen.gotID != uuid.Nil {
		t.Errorf("generator got (%s, %q)", gen.gotID, gen.gotName)
	}
	if !resp.Msg.GetCached() || resp.Msg.GetGastronomy().GetDishes()[0].GetName() != "Francesinha" {
		t.Errorf("response = %+v", resp.Msg)
	}

	id := uuid.New()
	if _, err := h.GetCityGastronomy(context.Background(), connect.NewRequest(&gastronomyv1.GetCityGastronomyRequest{
		City: &gastronomyv1.GetCityGastronomyRequest_CityId{CityId: id.String()},
	})); err != nil || gen.gotID != id {
		t.Fatalf("by id: err=%v gotID=%s", err, gen.gotID)
	}
}

func TestGetCityGastronomyErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code connect.Code
	}{
		{chatservice.ErrGastronomyCityNotFound, connect.CodeNotFound},
		{chatservice.ErrGastronomyUnavailable, connect.CodeNotFound},
		{errors.New("provider down"), connect.CodeUnavailable},
	} {
		h := NewHandler(&fakeGenerator{err: tc.err}, slog.Default())
		_, err := h.GetCityGastronomy(context.Background(), connect.NewRequest(&gastronomyv1.GetCityGastronomyRequest{
			City: &gastronomyv1.GetCityGastronomyRequest_CityName{CityName: "Porto"},
		}))
		if got := connect.CodeOf(err); got != tc.code {
			t.Errorf("%v: code = %v, want %v", tc.err, got, tc.code)
		}
	}

	h := NewHandler(&fakeGenerator{}, slog.Default())
	_, err := h.GetCityGastronomy(context.Background(), connect.NewRequest(&gastronomyv1.GetCityGastronomyRequest{
		City: &gastronomyv1.GetCityGastronomyRequest_CityId{CityId: "not-a-uuid"},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("bad id: code = %v", connect.CodeOf(err))
	}
}
