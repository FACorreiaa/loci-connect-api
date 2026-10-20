package journal

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	journalv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/journal"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/journal/journalconnect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements JournalService.
type Handler struct {
	journalconnect.UnimplementedJournalServiceHandler
	svc *Service
	log *slog.Logger
}

// NewHandler builds the journal Connect handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log.With(slog.String("component", "journal-handler"))}
}

// userID and toConnectErr are local, as in the other domains (see
// travelhistory/handler.go for why).
func userID(ctx context.Context) (uuid.UUID, error) {
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	return id, nil
}

func (h *Handler) toConnectErr(ctx context.Context, op string, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	case errors.Is(err, ErrNoStorage):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("photos are not available"))
	case errors.Is(err, ErrUploadMissing):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the photo has not finished uploading"))
	case errors.Is(err, ErrBadUpload):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the uploaded file does not match the request"))
	case errors.Is(err, ErrBadPhoto):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("photos must be JPEG, PNG or HEIC and at most 8 MB"))
	}
	h.log.ErrorContext(ctx, op+" failed", slog.Any("error", err))
	return connect.NewError(connect.CodeInternal, errors.New("journal unavailable"))
}

func parseID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid "+what))
	}
	return id, nil
}

func parseOptionalID(s *string, what string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := parseID(*s, what)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (h *Handler) GetTripJournal(ctx context.Context, req *connect.Request[journalv1.GetTripJournalRequest]) (*connect.Response[journalv1.GetTripJournalResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	tripID, err := parseID(req.Msg.GetTripId(), "trip ID")
	if err != nil {
		return nil, err
	}
	j, err := h.svc.Journal(ctx, uid, tripID)
	if err != nil {
		return nil, h.toConnectErr(ctx, "get trip journal", err)
	}
	return connect.NewResponse(&journalv1.GetTripJournalResponse{Journal: j}), nil
}

func (h *Handler) SetDayDiary(ctx context.Context, req *connect.Request[journalv1.SetDayDiaryRequest]) (*connect.Response[journalv1.SetDayDiaryResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	dayID, err := parseID(req.Msg.GetDayId(), "day ID")
	if err != nil {
		return nil, err
	}
	body, err := h.svc.SetDiary(ctx, uid, dayID, req.Msg.GetText())
	if err != nil {
		return nil, h.toConnectErr(ctx, "set day diary", err)
	}
	return connect.NewResponse(&journalv1.SetDayDiaryResponse{DayId: dayID.String(), Diary: body}), nil
}

func (h *Handler) CreatePhotoUpload(ctx context.Context, req *connect.Request[journalv1.CreatePhotoUploadRequest]) (*connect.Response[journalv1.CreatePhotoUploadResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	tripID, err := parseID(req.Msg.GetTripId(), "trip ID")
	if err != nil {
		return nil, err
	}
	dayID, err := parseOptionalID(req.Msg.DayId, "day ID")
	if err != nil {
		return nil, err
	}
	stopID, err := parseOptionalID(req.Msg.StopId, "stop ID")
	if err != nil {
		return nil, err
	}
	up, err := h.svc.CreateUpload(ctx, uid, Upload{
		TripID: tripID, DayID: dayID, StopID: stopID,
		ContentType: req.Msg.GetContentType(), Size: req.Msg.GetSizeBytes(),
	})
	if err != nil {
		return nil, h.toConnectErr(ctx, "create photo upload", err)
	}
	return connect.NewResponse(&journalv1.CreatePhotoUploadResponse{
		PhotoId:   up.PhotoID.String(),
		UploadUrl: up.URL,
		Headers:   up.Headers,
		ExpiresAt: timestamppb.New(up.ExpiresAt),
	}), nil
}

func (h *Handler) ConfirmPhoto(ctx context.Context, req *connect.Request[journalv1.ConfirmPhotoRequest]) (*connect.Response[journalv1.ConfirmPhotoResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	photoID, err := parseID(req.Msg.GetPhotoId(), "photo ID")
	if err != nil {
		return nil, err
	}
	p, err := h.svc.Confirm(ctx, uid, photoID, req.Msg.GetCaption())
	if err != nil {
		return nil, h.toConnectErr(ctx, "confirm photo", err)
	}
	return connect.NewResponse(&journalv1.ConfirmPhotoResponse{Photo: p}), nil
}

func (h *Handler) DeletePhoto(ctx context.Context, req *connect.Request[journalv1.DeletePhotoRequest]) (*connect.Response[journalv1.DeletePhotoResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	photoID, err := parseID(req.Msg.GetPhotoId(), "photo ID")
	if err != nil {
		return nil, err
	}
	deleted, err := h.svc.DeletePhoto(ctx, uid, photoID)
	if err != nil {
		return nil, h.toConnectErr(ctx, "delete photo", err)
	}
	return connect.NewResponse(&journalv1.DeletePhotoResponse{Deleted: deleted}), nil
}
