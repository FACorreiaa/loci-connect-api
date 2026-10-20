package placeintel

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/linkimport"
)

// linkImporter reads a shared link into places (linkimport.Service).
type linkImporter interface {
	Import(ctx context.Context, rawURL, cityHint string) (*linkimport.Result, error)
}

// WithLinkImport turns on ImportPlaces. Without it the RPC is Unimplemented.
func (h *Handler) WithLinkImport(imp linkImporter) *Handler {
	h.links = imp
	return h
}

// ImportPlaces reads a link and returns the places it mentions. Nothing is
// saved: the client adds the ones the traveller picks to a list.
func (h *Handler) ImportPlaces(ctx context.Context, req *connect.Request[placev1.ImportPlacesRequest]) (*connect.Response[placev1.ImportPlacesResponse], error) {
	if _, err := userID(ctx); err != nil {
		return nil, err
	}
	if h.links == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("importing links is not available"))
	}
	res, err := h.links.Import(ctx, req.Msg.GetUrl(), req.Msg.GetCityHint())
	if err != nil {
		return nil, h.importError(ctx, err)
	}
	out := &placev1.ImportPlacesResponse{
		Source:      importSource(res.Source),
		SourceTitle: res.Title,
		CityName:    res.City,
		Places:      make([]*placev1.ImportedPlace, 0, len(res.Places)),
	}
	for _, p := range res.Places {
		ip := &placev1.ImportedPlace{Name: p.Name, CityName: p.City, Note: p.Note}
		if p.Place != nil {
			ip.PoiId = &p.Place.ID
			ip.PoiName = &p.Place.Name
			ip.Latitude = &p.Place.Latitude
			ip.Longitude = &p.Place.Longitude
			if p.Place.Address != "" {
				ip.Address = &p.Place.Address
			}
			if p.Place.Category != "" {
				ip.Category = &p.Place.Category
			}
		}
		out.Places = append(out.Places, ip)
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) importError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, linkimport.ErrBadURL):
		return connect.NewError(connect.CodeInvalidArgument, linkimport.ErrBadURL)
	case errors.Is(err, linkimport.ErrNoPlaces):
		return connect.NewError(connect.CodeFailedPrecondition, linkimport.ErrNoPlaces)
	case errors.Is(err, linkimport.ErrUnreadable):
		return connect.NewError(connect.CodeFailedPrecondition, linkimport.ErrUnreadable)
	case errors.Is(err, linkimport.ErrModel):
		return connect.NewError(connect.CodeUnavailable, linkimport.ErrModel)
	}
	h.logger.ErrorContext(ctx, "import places", slog.Any("error", err))
	return connect.NewError(connect.CodeInternal, errors.New("couldn't import that link"))
}

func importSource(s linkimport.Source) placev1.ImportSource {
	switch s {
	case linkimport.SourceGoogleMaps:
		return placev1.ImportSource_IMPORT_SOURCE_GOOGLE_MAPS
	case linkimport.SourceTikTok:
		return placev1.ImportSource_IMPORT_SOURCE_TIKTOK
	case linkimport.SourceInstagram:
		return placev1.ImportSource_IMPORT_SOURCE_INSTAGRAM
	case linkimport.SourceYouTube:
		return placev1.ImportSource_IMPORT_SOURCE_YOUTUBE
	case linkimport.SourceWeb:
		return placev1.ImportSource_IMPORT_SOURCE_WEB
	}
	return placev1.ImportSource_IMPORT_SOURCE_UNSPECIFIED
}
