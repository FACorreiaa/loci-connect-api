package placeintel

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/linkimport"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type stubImporter struct {
	res      *linkimport.Result
	err      error
	url      string
	cityHint string
}

func (s *stubImporter) Import(_ context.Context, rawURL, cityHint string) (*linkimport.Result, error) {
	s.url, s.cityHint = rawURL, cityHint
	return s.res, s.err
}

func signedIn() context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, uuid.NewString())
}

func TestImportPlaces_MapsTheResult(t *testing.T) {
	imp := &stubImporter{res: &linkimport.Result{
		Source: linkimport.SourceTikTok, Title: "3 bakeries", City: "Lisbon",
		Places: []linkimport.Imported{
			{Name: "Manteigaria", City: "Lisbon", Note: "best nata", Place: &linkimport.Place{
				ID: "11111111-1111-1111-1111-111111111111", Name: "Manteigaria Chiado", Address: "R. do Loreto 2",
				Category: "bakery", Latitude: 38.71, Longitude: -9.14,
			}},
			{Name: "Ghost Café", City: "Lisbon"},
		},
	}}
	h := NewHandler(nil, nil).WithLinkImport(imp)
	hint := "Lisbon"
	resp, err := h.ImportPlaces(signedIn(), connect.NewRequest(&placev1.ImportPlacesRequest{Url: "https://vm.tiktok.com/x", CityHint: &hint}))
	require.NoError(t, err)
	require.Equal(t, "https://vm.tiktok.com/x", imp.url)
	require.Equal(t, "Lisbon", imp.cityHint)

	m := resp.Msg
	require.Equal(t, placev1.ImportSource_IMPORT_SOURCE_TIKTOK, m.GetSource())
	require.Equal(t, "3 bakeries", m.GetSourceTitle())
	require.Equal(t, "Lisbon", m.GetCityName())
	require.Len(t, m.GetPlaces(), 2)
	first := m.GetPlaces()[0]
	require.Equal(t, "Manteigaria", first.GetName())
	require.Equal(t, "best nata", first.GetNote())
	require.Equal(t, "11111111-1111-1111-1111-111111111111", first.GetPoiId())
	require.Equal(t, "Manteigaria Chiado", first.GetPoiName(), "the place that will be saved, shown when it differs")
	require.Equal(t, "R. do Loreto 2", first.GetAddress())
	require.InDelta(t, 38.71, first.GetLatitude(), 1e-9)
	second := m.GetPlaces()[1]
	require.Nil(t, second.PoiId)
	require.Nil(t, second.PoiName)
	require.Nil(t, second.Latitude)
}

func TestImportPlaces_ErrorCodes(t *testing.T) {
	req := connect.NewRequest(&placev1.ImportPlacesRequest{Url: "https://example.com/x"})

	_, err := NewHandler(nil, nil).WithLinkImport(&stubImporter{}).ImportPlaces(context.Background(), req)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = NewHandler(nil, nil).ImportPlaces(signedIn(), req)
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))

	for in, want := range map[error]connect.Code{
		linkimport.ErrBadURL:              connect.CodeInvalidArgument,
		linkimport.ErrUnreadable:          connect.CodeFailedPrecondition,
		linkimport.ErrNoPlaces:            connect.CodeFailedPrecondition,
		linkimport.ErrModel:               connect.CodeUnavailable,
		errors.New("database is on fire"): connect.CodeInternal,
	} {
		_, err := NewHandler(nil, nil).WithLinkImport(&stubImporter{err: in}).ImportPlaces(signedIn(), req)
		require.Equal(t, want, connect.CodeOf(err), in.Error())
	}
}
