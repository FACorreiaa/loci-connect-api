package statistics

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	statisticsv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/statistics"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/statistics/statisticsv1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

var _ statisticsv1connect.StatisticsServiceHandler = (*Handler)(nil)

// Handler implements the StatisticsService Connect handlers.
type Handler struct {
	statisticsv1connect.UnimplementedStatisticsServiceHandler
	service Service
	logger  *slog.Logger
}

// NewHandler constructs a new statistics handler.
func NewHandler(svc Service, logger *slog.Logger) *Handler {
	return &Handler{
		service: svc,
		logger:  logger,
	}
}

// getUserIDFromContext extracts user ID as UUID from context
func getUserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return uuid.Nil, false
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, false
	}
	return userID, true
}

// GetMainPageStatistics returns public statistics for the main page.
// This endpoint is PUBLIC - all users can access it.
func (h *Handler) GetMainPageStatistics(
	ctx context.Context,
	req *connect.Request[statisticsv1.GetMainPageStatisticsRequest],
) (*connect.Response[statisticsv1.GetMainPageStatisticsResponse], error) {
	l := h.logger.With(slog.String("method", "GetMainPageStatistics"))

	// For public stats, use a system/nil user ID to get aggregate stats
	systemUserID := uuid.MustParse("00000000-0000-0000-0000-000000000000")

	stats, err := h.service.GetMainPageStatistics(ctx, systemUserID)
	if err != nil {
		l.ErrorContext(ctx, "failed to get main page statistics", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response := &statisticsv1.GetMainPageStatisticsResponse{
		Statistics: &statisticsv1.MainPageStatistics{
			TotalUsers:       stats.TotalUsersCount,
			TotalItineraries: stats.TotalItinerariesSaved,
			TotalPois:        stats.TotalUniquePOIs,
			LastUpdated:      timestamppb.Now(),
		},
	}

	l.InfoContext(ctx, "successfully retrieved main page statistics",
		slog.Int64("users", stats.TotalUsersCount),
		slog.Int64("itineraries", stats.TotalItinerariesSaved),
		slog.Int64("pois", stats.TotalUniquePOIs))

	return connect.NewResponse(response), nil
}

// StreamMainPageStatistics streams real-time statistics updates.
func (h *Handler) StreamMainPageStatistics(
	ctx context.Context,
	req *connect.Request[statisticsv1.StreamMainPageStatisticsRequest],
	stream *connect.ServerStream[statisticsv1.StatisticsEvent],
) error {
	l := h.logger.With(slog.String("method", "StreamMainPageStatistics"))

	interval := req.Msg.UpdateIntervalSeconds
	if interval == 0 {
		interval = 30 // Default to 30 seconds
	}

	systemUserID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.InfoContext(ctx, "stream closed by client")
			return nil
		case <-ticker.C:
			stats, err := h.service.GetMainPageStatistics(ctx, systemUserID)
			if err != nil {
				l.ErrorContext(ctx, "failed to get statistics for stream", "error", err)
				continue
			}

			event := &statisticsv1.StatisticsEvent{
				EventType: "update",
				Timestamp: timestamppb.Now(),
				Payload: &statisticsv1.StatisticsEvent_MainStats{
					MainStats: &statisticsv1.MainPageStatistics{
						TotalUsers:       stats.TotalUsersCount,
						TotalItineraries: stats.TotalItinerariesSaved,
						TotalPois:        stats.TotalUniquePOIs,
						LastUpdated:      timestamppb.Now(),
					},
				},
			}

			if err := stream.Send(event); err != nil {
				l.ErrorContext(ctx, "failed to send stream event", "error", err)
				return err
			}
		}
	}
}

