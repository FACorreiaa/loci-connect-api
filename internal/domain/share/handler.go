package share

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	sharev1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/share"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/share/sharev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements the ShareService
type Handler struct {
	sharev1connect.UnimplementedShareServiceHandler
	baseURL   string
	shares    map[string]*shareEntry
	sharesMux sync.RWMutex
}

type shareEntry struct {
	Code        string
	ContentType sharev1.ShareContentType
	ContentID   string
	Title       string
	Description string
	ImageURL    string
	CreatedAt   time.Time
	ViewCount   int32
}

// NewHandler creates a new share handler
func NewHandler(baseURL string) *Handler {
	return &Handler{
		baseURL: baseURL,
		shares:  make(map[string]*shareEntry),
	}
}

// generateShareCode creates a random share code
func generateShareCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)[:10]
}

// CreateShareLink creates a shareable link for content
func (h *Handler) CreateShareLink(
	ctx context.Context,
	req *connect.Request[sharev1.CreateShareLinkRequest],
) (*connect.Response[sharev1.CreateShareLinkResponse], error) {
	msg := req.Msg

	code := generateShareCode()

	entry := &shareEntry{
		Code:        code,
		ContentType: msg.ContentType,
		ContentID:   msg.ContentId,
		Title:       msg.Title,
		Description: msg.Description,
		ImageURL:    msg.ImageUrl,
		CreatedAt:   time.Now(),
		ViewCount:   0,
	}

	h.sharesMux.Lock()
	h.shares[code] = entry
	h.sharesMux.Unlock()

	shareURL := fmt.Sprintf("%s/share/%s", h.baseURL, code)

	return connect.NewResponse(&sharev1.CreateShareLinkResponse{
		Success:   true,
		Message:   "Share link created",
		ShareCode: code,
		ShareUrl:  shareURL,
	}), nil
}

// GetShareMetadata returns metadata for a shared item
func (h *Handler) GetShareMetadata(
	ctx context.Context,
	req *connect.Request[sharev1.GetShareMetadataRequest],
) (*connect.Response[sharev1.GetShareMetadataResponse], error) {
	code := req.Msg.ShareCode

	h.sharesMux.RLock()
	entry, exists := h.shares[code]
	h.sharesMux.RUnlock()

	if !exists {
		return connect.NewResponse(&sharev1.GetShareMetadataResponse{
			Success: false,
			Message: "Share not found",
		}), nil
	}

	metadata := &sharev1.ShareMetadata{
		ShareCode:    entry.Code,
		ContentType:  entry.ContentType,
		Title:        entry.Title,
		Description:  entry.Description,
		ImageUrl:     entry.ImageURL,
		CanonicalUrl: fmt.Sprintf("%s/share/%s", h.baseURL, code),
		SiteName:     "Loci",
		CreatedAt:    timestamppb.New(entry.CreatedAt),
		ViewCount:    entry.ViewCount,
	}

	return connect.NewResponse(&sharev1.GetShareMetadataResponse{
		Success:  true,
		Message:  "Metadata retrieved",
		Metadata: metadata,
	}), nil
}

// GetSharedContent returns the full shared content
func (h *Handler) GetSharedContent(
	ctx context.Context,
	req *connect.Request[sharev1.GetSharedContentRequest],
) (*connect.Response[sharev1.GetSharedContentResponse], error) {
	code := req.Msg.ShareCode

	h.sharesMux.Lock()
	entry, exists := h.shares[code]
	if exists {
		entry.ViewCount++
	}
	h.sharesMux.Unlock()

	if !exists {
		return connect.NewResponse(&sharev1.GetSharedContentResponse{
			Success: false,
			Message: "Shared content not found",
		}), nil
	}

	metadata := &sharev1.ShareMetadata{
		ShareCode:    entry.Code,
		ContentType:  entry.ContentType,
		Title:        entry.Title,
		Description:  entry.Description,
		ImageUrl:     entry.ImageURL,
		CanonicalUrl: fmt.Sprintf("%s/share/%s", h.baseURL, code),
		SiteName:     "Loci",
		CreatedAt:    timestamppb.New(entry.CreatedAt),
		ViewCount:    entry.ViewCount,
	}

	content := &sharev1.SharedContent{
		Metadata: metadata,
	}

	// In a real implementation, you would fetch the actual content
	// from the appropriate repository based on content_type and content_id
	switch entry.ContentType {
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_POI:
		content.Poi = &sharev1.POIContent{
			Id:          entry.ContentID,
			Name:        entry.Title,
			Description: entry.Description,
		}
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_LIST:
		content.List = &sharev1.ListContent{
			Id:          entry.ContentID,
			Name:        entry.Title,
			Description: entry.Description,
		}
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_ITINERARY:
		content.Itinerary = &sharev1.ItineraryContent{
			Id:          entry.ContentID,
			Title:       entry.Title,
			Description: entry.Description,
		}
	}

	return connect.NewResponse(&sharev1.GetSharedContentResponse{
		Success: true,
		Message: "Content retrieved",
		Content: content,
	}), nil
}
