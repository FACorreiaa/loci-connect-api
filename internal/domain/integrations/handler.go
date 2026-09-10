package integrations

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	integrationsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/integrations"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/integrations/integrationsv1connect"
)

// Handler implements the IntegrationService.
//
// Built and registered whether or not an encryption key is configured. With
// none the service is nil and ListConnections answers enabled=false, which the
// settings page renders as an explanation instead of a spinner.
type Handler struct {
	integrationsv1connect.UnimplementedIntegrationServiceHandler
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
		logger: logger.With(slog.String("component", "integrations-handler")),
	}
}

func disabled() error {
	return connect.NewError(connect.CodeFailedPrecondition,
		errors.New("connecting a server is not available on this server: it has no encryption key configured"))
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

func connectionToProto(c Connection) *integrationsv1.Connection {
	pb := &integrationsv1.Connection{
		Provider:  c.Provider,
		Endpoint:  c.Endpoint,
		HasToken:  c.HasToken,
		CreatedAt: timestamppb.New(c.CreatedAt),
		LastError: c.LastError,
	}
	if c.LastSeenAt != nil {
		pb.LastSeenAt = timestamppb.New(*c.LastSeenAt)
	}
	return pb
}

// userText strips the package prefix from a message written for a person.
func userText(err error) string {
	return strings.TrimPrefix(err.Error(), "integrations: ")
}

func (h *Handler) ListConnections(ctx context.Context, _ *connect.Request[integrationsv1.ListConnectionsRequest]) (*connect.Response[integrationsv1.ListConnectionsResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	resp := &integrationsv1.ListConnectionsResponse{Enabled: h.svc.Enabled()}
	if !resp.Enabled {
		return connect.NewResponse(resp), nil
	}

	conns, err := h.svc.List(ctx, userID)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to list integrations", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list connections"))
	}
	for _, c := range conns {
		resp.Connections = append(resp.Connections, connectionToProto(c))
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) Connect(ctx context.Context, req *connect.Request[integrationsv1.ConnectRequest]) (*connect.Response[integrationsv1.ConnectResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if !h.svc.Enabled() {
		return nil, disabled()
	}

	conn, err := h.svc.Connect(ctx, userID, req.Msg.GetProvider(), req.Msg.GetEndpoint(), req.Msg.GetAccessToken())
	if err != nil {
		var input *InputError
		switch {
		case errors.Is(err, ErrSealingUnavailable):
			return nil, disabled()
		case errors.Is(err, ErrUnknownProvider):
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("that is not an integration Loci supports"))
		case errors.As(err, &input):
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(userText(err)))
		}
		h.logger.ErrorContext(ctx, "failed to connect an integration", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to save the connection"))
	}

	h.logger.InfoContext(ctx, "integration connected",
		slog.String("user_id", userID.String()), slog.String("provider", conn.Provider))
	return connect.NewResponse(&integrationsv1.ConnectResponse{Connection: connectionToProto(conn)}), nil
}

func (h *Handler) Disconnect(ctx context.Context, req *connect.Request[integrationsv1.DisconnectRequest]) (*connect.Response[integrationsv1.DisconnectResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if h.svc == nil {
		// Nothing could have been connected, so "disconnected" is true.
		return connect.NewResponse(&integrationsv1.DisconnectResponse{}), nil
	}

	provider := req.Msg.GetProvider()
	if err := h.svc.Disconnect(ctx, userID, provider); err != nil && !errors.Is(err, ErrNotFound) {
		h.logger.ErrorContext(ctx, "failed to disconnect an integration", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to remove the connection"))
	}

	h.logger.InfoContext(ctx, "integration disconnected",
		slog.String("user_id", userID.String()), slog.String("provider", provider))
	return connect.NewResponse(&integrationsv1.DisconnectResponse{}), nil
}

func (h *Handler) TestConnection(ctx context.Context, req *connect.Request[integrationsv1.TestConnectionRequest]) (*connect.Response[integrationsv1.TestConnectionResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if !h.svc.Enabled() {
		return nil, disabled()
	}

	names, err := h.svc.Test(ctx, userID, req.Msg.GetProvider())
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("that integration is not connected"))
	case err != nil:
		// A server that cannot be reached is the answer to the question the
		// button asks, not a failure of the RPC. The messages on this path
		// are written to be shown: none names the token.
		return connect.NewResponse(&integrationsv1.TestConnectionResponse{Ok: false, Error: userText(err)}), nil
	}
	return connect.NewResponse(&integrationsv1.TestConnectionResponse{Ok: true, ToolNames: names}), nil
}
