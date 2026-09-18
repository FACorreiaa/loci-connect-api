package bundle

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	bundlev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/bundle/v1"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/bundle/v1/bundlev1connect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	trippb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements BundleService.
//
// ListBundles and GetBundle are registered as optional-auth procedures: they
// answer an anonymous caller, and when a token happens to be present they use
// it to say whether this person already owns the pack. That is why identity is
// read with callerID rather than the usual authenticated-or-reject preamble.
type Handler struct {
	bundlev1connect.UnimplementedBundleServiceHandler
	svc    *Service
	logger *slog.Logger
}

// NewHandler builds the bundle handler.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// callerID returns the signed-in user, or nil when the call is anonymous.
func callerID(ctx context.Context) *uuid.UUID {
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

// requireCaller returns the signed-in user or an Unauthenticated error.
func requireCaller(ctx context.Context) (uuid.UUID, error) {
	if id := callerID(ctx); id != nil {
		return *id, nil
	}
	return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
}

// toConnectErr maps domain errors onto Connect codes.
//
// ErrNotOwned is PermissionDenied carrying x-loci-bundle, deliberately not the
// x-loci-entitlement header the client uses to open the Pro upgrade modal. A
// pack is a separate purchase, so offering a subscription here would be the
// wrong answer to "you have not bought this".
func toConnectErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("bundle not found"))
	case errors.Is(err, ErrAlreadyOwned):
		e := connect.NewError(connect.CodeAlreadyExists, errors.New("you already own this pack"))
		e.Meta().Set("x-loci-bundle", "already-owned")
		return e
	case errors.Is(err, ErrNotOwned):
		e := connect.NewError(connect.CodePermissionDenied, errors.New("this pack has not been purchased"))
		e.Meta().Set("x-loci-bundle", "not-owned")
		return e
	case errors.Is(err, ErrNotPurchasable):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("this pack is not for sale"))
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (h *Handler) toBundlePB(b *Bundle, owned bool) *bundlev1.Bundle {
	months := make([]int32, 0, len(b.Months))
	for _, m := range b.Months {
		months = append(months, int32(m))
	}
	out := &bundlev1.Bundle{
		Id:          b.ID.String(),
		Slug:        b.Slug,
		Title:       b.Title,
		Summary:     b.Summary,
		CityName:    b.CityName,
		CountryCode: b.CountryCode,
		Theme:       b.Theme,
		Months:      months,
		DayCount:    int32(b.DayCount),
		StopCount:   int32(b.StopCount),
		IsPaid:      b.IsPaid,
		PriceCents:  int32(h.svc.PriceCents()),
		Currency:    h.svc.Currency(),
		Owned:       owned,
	}
	if b.CityID != nil {
		s := b.CityID.String()
		out.CityId = &s
	}
	if b.CoverImageURL != nil {
		out.CoverImageUrl = b.CoverImageURL
	}
	if b.PublishedAt != nil {
		out.PublishedAt = timestamppb.New(*b.PublishedAt)
	}
	// A free pack is owned by everyone; saying so keeps the client's single
	// "can I open this" check honest.
	if !b.IsPaid {
		out.Owned = true
	}
	return out
}

func toDayPB(d Day) *bundlev1.BundleDay {
	stops := make([]*trippb.TripStop, 0, len(d.Stops))
	for _, s := range d.Stops {
		ts := &trippb.TripStop{
			Id:         s.ID.String(),
			OrderIndex: int32(s.OrderIndex),
			Name:       s.Name,
			Notes:      s.Notes,
		}
		if s.POIID != nil {
			ts.PoiId = s.POIID.String()
		}
		if s.BookingURL != nil {
			ts.BookingUrl = s.BookingURL
		}
		if s.StartMinute != nil {
			v := int32(*s.StartMinute)
			ts.StartMinute = &v
		}
		if s.DurationMinutes != nil {
			v := int32(*s.DurationMinutes)
			ts.DurationMinutes = &v
		}
		stops = append(stops, ts)
	}
	return &bundlev1.BundleDay{
		DayNumber: int32(d.DayNumber),
		Title:     d.Title,
		Stops:     stops,
	}
}

func paginationPB(total, page, pageSize int) *commonpb.PaginationMetadata {
	if pageSize <= 0 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}
	totalPages := (total + pageSize - 1) / pageSize
	return &commonpb.PaginationMetadata{
		TotalRecords: int32(total),
		Page:         int32(page),
		PageSize:     int32(pageSize),
		TotalPages:   int32(totalPages),
		HasMore:      page < totalPages,
	}
}

