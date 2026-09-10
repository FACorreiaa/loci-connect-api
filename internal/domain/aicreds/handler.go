package aicreds

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/ai/providers"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	aicredsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/aicreds"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/aicreds/aicredsv1connect"
)

// Handler implements the AiCredentialService.
//
// Built and registered whether or not bring-your-own-key is on. With no
// encryption key the service is nil and every read answers enabled=false, so
// the settings page can explain the absence; the alternative — not registering
// the service — is an Unimplemented error the page can only show as a spinner.
type Handler struct {
	aicredsv1connect.UnimplementedAiCredentialServiceHandler
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
		logger: logger.With(slog.String("component", "aicreds-handler")),
	}
}

// disabled is the error a write gets when there is nowhere safe to put a key.
func disabled() error {
	return connect.NewError(connect.CodeFailedPrecondition,
		errors.New("bring-your-own-key is not available on this server: it has no encryption key configured"))
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

func credentialToProto(c Credential) *aicredsv1.Credential {
	pb := &aicredsv1.Credential{
		Provider:  c.Provider,
		KeyHint:   c.KeyHint,
		Model:     c.Model,
		BaseUrl:   c.BaseURL,
		LastError: c.LastError,
		CreatedAt: timestamppb.New(c.CreatedAt),
		UpdatedAt: timestamppb.New(c.UpdatedAt),
	}
	if c.LastErrorAt != nil {
		pb.LastErrorAt = timestamppb.New(*c.LastErrorAt)
	}
	return pb
}

func providerToProto(p providers.BYOProvider) *aicredsv1.Provider {
	return &aicredsv1.Provider{
		Name:                 p.Name,
		Label:                p.Label,
		DefaultModel:         p.DefaultModel,
		KeyHint:              p.KeyHint,
		Note:                 p.Note,
		RequiresBaseUrl:      p.RequiresBaseURL,
		SupportsVerification: p.VerifyPath != "",
	}
}

func (h *Handler) GetCredential(ctx context.Context, _ *connect.Request[aicredsv1.GetCredentialRequest]) (*connect.Response[aicredsv1.GetCredentialResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	resp := &aicredsv1.GetCredentialResponse{Enabled: h.svc.Enabled()}
	if !resp.Enabled {
		return connect.NewResponse(resp), nil
	}

	cred, err := h.svc.Get(ctx, userID)
	switch {
	case errors.Is(err, ErrNotFound):
		// The ordinary case: this account runs on Loci's provider. Not an
		// error — the page shows the form, not a failure.
		return connect.NewResponse(resp), nil
	case err != nil:
		h.logger.ErrorContext(ctx, "failed to read provider credential", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read the credential"))
	}
	resp.Credential = credentialToProto(cred)
	return connect.NewResponse(resp), nil
}

func (h *Handler) SaveCredential(ctx context.Context, req *connect.Request[aicredsv1.SaveCredentialRequest]) (*connect.Response[aicredsv1.SaveCredentialResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if !h.svc.Enabled() {
		return nil, disabled()
	}

	cred, err := h.svc.Save(ctx, userID, Input{
		Provider: req.Msg.GetProvider(),
		APIKey:   req.Msg.GetApiKey(),
		Model:    req.Msg.GetModel(),
		BaseURL:  req.Msg.GetBaseUrl(),
	})
	if err != nil {
		return nil, h.saveError(ctx, err)
	}

	h.logger.InfoContext(ctx, "provider credential saved",
		slog.String("user_id", userID.String()),
		slog.String("provider", cred.Provider))
	return connect.NewResponse(&aicredsv1.SaveCredentialResponse{Credential: credentialToProto(cred)}), nil
}

// saveError turns a service failure into what the caller should see.
//
// Everything the person can fix is InvalidArgument with a sentence naming what
// to change. Everything else is Internal with the detail kept to the log, since
// a database error is not theirs to read and could name things they should not
// see.
func (h *Handler) saveError(ctx context.Context, err error) error {
	var input *InputError
	switch {
	case errors.Is(err, ErrSealingUnavailable):
		return disabled()
	case errors.Is(err, ErrUnknownProvider):
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("that is not a provider Loci can use a key for"))
	case errors.Is(err, ErrKeyRejected):
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("the provider rejected this key; check it and try again"))
	case errors.Is(err, ErrKeyRequired), errors.As(err, &input):
		return connect.NewError(connect.CodeInvalidArgument, errors.New(userText(err)))
	}
	h.logger.ErrorContext(ctx, "failed to save provider credential", slog.Any("error", err))
	return connect.NewError(connect.CodeInternal, errors.New("failed to save the credential"))
}

// userText strips the package prefix from a message written for a person.
func userText(err error) string {
	return strings.TrimPrefix(err.Error(), "aicreds: ")
}

func (h *Handler) DeleteCredential(ctx context.Context, _ *connect.Request[aicredsv1.DeleteCredentialRequest]) (*connect.Response[aicredsv1.DeleteCredentialResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if h.svc == nil {
		// Nothing could have been stored, so there is nothing to remove, and
		// "removed" is the truthful answer.
		return connect.NewResponse(&aicredsv1.DeleteCredentialResponse{}), nil
	}

	if err := h.svc.Delete(ctx, userID); err != nil && !errors.Is(err, ErrNotFound) {
		h.logger.ErrorContext(ctx, "failed to delete provider credential", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to remove the credential"))
	}

	h.logger.InfoContext(ctx, "provider credential removed", slog.String("user_id", userID.String()))
	return connect.NewResponse(&aicredsv1.DeleteCredentialResponse{}), nil
}

func (h *Handler) ListProviders(ctx context.Context, _ *connect.Request[aicredsv1.ListProvidersRequest]) (*connect.Response[aicredsv1.ListProvidersResponse], error) {
	if _, err := callerID(ctx); err != nil {
		return nil, err
	}

	// The catalogue is listed even when saving is off, so the page can show
	// what would be possible and say why it is not.
	resp := &aicredsv1.ListProvidersResponse{Enabled: h.svc.Enabled()}
	for _, p := range providers.Catalog {
		resp.Providers = append(resp.Providers, providerToProto(p))
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) VerifyCredential(ctx context.Context, _ *connect.Request[aicredsv1.VerifyCredentialRequest]) (*connect.Response[aicredsv1.VerifyCredentialResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	if !h.svc.Enabled() {
		return nil, disabled()
	}

	result, err := h.svc.VerifyStored(ctx, userID)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no provider key is stored on this account"))
	case err != nil:
		// A stored key that will not open. The same thing Resolve reports and
		// records; here the person is looking, so they get the sentence.
		h.logger.WarnContext(ctx, "could not open the stored credential to verify it",
			slog.String("user_id", userID.String()), slog.Any("error", err))
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("the stored key cannot be opened; remove it and save it again"))
	}

	return connect.NewResponse(&aicredsv1.VerifyCredentialResponse{
		Ok:      result.Checked && !result.Rejected,
		Checked: result.Checked,
		Error:   result.Detail,
	}), nil
}
