package apikey

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	apikeyv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/apikey"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/apikey/apikeyv1connect"
)

// Handler implements the ApiKeyService.
type Handler struct {
	apikeyv1connect.UnimplementedApiKeyServiceHandler
	svc    Service
	logger *slog.Logger
}

// NewHandler creates a new API key handler.
func NewHandler(svc Service, logger *slog.Logger) *Handler {
	return &Handler{
		svc:    svc,
		logger: logger.With(slog.String("component", "apikey-handler")),
	}
}

func callerID(ctx context.Context) (uuid.UUID, error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user identity"))
	}
	return userID, nil
}

func toProto(k *Key) *apikeyv1.ApiKey {
	pb := &apikeyv1.ApiKey{
		Id:        k.ID.String(),
		Name:      k.Name,
		KeyPrefix: k.KeyPrefix,
		CreatedAt: timestamppb.New(k.CreatedAt),
	}
	if k.LastUsedAt != nil {
		pb.LastUsedAt = timestamppb.New(*k.LastUsedAt)
	}
	if k.ExpiresAt != nil {
		pb.ExpiresAt = timestamppb.New(*k.ExpiresAt)
	}
	if k.RevokedAt != nil {
		pb.RevokedAt = timestamppb.New(*k.RevokedAt)
	}
	return pb
}

func (h *Handler) CreateApiKey(ctx context.Context, req *connect.Request[apikeyv1.CreateApiKeyRequest]) (*connect.Response[apikeyv1.CreateApiKeyResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	var expiresAt *time.Time
	if req.Msg.GetExpiresAt() != nil {
		t := req.Msg.GetExpiresAt().AsTime()
		if !t.After(time.Now()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("expires_at must be in the future"))
		}
		expiresAt = &t
	}

	key, plaintext, err := h.svc.Create(ctx, userID, req.Msg.GetName(), expiresAt)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to create api key", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create api key"))
	}

	h.logger.InfoContext(ctx, "api key created",
		slog.String("user_id", userID.String()),
		slog.String("key_id", key.ID.String()))
	return connect.NewResponse(&apikeyv1.CreateApiKeyResponse{
		ApiKey:       toProto(key),
		PlaintextKey: plaintext,
	}), nil
}

func (h *Handler) ListApiKeys(ctx context.Context, _ *connect.Request[apikeyv1.ListApiKeysRequest]) (*connect.Response[apikeyv1.ListApiKeysResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	keys, err := h.svc.List(ctx, userID)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to list api keys", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list api keys"))
	}

	resp := &apikeyv1.ListApiKeysResponse{}
	for i := range keys {
		resp.ApiKeys = append(resp.ApiKeys, toProto(&keys[i]))
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) RevokeApiKey(ctx context.Context, req *connect.Request[apikeyv1.RevokeApiKeyRequest]) (*connect.Response[apikeyv1.RevokeApiKeyResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	keyID, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid api key id"))
	}

	if err := h.svc.Revoke(ctx, userID, keyID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("api key not found"))
		}
		h.logger.ErrorContext(ctx, "failed to revoke api key", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to revoke api key"))
	}

	h.logger.InfoContext(ctx, "api key revoked",
		slog.String("user_id", userID.String()),
		slog.String("key_id", keyID.String()))
	return connect.NewResponse(&apikeyv1.RevokeApiKeyResponse{}), nil
}