// GetDetailedPOIStatistics returns detailed POI statistics for a user.
// This endpoint requires AUTHENTICATION.
func (h *Handler) GetDetailedPOIStatistics(
	ctx context.Context,
	req *connect.Request[statisticsv1.GetDetailedPOIStatisticsRequest],
) (*connect.Response[statisticsv1.GetDetailedPOIStatisticsResponse], error) {
	l := h.logger.With(slog.String("method", "GetDetailedPOIStatistics"))

	// Get user ID from context (set by auth interceptor)
	userID, ok := getUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	stats, err := h.service.GetDetailedPOIStatistics(ctx, userID)
	if err != nil {
		l.ErrorContext(ctx, "failed to get detailed POI statistics", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response := &statisticsv1.GetDetailedPOIStatisticsResponse{
		Statistics: &statisticsv1.DetailedPOIStatistics{
			UserId:             userID.String(),
			TotalPoiSearches:   int32(stats.TotalPOIs),
			FavoritePoisCount:  int32(stats.GeneralPOIs + stats.SuggestedPOIs),
			VisitedCitiesCount: int32(stats.Hotels + stats.Restaurants), // Placeholder
			GeneratedAt:        timestamppb.Now(),
		},
	}

	l.InfoContext(ctx, "successfully retrieved detailed POI statistics", "user_id", userID)
	return connect.NewResponse(response), nil
}

// GetLandingPageStatistics returns user-specific dashboard statistics.
// This endpoint requires AUTHENTICATION.
func (h *Handler) GetLandingPageStatistics(
	ctx context.Context,
	req *connect.Request[statisticsv1.GetLandingPageStatisticsRequest],
) (*connect.Response[statisticsv1.GetLandingPageStatisticsResponse], error) {
	l := h.logger.With(slog.String("method", "GetLandingPageStatistics"))

	// Get user ID from context (set by auth interceptor)
	userID, ok := getUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	stats, err := h.service.GetLandingPageStatistics(ctx, userID)
	if err != nil {
		l.ErrorContext(ctx, "failed to get landing page statistics", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response := &statisticsv1.GetLandingPageStatisticsResponse{
		Statistics: &statisticsv1.LandingPageUserStats{
			UserId:                      userID.String(),
			SearchesThisWeek:            int32(stats.Discoveries), // Using discoveries as a proxy
			NewFavoritesThisWeek:        int32(stats.SavedPlaces),
			ItinerariesCreatedThisMonth: int32(stats.Itineraries),
			RecentlySearchedCities:      []string{}, // Not implemented yet
			CitiesExplored:              int32(stats.CitiesExplored),
		},
	}

	l.InfoContext(ctx, "successfully retrieved landing page statistics",
		slog.String("user_id", userID.String()),
		slog.Int("saved_places", stats.SavedPlaces),
		slog.Int("itineraries", stats.Itineraries),
		slog.Int("cities_explored", stats.CitiesExplored),
		slog.Int("discoveries", stats.Discoveries))

	return connect.NewResponse(response), nil
}

// GetUserActivityAnalytics returns user activity analytics over a time range.
// This endpoint requires AUTHENTICATION.
func (h *Handler) GetUserActivityAnalytics(
	ctx context.Context,
	req *connect.Request[statisticsv1.GetUserActivityAnalyticsRequest],
) (*connect.Response[statisticsv1.GetUserActivityAnalyticsResponse], error) {
	l := h.logger.With(slog.String("method", "GetUserActivityAnalytics"))

	// Get user ID from context
	userID, ok := getUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	// For now, return empty activity data - this can be expanded later
	response := &statisticsv1.GetUserActivityAnalyticsResponse{
		ActivityData: []*statisticsv1.ActivityDataPoint{},
		Summary: &statisticsv1.ActivitySummary{
			TotalSearches:         0,
			TotalFavorites:        0,
			TotalItineraryActions: 0,
			MostActiveHour:        12,
			MostActiveDay:         "Monday",
		},
	}

	l.InfoContext(ctx, "successfully retrieved user activity analytics", "user_id", userID)
	return connect.NewResponse(response), nil
}

// GetSystemAnalytics returns system-wide analytics (admin only).
// This endpoint requires ADMIN AUTHENTICATION.
func (h *Handler) GetSystemAnalytics(
	ctx context.Context,
	req *connect.Request[statisticsv1.GetSystemAnalyticsRequest],
) (*connect.Response[statisticsv1.GetSystemAnalyticsResponse], error) {
	l := h.logger.With(slog.String("method", "GetSystemAnalytics"))

	// Get user ID from context - should verify admin role
	userID, ok := getUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	// TODO: Add admin role check here

	// Get aggregate statistics
	systemUserID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	stats, err := h.service.GetMainPageStatistics(ctx, systemUserID)
	if err != nil {
		l.ErrorContext(ctx, "failed to get system analytics", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response := &statisticsv1.GetSystemAnalyticsResponse{
		Analytics: &statisticsv1.SystemAnalytics{
			UserGrowth: &statisticsv1.UserGrowthMetrics{
				TotalUsers:          stats.TotalUsersCount,
				NewUsersToday:       0,
				NewUsersThisWeek:    0,
				NewUsersThisMonth:   0,
				ActiveUsersToday:    0,
				ActiveUsersThisWeek: 0,
			},
			UsageMetrics: &statisticsv1.UsageMetrics{
				TotalSearches:    0,
				SearchesToday:    0,
				SearchesThisWeek: 0,
			},
			GeneratedAt: timestamppb.Now(),
		},
	}

	l.InfoContext(ctx, "successfully retrieved system analytics", "user_id", userID)
	return connect.NewResponse(response), nil
}
