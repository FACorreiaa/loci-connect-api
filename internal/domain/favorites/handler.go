package favorites

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/apierr"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	favoritesv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/favorites/v1"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/favorites/v1/favoritesv1connect"
)

// Handler implements the FavoritesService
type Handler struct {
	favoritesv1connect.UnimplementedFavoritesServiceHandler
	repo   Repository
	plans  PlanChecker
	prefs  preference.Recorder
	logger *slog.Logger
}

// PlanChecker is the subset of subscription.Service needed for freemium gates.
type PlanChecker interface {
	EffectivePlan(ctx context.Context, userID uuid.UUID) (string, error)
}

// NewHandler creates a new favorites handler. plans/prefs may be nil.
func NewHandler(repo Repository, logger *slog.Logger, plans PlanChecker, prefs preference.Recorder) *Handler {
	return &Handler{
		repo:   repo,
		plans:  plans,
		prefs:  prefs,
		logger: logger.With(slog.String("component", "favorites-handler")),
	}
}

// contentTypeToString converts proto enum to string
func contentTypeToString(ct favoritesv1.ContentType) string {
	switch ct {
	case favoritesv1.ContentType_CONTENT_TYPE_POI:
		return "poi"
	case favoritesv1.ContentType_CONTENT_TYPE_HOTEL:
		return "hotel"
	case favoritesv1.ContentType_CONTENT_TYPE_RESTAURANT:
		return "restaurant"
	case favoritesv1.ContentType_CONTENT_TYPE_ITINERARY:
		return "itinerary"
	default:
		return "poi"
	}
}

// stringToContentType converts string to proto enum
func stringToContentType(s string) favoritesv1.ContentType {
	switch s {
	case "hotel":
		return favoritesv1.ContentType_CONTENT_TYPE_HOTEL
	case "restaurant":
		return favoritesv1.ContentType_CONTENT_TYPE_RESTAURANT
	case "itinerary":
		return favoritesv1.ContentType_CONTENT_TYPE_ITINERARY
	default:
		return favoritesv1.ContentType_CONTENT_TYPE_POI
	}
}

