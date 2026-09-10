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
	apikeyv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/apikey"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/apikey/apikeyv1connect"
)

// Handler implements the ApiKeyService.
type Handler struct {
	apikeyv1connect.UnimplementedApiKeyServiceHandler
	svc    Service
	logger *slog.Logger

	// setup renders the per-client instructions. Nil when the composition
	// root did not supply one, in which case keys are still minted and only
	// the instructions are missing — they are a convenience, the key is the
	// product.
	setup *SetupWriter
}

// NewHandler creates a new API key handler.
func NewHandler(svc Service, logger *slog.Logger) *Handler {
	return &Handler{
		svc:    svc,
		logger: logger.With(slog.String("component", "apikey-handler")),
	}
}

// WithSetup gives the handler a writer for setup instructions.
//
// Supplied by the composition root rather than built here because the writer
// needs the MCP endpoint and tool names, which live in internal/mcp — a
// package that imports this one.
func (h *Handler) WithSetup(writer *SetupWriter) *Handler {
	h.setup = writer
	return h
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
	pb.Scopes = ScopeStrings(k.Scopes)
	pb.ClientKind = string(k.ClientKind)
	return pb
}

func setupToProto(s Setup) *apikeyv1.SetupInstructions {
	return &apikeyv1.SetupInstructions{
		ClientKind:  string(s.Kind),
		Endpoint:    s.Endpoint,
		ConfigLabel: s.ConfigLabel,
		ConfigLang:  s.ConfigLang,
		Config:      s.Config,
		SafeLabel:   s.SafeLabel,
		SafeLang:    s.SafeLang,
		Safe:        s.Safe,
		SafeNote:    s.SafeNote,
		ExportLine:  s.Export,
		Prompt:      s.Prompt,
	}
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

	// Omitted scopes mean read-only. An unrecognised scope is rejected rather
	// than dropped: silently narrowing the grant would hand back a key weaker
	// than the caller believes they hold, and they would only discover it at the
	// first refusal.
	scopes, err := ParseScopes(req.Msg.GetScopes())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Empty means the generic client; anything else must be a kind there are
	// instructions for. See ParseClientKind for why unknown is an error.
	clientKind, err := ParseClientKind(req.Msg.GetClientKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	key, plaintext, err := h.svc.Create(ctx, userID, req.Msg.GetName(), expiresAt, scopes, clientKind)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to create api key", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create api key"))
	}

	h.logger.InfoContext(ctx, "api key created",
		slog.String("user_id", userID.String()),
		slog.String("key_id", key.ID.String()),
		slog.String("client_kind", string(key.ClientKind)),
		slog.String("scopes", JoinScopes(key.Scopes)))

	resp := &apikeyv1.CreateApiKeyResponse{
		ApiKey:       toProto(key),
		PlaintextKey: plaintext,
	}
	// The only response that can carry instructions with the real key in
	// them: the plaintext exists here and nowhere else.
	if h.setup != nil {
		resp.Setup = setupToProto(h.setup.Instructions(clientKind, plaintext))
	}
	return connect.NewResponse(resp), nil
}

// GetSetupInstructions renders the setup for a client with the placeholder
// where the key goes, so the page can show what connecting looks like without
// minting a credential to find out.
func (h *Handler) GetSetupInstructions(ctx context.Context, req *connect.Request[apikeyv1.GetSetupInstructionsRequest]) (*connect.Response[apikeyv1.GetSetupInstructionsResponse], error) {
	if _, err := callerID(ctx); err != nil {
		return nil, err
	}
	if h.setup == nil {
		// Unavailable rather than Unimplemented: the RPC exists, this
		// deployment has no address to write into it.
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("setup instructions are not configured on this server"))
	}

	kind, err := ParseClientKind(req.Msg.GetClientKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&apikeyv1.GetSetupInstructionsResponse{
		Instructions: setupToProto(h.setup.Preview(kind)),
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
