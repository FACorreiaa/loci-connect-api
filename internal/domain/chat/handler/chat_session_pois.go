package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// sectionFromProto maps the wire enum onto the service's own vocabulary, so
// the service layer never imports generated code.
func sectionFromProto(section chatv1.SessionPOISection) locitypes.SessionPOISection {
	switch section {
	case chatv1.SessionPOISection_SESSION_POI_SECTION_GENERAL:
		return locitypes.SectionGeneral
	case chatv1.SessionPOISection_SESSION_POI_SECTION_RESTAURANTS:
		return locitypes.SectionRestaurants
	case chatv1.SessionPOISection_SESSION_POI_SECTION_HOTELS:
		return locitypes.SectionHotels
	case chatv1.SessionPOISection_SESSION_POI_SECTION_ACTIVITIES:
		return locitypes.SectionActivities
	default:
		return locitypes.SectionItinerary
	}
}

// GetSessionPOIs serves a page of an answer that was already generated.
//
// It is a read: no model call, no quota. The ownership check lives in the
// service, against the user id passed in rather than against the context, so
// the in-process caller (the Telegram bridge) gets the same guarantee this one
// does.
func (h *ChatHandler) GetSessionPOIs(
	ctx context.Context,
	req *connect.Request[chatv1.GetSessionPOIsRequest],
) (*connect.Response[chatv1.GetSessionPOIsResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session ID"))
	}

	page, err := h.service.GetSessionPOIs(ctx, userID, sessionID,
		sectionFromProto(req.Msg.GetSection()),
		int(req.Msg.GetPagination().GetPage()),
		int(req.Msg.GetPagination().GetPageSize()),
	)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.ToGetSessionPOIsResponse(page)), nil
}
