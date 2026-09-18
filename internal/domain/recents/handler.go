package recents

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	recentsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recents"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recents/recentsv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ recentsv1connect.RecentsServiceHandler = (*Handler)(nil)

// Handler implements the RecentsServiceHandler interface
type Handler struct {
	service Service
	logger  *slog.Logger
}

// NewHandler creates a new RecentsHandler
func NewHandler(service Service, logger *slog.Logger) *Handler {
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// getUserIDFromContext extracts the user ID from the request context
func (h *Handler) getUserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return uuid.UUID{}, false
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Error("failed to parse user ID", slog.Any("error", err))
		return uuid.UUID{}, false
	}
	return userID, true
}

// resolveUserID takes the caller's id from the auth interceptor's context, and
// falls back to the one in the request body. The fallback exists because the
// request messages carry user_id and older callers still set it; the context is
// authoritative when both are present.
func (h *Handler) resolveUserID(ctx context.Context, fromRequest string) (uuid.UUID, error) {
	if userID, ok := h.getUserIDFromContext(ctx); ok {
		return userID, nil
	}
	if fromRequest == "" {
		return uuid.UUID{}, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(fromRequest)
	if err != nil {
		h.logger.Error("invalid user ID format", slog.Any("error", err))
		return uuid.UUID{}, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID format"))
	}
	return userID, nil
}

// GetRecentInteractions returns the user's recent interactions
func (h *Handler) GetRecentInteractions(
	ctx context.Context,
	req *connect.Request[recentsv1.GetRecentInteractionsRequest],
) (*connect.Response[recentsv1.GetRecentInteractionsResponse], error) {
	l := h.logger.With(slog.String("method", "GetRecentInteractions"))

	// Get user ID from context or request
	var userID uuid.UUID
	var ok bool

	// First try from context (auth interceptor)
	userID, ok = h.getUserIDFromContext(ctx)
	if !ok {
		// Fall back to request body
		if req.Msg.UserId != "" {
			var err error
			userID, err = uuid.Parse(req.Msg.UserId)
			if err != nil {
				l.ErrorContext(ctx, "invalid user ID format", slog.Any("error", err))
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID format"))
			}
		} else {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
	}

	limit := int(req.Msg.Limit)
	if limit <= 0 {
		limit = 20
	}

	offset := int(req.Msg.Offset)
	page := (offset / limit) + 1

	// Create filter from request
	var filter *locitypes.RecentInteractionsFilter
	if req.Msg.GroupByCity {
		filter = &locitypes.RecentInteractionsFilter{
			SortBy:    "last_activity",
			SortOrder: "desc",
		}
	}

	response, err := h.service.GetUserRecentInteractions(ctx, userID, page, limit, filter)
	if err != nil {
		l.ErrorContext(ctx, "failed to get recent interactions", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	protoInteractions := make([]*recentsv1.RecentInteraction, 0)
	citySummaries := make([]*recentsv1.CityInteractionSummary, 0)

	for _, city := range response.Cities {
		// Create city summary
		citySummary := &recentsv1.CityInteractionSummary{
			CityId:           "", // CityInteractions doesn't have CityID
			CityName:         city.CityName,
			Country:          "",
			InteractionCount: int32(len(city.Interactions)),
		}

		if !city.LastActivity.IsZero() {
			citySummary.LatestInteraction = timestamppb.New(city.LastActivity)
		}

		// Convert interactions
		recentInteractions := make([]*recentsv1.RecentInteraction, 0)
		for _, interaction := range city.Interactions {
			protoInteraction := &recentsv1.RecentInteraction{
				Id:          interaction.ID.String(),
				UserId:      interaction.UserID.String(),
				CityName:    city.CityName,
				Description: interaction.Prompt,
				CreatedAt:   timestamppb.New(interaction.CreatedAt),
			}

			// Determine interaction type based on content
			if len(interaction.Hotels) > 0 {
				protoInteraction.InteractionType = recentsv1.InteractionType_INTERACTION_TYPE_SEARCH
				protoInteraction.EntityType = "hotel"
			} else if len(interaction.Restaurants) > 0 {
				protoInteraction.InteractionType = recentsv1.InteractionType_INTERACTION_TYPE_SEARCH
				protoInteraction.EntityType = "restaurant"
			} else if len(interaction.POIs) > 0 {
				protoInteraction.InteractionType = recentsv1.InteractionType_INTERACTION_TYPE_SEARCH
				protoInteraction.EntityType = "poi"
			} else {
				protoInteraction.InteractionType = recentsv1.InteractionType_INTERACTION_TYPE_CHAT
				protoInteraction.EntityType = "chat"
			}

			recentInteractions = append(recentInteractions, protoInteraction)
			protoInteractions = append(protoInteractions, protoInteraction)
		}

		citySummary.RecentInteractions = recentInteractions
		citySummaries = append(citySummaries, citySummary)
	}

	result := &recentsv1.GetRecentInteractionsResponse{
		Interactions:  protoInteractions,
		TotalCount:    int32(response.Total),
		CitySummaries: citySummaries,
	}

	l.InfoContext(ctx, "successfully retrieved recent interactions",
		slog.Int("interaction_count", len(protoInteractions)),
		slog.Int("city_count", len(citySummaries)))

	return connect.NewResponse(result), nil
}

// GetCityInteractions returns interactions for a specific city
func (h *Handler) GetCityInteractions(
	ctx context.Context,
	req *connect.Request[recentsv1.GetCityInteractionsRequest],
) (*connect.Response[recentsv1.GetCityInteractionsResponse], error) {
	l := h.logger.With(slog.String("method", "GetCityInteractions"))

	// Get user ID from context or request
	var userID uuid.UUID
	var ok bool

	userID, ok = h.getUserIDFromContext(ctx)
	if !ok {
		if req.Msg.UserId != "" {
			var err error
			userID, err = uuid.Parse(req.Msg.UserId)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID format"))
			}
		} else {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
	}

	cityName := req.Msg.CityName
	if cityName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("city name is required"))
	}

	cityDetails, err := h.service.GetCityDetailsForUser(ctx, userID, cityName)
	if err != nil {
		l.ErrorContext(ctx, "failed to get city interactions", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto
	protoCityInteractions := &recentsv1.CityInteractions{
		CityName:          cityDetails.CityName,
		TotalInteractions: int32(len(cityDetails.Interactions)),
		PoisViewed:        int32(cityDetails.POICount),
	}

	if !cityDetails.LastActivity.IsZero() {
		protoCityInteractions.LastInteraction = timestamppb.New(cityDetails.LastActivity)
		protoCityInteractions.FirstInteraction = timestamppb.New(cityDetails.LastActivity)
	}

	result := &recentsv1.GetCityInteractionsResponse{
		CityInteractions: protoCityInteractions,
	}

	l.InfoContext(ctx, "successfully retrieved city interactions", slog.String("city", cityName))

	return connect.NewResponse(result), nil
}

// RecordInteraction records a new user interaction
func (h *Handler) RecordInteraction(
	ctx context.Context,
	_ *connect.Request[recentsv1.RecordInteractionRequest],
) (*connect.Response[recentsv1.RecordInteractionResponse], error) {
	l := h.logger.With(slog.String("method", "RecordInteraction"))

	// This is an internal API, for now return success
	// TODO: Implement actual recording
	l.InfoContext(ctx, "record interaction called (stub)")

	return connect.NewResponse(&recentsv1.RecordInteractionResponse{
		Success:       true,
		InteractionId: uuid.New().String(),
		Message:       "Interaction recorded",
	}), nil
}

// GetInteractionHistory returns the recents activity feed: one flat,
// reverse-chronological page of everything the user did — every prompt they
// sent, every itinerary they saved, every place they favourited.
//
// This used to delegate to GetUserRecentInteractions, which groups by city and
// keeps five interactions per city, and then threw the request's filters away.
// Grouping by city can only answer "where have I been"; the feed has to answer
// "what did I do", so it reads the three sources directly.
//
// TotalCount is a floor, not a total. Counting a three-way union means running
// every branch with no limit on every page request, so the service fetches one
// row past the page instead and reports its existence here: when another page
// exists TotalCount is one more than what has been served, and a client's test
// for "is there more" is offset+len(interactions) < total_count.
func (h *Handler) GetInteractionHistory(
	ctx context.Context,
	req *connect.Request[recentsv1.GetInteractionHistoryRequest],
) (*connect.Response[recentsv1.GetInteractionHistoryResponse], error) {
	l := h.logger.With(slog.String("method", "GetInteractionHistory"))

	userID, err := h.resolveUserID(ctx, req.Msg.GetUserId())
	if err != nil {
		return nil, err
	}

	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	offset := int(req.Msg.GetOffset())

	filter := activityFilterFromProto(req.Msg.GetFilter(), req.Msg.GetSortOrder())

	entries, hasMore, err := h.service.GetUserActivityFeed(ctx, userID, limit, offset, filter)
	if err != nil {
		l.ErrorContext(ctx, "failed to get interaction history", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	interactions := make([]*recentsv1.RecentInteraction, 0, len(entries))
	for _, e := range entries {
		interactions = append(interactions, activityEntryToProto(userID.String(), e))
	}

	total := offset + len(interactions)
	if hasMore {
		total++
	}

	l.InfoContext(ctx, "successfully retrieved interaction history",
		slog.Int("count", len(interactions)),
		slog.Bool("has_more", hasMore))

	return connect.NewResponse(&recentsv1.GetInteractionHistoryResponse{
		Interactions: interactions,
		TotalCount:   int32(total),
	}), nil
}

// GetFrequentPlaces returns frequently visited places
func (h *Handler) GetFrequentPlaces(
	ctx context.Context,
	req *connect.Request[recentsv1.GetFrequentPlacesRequest],
) (*connect.Response[recentsv1.GetFrequentPlacesResponse], error) {
	l := h.logger.With(slog.String("method", "GetFrequentPlaces"))

	// Get user ID
	var userID uuid.UUID
	var ok bool

	userID, ok = h.getUserIDFromContext(ctx)
	if !ok {
		if req.Msg.UserId != "" {
			var err error
			userID, err = uuid.Parse(req.Msg.UserId)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID format"))
			}
		} else {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
	}

	// Get recent interactions to derive frequent places
	response, err := h.service.GetUserRecentInteractions(ctx, userID, 1, 100, nil)
	if err != nil {
		l.ErrorContext(ctx, "failed to get interactions for frequent places", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert cities to frequent places
	places := make([]*recentsv1.FrequentPlace, 0)
	for _, city := range response.Cities {
		place := &recentsv1.FrequentPlace{
			PlaceId:          "", // No CityID available
			PlaceName:        city.CityName,
			PlaceType:        "city",
			Category:         "destination",
			CityName:         city.CityName,
			InteractionCount: int32(len(city.Interactions)),
			VisitCount:       int32(len(city.Interactions)),
		}

		if !city.LastActivity.IsZero() {
			place.LastVisit = timestamppb.New(city.LastActivity)
			place.FirstVisit = timestamppb.New(city.LastActivity)
		}

		places = append(places, place)
	}

	l.InfoContext(ctx, "successfully retrieved frequent places", slog.Int("count", len(places)))

	return connect.NewResponse(&recentsv1.GetFrequentPlacesResponse{
		Places: places,
	}), nil
}
