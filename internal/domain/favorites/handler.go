package favorites

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/apierr"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	favoritesv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/favorites/v1"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/favorites/v1/favoritesv1connect"
)

// Handler implements the FavoritesService
type Handler struct {
	favoritesv1connect.UnimplementedFavoritesServiceHandler
	repo      Repository
	plans     PlanChecker
	listItems ListItemCounter
	prefs     preference.Recorder
	places    PlaceReader
	field     FieldScorer
	logger    *slog.Logger
}

// FieldScorer is the field score (gamification.Service): a first save and a
// note in the traveller's own words each count once per place.
type FieldScorer interface {
	FavoriteSaved(ctx context.Context, userID uuid.UUID, itemID, contentType, name, cityName string) int
	FavoriteNoted(ctx context.Context, userID uuid.UUID, itemID, contentType, name, cityName, note string) (int, bool)
}

// PlanChecker is the subset of subscription.Service needed for freemium gates.
type PlanChecker interface {
	EffectivePlan(ctx context.Context, userID uuid.UUID) (string, error)
}

// ListItemCounter tallies list saves so favorites share the free-tier place cap.
type ListItemCounter interface {
	CountUserListItems(ctx context.Context, userID uuid.UUID) (int, error)
}

// NewHandler creates a new favorites handler. Optional deps may be nil.
func NewHandler(
	repo Repository,
	logger *slog.Logger,
	plans PlanChecker,
	prefs preference.Recorder,
	listItems ListItemCounter,
) *Handler {
	return &Handler{
		repo:      repo,
		plans:     plans,
		prefs:     prefs,
		listItems: listItems,
		logger:    logger.With(slog.String("component", "favorites-handler")),
	}
}

// WithPlaces lets the detail and nearby RPCs read stored hotels and
// restaurants. Without it they answer from saved snapshots alone.
func (h *Handler) WithPlaces(places PlaceReader) *Handler {
	h.places = places
	return h
}

// WithScorer turns on field points for saves and notes.
func (h *Handler) WithScorer(s FieldScorer) *Handler {
	h.field = s
	return h
}

func favoriteToProto(f *locitypes.FavoriteItem) *favoritesv1.FavoriteItem {
	return &favoritesv1.FavoriteItem{
		Id:          f.ID.String(),
		UserId:      f.UserID.String(),
		ItemId:      f.ItemID,
		ItemName:    f.ItemName,
		ContentType: stringToContentType(f.ContentType),
		Notes:       f.Notes,
		Description: f.Description,
		CityName:    f.CityName,
		Latitude:    f.Latitude,
		Longitude:   f.Longitude,
		Rating:      f.Rating,
		Category:    f.Category,
		AddedAt:     timestamppb.New(f.AddedAt),
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

	if h.prefs != nil && req.Msg.GetRecommendationTrace() == nil {
		h.prefs.Record(ctx, userID, preference.EventFavorited, preference.RecordOpts{
			POIID:    itemID,
			Metadata: map[string]any{"content_type": fav.ContentType, "name": fav.ItemName},
		})
	}

	l.InfoContext(ctx, "added to favorites",
		slog.String("user_id", userID.String()),
		slog.String("item_id", itemID),
		slog.String("content_type", fav.ContentType))

	if h.field != nil {
		h.field.FavoriteSaved(ctx, userID, result.ItemID, result.ContentType, result.ItemName, result.CityName)
		if result.Notes != "" {
			h.field.FavoriteNoted(ctx, userID, result.ItemID, result.ContentType, result.ItemName, result.CityName, result.Notes)
		}
	}

	return connect.NewResponse(&favoritesv1.AddToFavoritesResponse{
		Success:  true,
		Message:  "Added to favorites",
		Favorite: favoriteToProto(result),
	}), nil
}

// UpdateFavoriteNote sets the caller's note on one of their saved items.
func (h *Handler) UpdateFavoriteNote(
	ctx context.Context,
	req *connect.Request[favoritesv1.UpdateFavoriteNoteRequest],
) (*connect.Response[favoritesv1.UpdateFavoriteNoteResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}
	contentType := contentTypeToString(req.Msg.GetContentType())
	fav, err := h.repo.UpdateNote(ctx, userID, req.Msg.GetItemId(), contentType, strings.TrimSpace(req.Msg.GetNotes()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to save the note"))
	}
	if fav == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not in your saved places"))
	}
	out := &favoritesv1.UpdateFavoriteNoteResponse{Favorite: favoriteToProto(fav)}
	if h.field != nil && fav.Notes != "" {
		points, counts := h.field.FavoriteNoted(ctx, userID, fav.ItemID, fav.ContentType, fav.ItemName, fav.CityName, fav.Notes)
		out.PointsAwarded, out.NoteCounts = int32(points), counts
	}
	return connect.NewResponse(out), nil
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

	// item_id is a TEXT column: AddToFavorites stores whatever the client sent,
	// which is a place id for some sources and a name for others. Coercing it
	// through uuid.Parse here matched nothing and deleted no rows.
	contentType := contentTypeToString(req.Msg.ContentType)

	err = h.repo.RemoveFavorite(ctx, userID, req.Msg.ItemId, contentType)
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

	// Compared as the text it is stored as; see RemoveFromFavorites.
	contentType := contentTypeToString(req.Msg.ContentType)

	isFavorited, err := h.repo.IsFavorited(ctx, userID, req.Msg.ItemId, contentType)
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
	if h.listItems != nil {
		items, lerr := h.listItems.CountUserListItems(ctx, userID)
		if lerr != nil {
			return lerr
		}
		n += items
	}
	return subscription.CheckPlaceAdd(plan, n)
}
