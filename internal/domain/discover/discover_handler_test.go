package discover

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/discover/presenter"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat"
	discoverv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/discover"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/discover/discoverconnect"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type stubService struct {
	pageData  *locitypes.DiscoverPageData
	trending  []locitypes.TrendingDiscovery
	featured  []locitypes.FeaturedCollection
	recent    []locitypes.ChatSession
	category  []locitypes.DiscoverResult
	err       error
	lastCalls []string
}

func (s *stubService) GetDiscoverPageData(ctx context.Context, userID uuid.UUID, limit int) (*locitypes.DiscoverPageData, error) {
	s.lastCalls = append(s.lastCalls, "GetDiscoverPageData")
	return s.pageData, s.err
}
func (s *stubService) GetTrendingDiscoveries(ctx context.Context, limit int) ([]locitypes.TrendingDiscovery, error) {
	s.lastCalls = append(s.lastCalls, "GetTrendingDiscoveries")
	return s.trending, s.err
}
func (s *stubService) GetFeaturedCollections(ctx context.Context, limit int) ([]locitypes.FeaturedCollection, error) {
	s.lastCalls = append(s.lastCalls, "GetFeaturedCollections")
	return s.featured, s.err
}
func (s *stubService) GetRecentDiscoveries(ctx context.Context, userID uuid.UUID, limit int) ([]locitypes.ChatSession, error) {
	s.lastCalls = append(s.lastCalls, "GetRecentDiscoveries")
	return s.recent, s.err
}
func (s *stubService) GetCategoryResults(ctx context.Context, category, cityName string, page, limit int) ([]locitypes.DiscoverResult, error) {
	s.lastCalls = append(s.lastCalls, "GetCategoryResults")
	return s.category, s.err
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
}

func TestGetDiscoverPage_Succeeds(t *testing.T) {
	svc := &stubService{
		pageData: &locitypes.DiscoverPageData{
			Trending: []locitypes.TrendingDiscovery{{CityName: "Paris", SearchCount: 5}},
			Featured: []locitypes.FeaturedCollection{{Category: "food", Title: "Food", ItemCount: 3}},
		},
	}
	h := NewHandler(svc, newTestLogger())

	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, uuid.New().String())
	resp, err := h.GetDiscoverPage(ctx, connect.NewRequest(&discoverv1.GetDiscoverPageRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.GetData())
	require.Len(t, resp.Msg.GetData().GetTrending(), 1)
	require.Equal(t, "Paris", resp.Msg.GetData().GetTrending()[0].GetCityName())
	require.Equal(t, int32(5), resp.Msg.GetData().GetTrending()[0].GetSearchCount())
}

func TestGetRecentDiscoveries_RequiresAuth(t *testing.T) {
	svc := &stubService{}
	h := NewHandler(svc, newTestLogger())

	_, err := h.GetRecentDiscoveries(context.Background(), connect.NewRequest(&discoverv1.GetRecentDiscoveriesRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetCategoryResults_ValidatesCategory(t *testing.T) {
	svc := &stubService{}
	h := NewHandler(svc, newTestLogger())

	_, err := h.GetCategoryResults(context.Background(), connect.NewRequest(&discoverv1.GetCategoryResultsRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDiscoverE2E_GetTrending(t *testing.T) {
	svc := &stubService{
		trending: []locitypes.TrendingDiscovery{
			{CityName: "Tokyo", SearchCount: 7, Emoji: "🍜"},
		},
	}
	handler := NewHandler(svc, newTestLogger())

	mux := http.NewServeMux()
	path, h := discoverconnect.NewDiscoverServiceHandler(handler)
	mux.Handle(path, h)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := discoverconnect.NewDiscoverServiceClient(server.Client(), server.URL)
	resp, err := client.GetTrending(context.Background(), connect.NewRequest(&discoverv1.GetTrendingRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetTrending(), 1)
	require.Equal(t, "Tokyo", resp.Msg.GetTrending()[0].GetCityName())
	require.Equal(t, int32(7), resp.Msg.GetTrending()[0].GetSearchCount())
}

func TestPresenter_ToDiscoverResults(t *testing.T) {
	results := []locitypes.DiscoverResult{
		{
			ID:          "1",
			Name:        "Place",
			Latitude:    1.23,
			Longitude:   4.56,
			Category:    "restaurant",
			Description: "desc",
			Address:     "addr",
			Tags:        []string{"tag"},
			Images:      []string{"img"},
			CuisineType: ptr("asian"),
			StarRating:  ptr("5"),
		},
	}

	proto := presenter.ToDiscoverResults(results)
	require.Len(t, proto, 1)
	require.Equal(t, "Place", proto[0].GetName())
	require.Equal(t, "asian", proto[0].GetCuisineType())
	require.Equal(t, "5", proto[0].GetStarRating())
}

func ptr[T any](v T) *T { return &v }
