package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	memoryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/memory"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/memory/memoryv1connect"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler serves MemoryService.
type Handler struct {
	memoryv1connect.UnimplementedMemoryServiceHandler
	svc    *Service
	logger *slog.Logger
}

// NewHandler wires the handler.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, logger: logger.With(slog.String("component", "memory-handler"))}
}

func callerID(ctx context.Context) (uuid.UUID, error) {
	idStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || idStr == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user identity"))
	}
	return id, nil
}

// GetMemory returns the caller's learned profile.
func (h *Handler) GetMemory(
	ctx context.Context,
	req *connect.Request[memoryv1.GetMemoryRequest],
) (*connect.Response[memoryv1.GetMemoryResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	profile, err := h.svc.Get(ctx, userID, req.Msg.GetIncludeEvidence())
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to read memory", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read memory"))
	}

	resp := &memoryv1.GetMemoryResponse{
		PersonalizationEnabled: profile.PersonalizationEnabled,
		HasVector:              profile.HasVector,
		SignalCount:            int32(profile.SignalCount),
		GeneratedAt:            timestamppb.New(profile.GeneratedAt),
	}
	if profile.LastSignalAt != nil {
		resp.LastSignalAt = timestamppb.New(*profile.LastSignalAt)
	}
	for _, t := range profile.Traits {
		resp.Traits = append(resp.Traits, traitToProto(t))
	}
	return connect.NewResponse(resp), nil
}

func traitToProto(t Trait) *memoryv1.Trait {
	pb := &memoryv1.Trait{
		Key:           t.Key,
		Label:         t.Label,
		Score:         t.Score,
		Confidence:    t.Confidence,
		EvidenceCount: int32(t.EvidenceCount),
		UpdatedAt:     timestamppb.New(t.UpdatedAt),
	}
	for _, e := range t.Evidence {
		pb.Evidence = append(pb.Evidence, &memoryv1.Evidence{
			Id:         e.ID.String(),
			FeedbackId: e.FeedbackID.String(),
			Event:      e.Event,
			Weight:     e.Weight,
			PoiId:      e.POIID,
			PoiName:    e.POIName,
			CityName:   e.CityName,
			OccurredAt: timestamppb.New(e.OccurredAt),
		})
	}
	return pb
}

// ForgetTrait removes a belief and the signals behind it.
func (h *Handler) ForgetTrait(
	ctx context.Context,
	req *connect.Request[memoryv1.ForgetTraitRequest],
) (*connect.Response[memoryv1.ForgetTraitResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	traitKey := req.Msg.GetTraitKey()
	if traitKey == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("trait_key is required"))
	}

	removed, err := h.svc.ForgetTrait(ctx, userID, traitKey)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to forget trait",
			slog.String("trait_key", traitKey), slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to forget trait"))
	}

	h.logger.InfoContext(ctx, "trait forgotten",
		slog.String("user_id", userID.String()),
		slog.String("trait_key", traitKey),
		slog.Int("signals_removed", removed))
	return connect.NewResponse(&memoryv1.ForgetTraitResponse{
		SignalsRemoved: int32(removed),
	}), nil
}

// ForgetEvidence removes one recorded action.
func (h *Handler) ForgetEvidence(
	ctx context.Context,
	req *connect.Request[memoryv1.ForgetEvidenceRequest],
) (*connect.Response[memoryv1.ForgetEvidenceResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	feedbackID, err := uuid.Parse(req.Msg.GetFeedbackId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid feedback id: %w", err))
	}

	if err := h.svc.ForgetEvidence(ctx, userID, feedbackID); err != nil {
		if errors.Is(err, ErrNotFound) {
			// Not-found and not-yours are deliberately the same response: a
			// distinct error would confirm that someone else's id exists.
			return nil, connect.NewError(connect.CodeNotFound, errors.New("evidence not found"))
		}
		h.logger.ErrorContext(ctx, "failed to forget evidence", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to forget evidence"))
	}

	h.logger.InfoContext(ctx, "evidence forgotten",
		slog.String("user_id", userID.String()),
		slog.String("feedback_id", feedbackID.String()))
	return connect.NewResponse(&memoryv1.ForgetEvidenceResponse{}), nil
}
