package share

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	sharev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/share"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/share/sharev1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements the ShareService, backed by a persistent share repository
// so links survive restarts.
type Handler struct {
	sharev1connect.UnimplementedShareServiceHandler
	baseURL string
	repo    Repository
}

// NewHandler creates a new share handler.
func NewHandler(baseURL string, repo Repository) *Handler {
	return &Handler{baseURL: baseURL, repo: repo}
}

// generateShareCode creates a random share code.
func generateShareCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)[:10]
}

func (h *Handler) shareURL(code string) string {
	return fmt.Sprintf("%s/share/%s", h.baseURL, code)
}

func (h *Handler) toMetadata(s *Share) *sharev1.ShareMetadata {
	return &sharev1.ShareMetadata{
		ShareCode:    s.Code,
		ContentType:  sharev1.ShareContentType(s.ContentType),
		Title:        s.Title,
		Description:  s.Description,
		ImageUrl:     s.ImageURL,
		CanonicalUrl: h.shareURL(s.Code),
		SiteName:     "Loci",
		CreatedAt:    timestamppb.New(s.CreatedAt),
		ViewCount:    s.ViewCount,
	}
}

// CreateShareLink creates and persists a shareable link for content.
func (h *Handler) CreateShareLink(
	ctx context.Context,
	req *connect.Request[sharev1.CreateShareLinkRequest],
) (*connect.Response[sharev1.CreateShareLinkResponse], error) {
	msg := req.Msg
	code := generateShareCode()

	s := &Share{
		Code:        code,
		ContentType: int32(msg.ContentType),
		ContentID:   msg.ContentId,
		Title:       msg.Title,
		Description: msg.Description,
		ImageURL:    msg.ImageUrl,
	}
	// Attribute the share to the authenticated user when available.
	if idStr, ok := interceptors.GetUserIDFromContext(ctx); ok {
		if uid, err := uuid.Parse(idStr); err == nil {
			s.CreatedBy = &uid
		}
	}

	if err := h.repo.Create(ctx, s); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&sharev1.CreateShareLinkResponse{
		Success:   true,
		Message:   "Share link created",
		ShareCode: code,
		ShareUrl:  h.shareURL(code),
	}), nil
}

// GetShareMetadata returns metadata for a shared item.
func (h *Handler) GetShareMetadata(
	ctx context.Context,
	req *connect.Request[sharev1.GetShareMetadataRequest],
) (*connect.Response[sharev1.GetShareMetadataResponse], error) {
	s, err := h.repo.GetByCode(ctx, req.Msg.ShareCode)
	if errors.Is(err, ErrNotFound) {
		return connect.NewResponse(&sharev1.GetShareMetadataResponse{Success: false, Message: "Share not found"}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&sharev1.GetShareMetadataResponse{
		Success:  true,
		Message:  "Metadata retrieved",
		Metadata: h.toMetadata(s),
	}), nil
}

// GetSharedContent returns the shared content and increments the view count.
func (h *Handler) GetSharedContent(
	ctx context.Context,
	req *connect.Request[sharev1.GetSharedContentRequest],
) (*connect.Response[sharev1.GetSharedContentResponse], error) {
	s, err := h.repo.IncrementView(ctx, req.Msg.ShareCode)
	if errors.Is(err, ErrNotFound) {
		return connect.NewResponse(&sharev1.GetSharedContentResponse{Success: false, Message: "Shared content not found"}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	content := &sharev1.SharedContent{Metadata: h.toMetadata(s)}
	switch sharev1.ShareContentType(s.ContentType) {
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_POI:
		content.Poi = &sharev1.POIContent{Id: s.ContentID, Name: s.Title, Description: s.Description}
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_LIST:
		content.List = &sharev1.ListContent{Id: s.ContentID, Name: s.Title, Description: s.Description}
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_ITINERARY:
		content.Itinerary = &sharev1.ItineraryContent{Id: s.ContentID, Title: s.Title, Description: s.Description}
	}

	return connect.NewResponse(&sharev1.GetSharedContentResponse{
		Success: true,
		Message: "Content retrieved",
		Content: content,
	}), nil
}
