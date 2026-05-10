package statistics

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	statisticsv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/statistics"
)

// stubStatsService is a no-op Service implementation used by handler tests.
type stubStatsService struct{}

func (s *stubStatsService) GetMainPageStatistics(ctx context.Context, userID uuid.UUID) (*locitypes.MainPageStatistics, error) {
	return &locitypes.MainPageStatistics{}, nil
}

func (s *stubStatsService) GetDetailedPOIStatistics(ctx context.Context, userID uuid.UUID) (*locitypes.DetailedPOIStatistics, error) {
	return &locitypes.DetailedPOIStatistics{}, nil
}

func (s *stubStatsService) GetLandingPageStatistics(ctx context.Context, userID uuid.UUID) (*locitypes.LandingPageUserStats, error) {
	return &locitypes.LandingPageUserStats{}, nil
}

func newStatsTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
}

func TestGetSystemAnalytics_NonAdminDenied(t *testing.T) {
	h := NewHandler(&stubStatsService{}, newStatsTestLogger())

	ctx := interceptors.ContextWithClaims(context.Background(), &interceptors.Claims{
		UserID: uuid.New().String(),
		Role:   "user",
	})

	_, err := h.GetSystemAnalytics(ctx, connect.NewRequest(&statisticsv1.GetSystemAnalyticsRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestGetSystemAnalytics_MissingClaimsUnauthenticated(t *testing.T) {
	h := NewHandler(&stubStatsService{}, newStatsTestLogger())

	_, err := h.GetSystemAnalytics(context.Background(), connect.NewRequest(&statisticsv1.GetSystemAnalyticsRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetSystemAnalytics_AdminAllowed(t *testing.T) {
	h := NewHandler(&stubStatsService{}, newStatsTestLogger())

	ctx := interceptors.ContextWithClaims(context.Background(), &interceptors.Claims{
		UserID: uuid.New().String(),
		Role:   "admin",
	})

	resp, err := h.GetSystemAnalytics(ctx, connect.NewRequest(&statisticsv1.GetSystemAnalyticsRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.GetAnalytics())
}
