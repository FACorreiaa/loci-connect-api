package statistics

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect"
	statisticsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/statistics"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/statistics/statisticsv1connect"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/types/known/timestamppb"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

var _ statisticsv1connect.StatisticsServiceHandler = (*Handler)(nil)

// VisitedCitiesCounter reports how many cities a user has actually been to.
//
// Narrow on purpose: statistics needs one number from travel history, and an
// interface keeps that a one-way dependency rather than coupling the two
// domains.
type VisitedCitiesCounter interface {
	CountVisitedCities(ctx context.Context, userID uuid.UUID) (int32, error)
}

// Handler implements the StatisticsService Connect handlers.
type Handler struct {
	statisticsv1connect.UnimplementedStatisticsServiceHandler
	service Service
	logger  *slog.Logger

	// visits backs VisitedCitiesCount. Nil until wired; the field reports 0
	// rather than the hotels+restaurants sum it used to invent.
	visits VisitedCitiesCounter

	// mainStats is what StreamMainPageStatistics serves: the aggregate is heavy
	// and the stream needs no sign-in, so its cost must not grow with viewers.
	mainStats         mainStatsCache
	streamMaxLifetime time.Duration
}

const (
	// streamMinInterval is the fastest a stream ticks, whatever the client asks
	// for. Equal to mainStatsTTL: ticking faster would resend the same numbers.
	streamMinInterval = 30 * time.Second
	// streamMaxLifetime ends a stream so that an idle one cannot hold its
	// connection and goroutine forever. Clients reconnect.
	streamMaxLifetime = 10 * time.Minute

	mainStatsTTL          = 30 * time.Second
	mainStatsQueryTimeout = 10 * time.Second
)

// NewHandler constructs a new statistics handler.
func NewHandler(svc Service, logger *slog.Logger) *Handler {
	return &Handler{
		service:           svc,
		logger:            logger,
		streamMaxLifetime: streamMaxLifetime,
	}
}

// WithVisitedCities attaches the real source for visited-city counts.
func (h *Handler) WithVisitedCities(v VisitedCitiesCounter) *Handler {
	h.visits = v
	return h
}

// visitedCitiesCount returns the real count, or 0 when travel history is
// unavailable.
//
// Zero is deliberate. This field previously returned `hotels + restaurants`
// with a "// Placeholder" comment — a number that looked plausible, moved when
// the user searched, and was never once correct. Reporting 0 when we do not
// know is the honest failure mode.
func (h *Handler) visitedCitiesCount(ctx context.Context, userID uuid.UUID) int32 {
	if h.visits == nil {
		return 0
	}
	n, err := h.visits.CountVisitedCities(ctx, userID)
	if err != nil {
		h.logger.WarnContext(ctx, "failed to count visited cities", "error", err)
		return 0
	}
	return n
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
	_ *connect.Request[statisticsv1.GetMainPageStatisticsRequest],
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

	return h.streamMainStats(ctx, req.Msg.UpdateIntervalSeconds, func(snap mainStatsSnapshot) error {
		event := &statisticsv1.StatisticsEvent{
			EventType: "update",
			Timestamp: timestamppb.Now(),
			Payload: &statisticsv1.StatisticsEvent_MainStats{
				MainStats: &statisticsv1.MainPageStatistics{
					TotalUsers:       snap.stats.TotalUsersCount,
					TotalItineraries: snap.stats.TotalItinerariesSaved,
					TotalPois:        snap.stats.TotalUniquePOIs,
					LastUpdated:      timestamppb.New(snap.fetchedAt),
				},
			},
		}
		if err := stream.Send(event); err != nil {
			l.ErrorContext(ctx, "failed to send stream event", "error", err)
			return err
		}
		return nil
	})
}

// mainStatsSnapshot is the main page numbers and when they were read.
type mainStatsSnapshot struct {
	stats     *locitypes.MainPageStatistics
	fetchedAt time.Time
}