// ListBundles serves the public catalog.
func (h *Handler) ListBundles(
	ctx context.Context,
	req *connect.Request[bundlev1.ListBundlesRequest],
) (*connect.Response[bundlev1.ListBundlesResponse], error) {
	msg := req.Msg
	f := ListFilter{Page: 1, PageSize: 20}
	if msg.Pagination != nil {
		f.Page = int(msg.Pagination.Page)
		f.PageSize = int(msg.Pagination.PageSize)
	}
	if msg.CityName != nil {
		f.CityName = *msg.CityName
	}
	if msg.Theme != nil {
		f.Theme = *msg.Theme
	}
	if msg.Month != nil {
		f.Month = int(*msg.Month)
	}
	if msg.OnlyFree != nil {
		f.OnlyFree = *msg.OnlyFree
	}

	bundles, owned, total, err := h.svc.List(ctx, f, callerID(ctx))
	if err != nil {
		return nil, toConnectErr(err)
	}

	out := make([]*bundlev1.Bundle, 0, len(bundles))
	for i := range bundles {
		out = append(out, h.toBundlePB(&bundles[i], owned[bundles[i].ID]))
	}

	return connect.NewResponse(&bundlev1.ListBundlesResponse{
		Bundles:    out,
		Pagination: paginationPB(total, f.Page, f.PageSize),
	}), nil
}

// GetBundle serves one pack, with only the days the caller may read.
func (h *Handler) GetBundle(
	ctx context.Context,
	req *connect.Request[bundlev1.GetBundleRequest],
) (*connect.Response[bundlev1.BundleDetail], error) {
	detail, err := h.svc.GetDetail(ctx, req.Msg.Slug, callerID(ctx))
	if err != nil {
		return nil, toConnectErr(err)
	}

	days := make([]*bundlev1.BundleDay, 0, len(detail.Days))
	for _, d := range detail.Days {
		days = append(days, toDayPB(d))
	}

	return connect.NewResponse(&bundlev1.BundleDetail{
		Bundle:         h.toBundlePB(detail.Bundle, detail.Owned),
		Days:           days,
		LockedDayCount: int32(detail.LockedDayCount),
	}), nil
}

// CreateBundleCheckout opens a one-time Stripe Checkout session.
func (h *Handler) CreateBundleCheckout(
	ctx context.Context,
	req *connect.Request[bundlev1.CreateBundleCheckoutRequest],
) (*connect.Response[bundlev1.CreateBundleCheckoutResponse], error) {
	userID, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	bundleID, err := uuid.Parse(req.Msg.BundleId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid bundle id"))
	}

	sessionID, url, err := h.svc.StartCheckout(ctx, userID, bundleID, req.Msg.SuccessUrl, req.Msg.CancelUrl)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&bundlev1.CreateBundleCheckoutResponse{
		SessionId: sessionID,
		Url:       url,
	}), nil
}

// ClaimBundle copies a pack into the caller's own trips.
func (h *Handler) ClaimBundle(
	ctx context.Context,
	req *connect.Request[bundlev1.ClaimBundleRequest],
) (*connect.Response[bundlev1.ClaimBundleResponse], error) {
	userID, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	bundleID, err := uuid.Parse(req.Msg.BundleId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid bundle id"))
	}

	tripID, err := h.svc.Claim(ctx, userID, bundleID)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&bundlev1.ClaimBundleResponse{TripId: tripID.String()}), nil
}

// ListMyBundles returns the packs the caller has bought.
func (h *Handler) ListMyBundles(
	ctx context.Context,
	req *connect.Request[bundlev1.ListMyBundlesRequest],
) (*connect.Response[bundlev1.ListMyBundlesResponse], error) {
	userID, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}

	page, pageSize := 1, 20
	if req.Msg.Pagination != nil {
		page, pageSize = int(req.Msg.Pagination.Page), int(req.Msg.Pagination.PageSize)
	}

	bundles, total, err := h.svc.ListMine(ctx, userID, page, pageSize)
	if err != nil {
		return nil, toConnectErr(err)
	}

	out := make([]*bundlev1.Bundle, 0, len(bundles))
	for i := range bundles {
		out = append(out, h.toBundlePB(&bundles[i], true))
	}
	return connect.NewResponse(&bundlev1.ListMyBundlesResponse{
		Bundles:    out,
		Pagination: paginationPB(total, page, pageSize),
	}), nil
}