// AddToFavorites adds an item to favorites
func (h *Handler) AddToFavorites(
	ctx context.Context,
	req *connect.Request[favoritesv1.AddToFavoritesRequest],
) (*connect.Response[favoritesv1.AddToFavoritesResponse], error) {
	l := h.logger.With(slog.String("method", "AddToFavorites"))

	// Get user ID from context (returns string)
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		l.ErrorContext(ctx, "invalid user ID format", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	if err := h.enforcePlaceLimit(ctx, userID); err != nil {
		return nil, apierr.ToConnect(err)
	}

	// Parse item ID - could be name or UUID
	itemID := req.Msg.ItemId
	if itemID == "" {
		itemID = req.Msg.ItemName // Fall back to name if no ID
	}

	fav := &locitypes.FavoriteItem{
		UserID:      userID,
		ItemID:      itemID,
		ItemName:    req.Msg.ItemName,
		ContentType: contentTypeToString(req.Msg.ContentType),
		Notes:       req.Msg.Notes,
		Description: req.Msg.Description,
		CityName:    req.Msg.CityName,
		Latitude:    req.Msg.Latitude,
		Longitude:   req.Msg.Longitude,
		Rating:      req.Msg.Rating,
		Category:    req.Msg.Category,
	}

	result, err := h.repo.AddFavorite(ctx, fav)
	if err != nil {
		l.ErrorContext(ctx, "failed to add favorite", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to add favorite"))
	}

	if h.prefs != nil {
		h.prefs.Record(ctx, userID, preference.EventFavorited, preference.RecordOpts{
			POIID:    itemID,
			Metadata: map[string]any{"content_type": fav.ContentType, "name": fav.ItemName},
		})
	}

	l.InfoContext(ctx, "added to favorites",
		slog.String("user_id", userID.String()),
		slog.String("item_id", itemID),
		slog.String("content_type", fav.ContentType))

	return connect.NewResponse(&favoritesv1.AddToFavoritesResponse{
		Success: true,
		Message: "Added to favorites",
		Favorite: &favoritesv1.FavoriteItem{
			Id:          result.ID.String(),
			UserId:      result.UserID.String(),
			ItemId:      result.ItemID,
			ItemName:    result.ItemName,
			ContentType: stringToContentType(result.ContentType),
			Notes:       result.Notes,
			Description: result.Description,
			CityName:    result.CityName,
			Latitude:    result.Latitude,
			Longitude:   result.Longitude,
			Rating:      result.Rating,
			Category:    result.Category,
			AddedAt:     timestamppb.New(result.AddedAt),
		},
	}), nil
}

// RemoveFromFavorites removes an item from favorites
func (h *Handler) RemoveFromFavorites(
	ctx context.Context,
	req *connect.Request[favoritesv1.RemoveFromFavoritesRequest],
) (*connect.Response[favoritesv1.RemoveFromFavoritesResponse], error) {
	l := h.logger.With(slog.String("method", "RemoveFromFavorites"))

	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		l.ErrorContext(ctx, "invalid user ID format", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	// Parse item ID - it might be a UUID or a name string
	itemUUID, err := uuid.Parse(req.Msg.ItemId)
	if err != nil {
		// If not a valid UUID, use uuid.Nil and we'll match by string item_id
		itemUUID = uuid.Nil
	}

	contentType := contentTypeToString(req.Msg.ContentType)

	// Pass the original item ID string for matching
	err = h.repo.RemoveFavorite(ctx, userID, itemUUID, contentType)
	if err != nil {
		l.ErrorContext(ctx, "failed to remove favorite", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to remove favorite"))
	}

	l.InfoContext(ctx, "removed from favorites",
		slog.String("user_id", userID.String()),
		slog.String("item_id", req.Msg.ItemId))

	return connect.NewResponse(&favoritesv1.RemoveFromFavoritesResponse{
		Success: true,
		Message: "Removed from favorites",
	}), nil
}

// GetFavorites retrieves all favorites for a user
func (h *Handler) GetFavorites(
	ctx context.Context,
	req *connect.Request[favoritesv1.GetFavoritesRequest],
) (*connect.Response[favoritesv1.GetFavoritesResponse], error) {
	l := h.logger.With(slog.String("method", "GetFavorites"))

	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	contentType := contentTypeToString(req.Msg.ContentType)
	if req.Msg.ContentType == favoritesv1.ContentType_CONTENT_TYPE_UNSPECIFIED {
		contentType = ""
	}

	limit := int(req.Msg.Limit)
	if limit <= 0 {
		limit = 100
	}
	offset := int(req.Msg.Offset)

	favorites, totalCount, err := h.repo.GetFavorites(ctx, userID, contentType, limit, offset)
	if err != nil {
		l.ErrorContext(ctx, "failed to get favorites", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to get favorites"))
	}

	protoFavorites := make([]*favoritesv1.FavoriteItem, len(favorites))
	for i, fav := range favorites {
		protoFavorites[i] = &favoritesv1.FavoriteItem{
			Id:          fav.ID.String(),
			UserId:      fav.UserID.String(),
			ItemId:      fav.ItemID,
			ItemName:    fav.ItemName,
			ContentType: stringToContentType(fav.ContentType),
			Notes:       fav.Notes,
			Description: fav.Description,
			CityName:    fav.CityName,
			Latitude:    fav.Latitude,
			Longitude:   fav.Longitude,
			Rating:      fav.Rating,
			Category:    fav.Category,
			AddedAt:     timestamppb.New(fav.AddedAt),
		}
	}

	l.InfoContext(ctx, "retrieved favorites",
		slog.String("user_id", userID.String()),
		slog.Int("count", len(favorites)))

	return connect.NewResponse(&favoritesv1.GetFavoritesResponse{
		Favorites:  protoFavorites,
		TotalCount: int32(totalCount),
	}), nil
}

// IsFavorited checks if an item is favorited
func (h *Handler) IsFavorited(
	ctx context.Context,
	req *connect.Request[favoritesv1.IsFavoritedRequest],
) (*connect.Response[favoritesv1.IsFavoritedResponse], error) {
	l := h.logger.With(slog.String("method", "IsFavorited"))

	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	// Parse item ID
	itemUUID, err := uuid.Parse(req.Msg.ItemId)
	if err != nil {
		itemUUID = uuid.Nil
	}

	contentType := contentTypeToString(req.Msg.ContentType)

	isFavorited, err := h.repo.IsFavorited(ctx, userID, itemUUID, contentType)
	if err != nil {
		l.ErrorContext(ctx, "failed to check favorite", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to check favorite"))
	}

	return connect.NewResponse(&favoritesv1.IsFavoritedResponse{
		IsFavorited: isFavorited,
	}), nil
}

// GetFavoritesCount returns the count of favorites
func (h *Handler) GetFavoritesCount(
	ctx context.Context,
	req *connect.Request[favoritesv1.GetFavoritesCountRequest],
) (*connect.Response[favoritesv1.GetFavoritesCountResponse], error) {
	l := h.logger.With(slog.String("method", "GetFavoritesCount"))

	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	contentType := contentTypeToString(req.Msg.ContentType)
	if req.Msg.ContentType == favoritesv1.ContentType_CONTENT_TYPE_UNSPECIFIED {
		contentType = ""
	}

	count, err := h.repo.GetFavoritesCount(ctx, userID, contentType)
	if err != nil {
		l.ErrorContext(ctx, "failed to get favorites count", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to get favorites count"))
	}

	l.InfoContext(ctx, "retrieved favorites count",
		slog.String("user_id", userID.String()),
		slog.Int("count", count))

	return connect.NewResponse(&favoritesv1.GetFavoritesCountResponse{
		Count: int32(count),
	}), nil
}

func (h *Handler) enforcePlaceLimit(ctx context.Context, userID uuid.UUID) error {
	if h.plans == nil {
		return nil
	}
	plan, err := h.plans.EffectivePlan(ctx, userID)
	if err != nil {
		return err
	}
	n, err := h.repo.GetFavoritesCount(ctx, userID, "")
	if err != nil {
		return err
	}
	return subscription.CheckPlaceAdd(plan, n)
}