// mainStatsCache holds the last read of the main page numbers.
type mainStatsCache struct {
	group singleflight.Group

	mu   sync.Mutex
	last mainStatsSnapshot
}

func (c *mainStatsCache) fresh() (mainStatsSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last.stats == nil || time.Since(c.last.fetchedAt) >= mainStatsTTL {
		return mainStatsSnapshot{}, false
	}
	return c.last, true
}

func (c *mainStatsCache) store(snap mainStatsSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = snap
}

// sharedMainStats returns the cached numbers, or reads them once on behalf of
// everyone asking. Failures are not cached.
//
// Do rather than DoChan: DoChan re-panics on a goroutine of its own, where no
// recover can reach it, and a panic here must stay inside this request. The cost
// is that a stream leaving mid-query waits for it, at most mainStatsQueryTimeout.
func (h *Handler) sharedMainStats(ctx context.Context) (mainStatsSnapshot, error) {
	if snap, ok := h.mainStats.fresh(); ok {
		return snap, nil
	}

	out, err, _ := h.mainStats.group.Do("main", func() (any, error) {
		// A caller that lost the race to one that has just finished.
		if snap, ok := h.mainStats.fresh(); ok {
			return snap, nil
		}

		// Detached: the others waiting on this result must not fail because the
		// stream that happened to start it has gone.
		queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mainStatsQueryTimeout)
		defer cancel()

		// Timed from the start of the read, which is the age of the numbers.
		fetchedAt := time.Now()
		stats, err := h.service.GetMainPageStatistics(queryCtx, uuid.Nil)
		if err != nil {
			return mainStatsSnapshot{}, err
		}
		snap := mainStatsSnapshot{stats: stats, fetchedAt: fetchedAt}
		h.mainStats.store(snap)
		return snap, nil
	})
	if err != nil {
		return mainStatsSnapshot{}, err
	}
	return out.(mainStatsSnapshot), nil
}

// streamMainStats sends the numbers on open and on every tick until the client
// leaves or the stream reaches its lifetime, which is a clean end.
func (h *Handler) streamMainStats(
	ctx context.Context,
	intervalSeconds int32,
	send func(mainStatsSnapshot) error,
) error {
	l := h.logger.With(slog.String("method", "StreamMainPageStatistics"))

	ctx, cancel := context.WithTimeout(ctx, h.streamMaxLifetime)
	defer cancel()

	ticker := time.NewTicker(max(time.Duration(intervalSeconds)*time.Second, streamMinInterval))
	defer ticker.Stop()

	for {
		snap, err := h.sharedMainStats(ctx)
		if err != nil {
			l.ErrorContext(ctx, "failed to get statistics for stream", "error", err)
		} else if err := send(snap); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			l.InfoContext(ctx, "stream closed")
			return nil
		case <-ticker.C:
		}
	}
}

// GetDetailedPOIStatistics returns detailed POI statistics for a user.
// This endpoint requires AUTHENTICATION.
func (h *Handler) GetDetailedPOIStatistics(
	ctx context.Context,
	_ *connect.Request[statisticsv1.GetDetailedPOIStatisticsRequest],
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
			VisitedCitiesCount: h.visitedCitiesCount(ctx, userID),
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
	_ *connect.Request[statisticsv1.GetLandingPageStatisticsRequest],
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
	_ *connect.Request[statisticsv1.GetUserActivityAnalyticsRequest],
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
	_ *connect.Request[statisticsv1.GetSystemAnalyticsRequest],
) (*connect.Response[statisticsv1.GetSystemAnalyticsResponse], error) {
	l := h.logger.With(slog.String("method", "GetSystemAnalytics"))

	// Require an authenticated admin caller.
	claims, err := interceptors.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if claims.Role != "admin" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin role required"))
	}

	// Get user ID from context - should verify admin role
	userID, ok := getUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

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
