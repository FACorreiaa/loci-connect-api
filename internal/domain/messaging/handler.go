package messaging

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	messagingv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/messaging"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/messaging/messagingv1connect"
)

// Handler implements the MessagingService: the settings-page half of linking
// a chat to an account. The chat half is Service.Handle, driven by the
// platform adapter.
//
// Built and registered whether or not a bot is configured. With none the
// service is nil and GetLink answers with an empty bot_handle, which is the
// proto's own signal for "no bot on this server" and what the page renders as
// an explanation instead of a spinner.
type Handler struct {
	messagingv1connect.UnimplementedMessagingServiceHandler
	svc    *Service
	logger *slog.Logger
}

// NewHandler creates the handler. svc may be nil.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		svc:    svc,
		logger: logger.With(slog.String("component", "messaging-handler")),
	}
}

func notConfigured() error {
	return connect.NewError(connect.CodeFailedPrecondition,
		errors.New("no chat platform is configured on this server"))
}

func callerID(ctx context.Context) (uuid.UUID, error) {
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user identity"))
	}
	return userID, nil
}

// platformOf reads the platform from a request. Empty means Telegram, the only
// platform there is, so a client that predates the field still works.
func platformOf(raw string) string {
	if raw == "" {
		return PlatformTelegram
	}
	return raw
}

func linkToProto(l Link) *messagingv1.Link {
	pb := &messagingv1.Link{
		Platform:    l.Platform,
		DisplayName: l.DisplayName,
		LinkedAt:    timestamppb.New(l.LinkedAt),
	}
	if l.LastSeenAt != nil {
		pb.LastSeenAt = timestamppb.New(*l.LastSeenAt)
	}
	return pb
}

func (h *Handler) GetLink(ctx context.Context, req *connect.Request[messagingv1.GetLinkRequest]) (*connect.Response[messagingv1.GetLinkResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if h.svc == nil {
		// No bot handle and no link: the page explains the absence rather
		// than offering a code nothing can redeem.
		return connect.NewResponse(&messagingv1.GetLinkResponse{}), nil
	}

	resp := &messagingv1.GetLinkResponse{BotHandle: h.svc.BotHandle()}
	link, err := h.svc.Status(ctx, userID, platformOf(req.Msg.GetPlatform()))
	switch {
	case errors.Is(err, ErrNotLinked):
		// The ordinary state for most accounts, and the page's cue to offer
		// a code. Not an error.
		return connect.NewResponse(resp), nil
	case err != nil:
		h.logger.ErrorContext(ctx, "failed to read messaging link", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read the link"))
	}
	resp.Link = linkToProto(link)
	return connect.NewResponse(resp), nil
}

func (h *Handler) CreateLinkCode(ctx context.Context, req *connect.Request[messagingv1.CreateLinkCodeRequest]) (*connect.Response[messagingv1.CreateLinkCodeResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if h.svc == nil {
		return nil, notConfigured()
	}

	platform := platformOf(req.Msg.GetPlatform())
	code, expiresAt, err := h.svc.IssueCode(ctx, userID, platform)
	if err != nil {
		if platform != PlatformTelegram {
			// The only way IssueCode refuses on input.
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("that is not a platform Loci links"))
		}
		h.logger.ErrorContext(ctx, "failed to issue a link code",
			slog.String("user_id", userID.String()), slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to issue a link code"))
	}

	// The code itself is deliberately not logged: it is a credential for the
	// next fifteen minutes.
	h.logger.InfoContext(ctx, "link code issued",
		slog.String("user_id", userID.String()), slog.String("platform", platform))
	return connect.NewResponse(&messagingv1.CreateLinkCodeResponse{
		Code:      code,
		ExpiresAt: timestamppb.New(expiresAt),
		BotHandle: h.svc.BotHandle(),
	}), nil
}

func (h *Handler) Unlink(ctx context.Context, req *connect.Request[messagingv1.UnlinkRequest]) (*connect.Response[messagingv1.UnlinkResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if h.svc == nil {
		// Nothing could be linked, so "unlinked" is the truthful answer.
		return connect.NewResponse(&messagingv1.UnlinkResponse{}), nil
	}

	platform := platformOf(req.Msg.GetPlatform())
	if err := h.svc.Unlink(ctx, userID, platform); err != nil && !errors.Is(err, ErrNotLinked) {
		h.logger.ErrorContext(ctx, "failed to unlink", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to unlink"))
	}

	h.logger.InfoContext(ctx, "chat unlinked from settings",
		slog.String("user_id", userID.String()), slog.String("platform", platform))
	return connect.NewResponse(&messagingv1.UnlinkResponse{}), nil
}
