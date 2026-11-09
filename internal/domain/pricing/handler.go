// Package pricing serves live partner prices for places Loci recommends.
package pricing

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	pricingv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/pricing/v1"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/pricing/v1/pricingv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/hotelrates"
)

const defaultCurrency = "EUR"

// Handler implements PricingService. A nil provider means pricing is off:
// every hotel comes back UNMATCHED, so clients render "no live price" rather
// than an error.
type Handler struct {
	pricingv1connect.UnimplementedPricingServiceHandler
	provider hotelrates.Provider
	logger   *slog.Logger
}

func NewHandler(provider hotelrates.Provider, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{provider: provider, logger: logger}
}

func (h *Handler) GetHotelRates(
	ctx context.Context,
	req *connect.Request[pricingv1.GetHotelRatesRequest],
) (*connect.Response[pricingv1.GetHotelRatesResponse], error) {
	msg := req.Msg
	checkin, err1 := time.Parse(time.DateOnly, msg.GetCheckin())
	checkout, err2 := time.Parse(time.DateOnly, msg.GetCheckout())
	if err1 != nil || err2 != nil || !checkout.After(checkin) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("checkin and checkout must be dates with checkout after checkin"))
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if checkin.Before(today.AddDate(0, 0, -1)) {
		// One day of slack for clients ahead of UTC.
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("checkin is in the past"))
	}

	stay := hotelrates.Stay{
		Checkin:  checkin,
		Checkout: checkout,
		Adults:   int(msg.GetAdults()),
		Currency: strings.ToUpper(msg.GetCurrency()),
	}
	if stay.Currency == "" {
		stay.Currency = defaultCurrency
	}
	if stay.Adults < 1 {
		stay.Adults = 2
	}

	queries := make([]hotelrates.Query, len(msg.GetHotels()))
	for i, q := range msg.GetHotels() {
		queries[i] = hotelrates.Query{
			Key:         q.GetPoiId(),
			Name:        q.GetName(),
			Lat:         q.GetLatitude(),
			Lon:         q.GetLongitude(),
			City:        q.GetCity(),
			CountryCode: q.GetCountryCode(),
		}
	}

	rates := hotelrates.Unmatched(queries)
	if h.provider != nil {
		got, err := h.provider.Rates(ctx, queries, stay)
		if err != nil {
			h.logger.WarnContext(ctx, "hotel rates unavailable", slog.Any("error", err))
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("live hotel prices are unavailable right now"))
		}
		rates = got
	}

	out := &pricingv1.GetHotelRatesResponse{
		Rates:  make([]*pricingv1.HotelRate, len(rates)),
		Nights: int32(stay.Nights()),
	}
	for i, r := range rates {
		pr := &pricingv1.HotelRate{
			PoiId:              r.Key,
			Status:             toProtoStatus(r.Status),
			NightlyAmountMinor: r.NightlyMinor,
			TotalAmountMinor:   r.TotalMinor,
			Currency:           r.Currency,
			Provider:           r.Provider,
			BookingUrl:         r.BookingURL,
		}
		if !r.FetchedAt.IsZero() {
			pr.FetchedAt = timestamppb.New(r.FetchedAt)
		}
		out.Rates[i] = pr
	}
	return connect.NewResponse(out), nil
}

func toProtoStatus(s hotelrates.Status) pricingv1.HotelRateStatus {
	switch s {
	case hotelrates.StatusPriced:
		return pricingv1.HotelRateStatus_HOTEL_RATE_STATUS_PRICED
	case hotelrates.StatusSoldOut:
		return pricingv1.HotelRateStatus_HOTEL_RATE_STATUS_SOLD_OUT
	default:
		return pricingv1.HotelRateStatus_HOTEL_RATE_STATUS_UNMATCHED
	}
}
