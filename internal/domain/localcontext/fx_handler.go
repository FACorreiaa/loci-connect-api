package localcontext

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WithFX attaches the exchange-rate source and the fuel assumptions used to
// price a driving leg.
//
// Optional, like WithScoring and WithSignals: without it GetFxRates reports the
// feature as unconfigured rather than failing, and EstimateDriveCost still
// answers from the built-in defaults, since the arithmetic needs no provider.
func (h *Handler) WithFX(
	fx *FXAdapter,
	base string,
	litresPer100Km, pricePerLitre float64,
	country CountryResolver,
) *Handler {
	h.fx = fx
	h.fxBase = normaliseCurrency(base)
	h.litresPer100Km = litresPer100Km
	h.pricePerLitre = pricePerLitre
	h.fxCountry = country
	return h
}

// GetFxRates answers "what is my money worth there?".
func (h *Handler) GetFxRates(
	ctx context.Context,
	req *connect.Request[lcv1.GetFxRatesRequest],
) (*connect.Response[lcv1.GetFxRatesResponse], error) {
	if h.fx == nil {
		// Unavailable rather than broken. Same convention as the rest of this
		// handler: an unconfigured extra must not look like a server fault.
		return connect.NewResponse(&lcv1.GetFxRatesResponse{}), nil
	}

	base := normaliseCurrency(req.Msg.GetBase())
	if base == "" {
		base = h.fxBase
	}

	quotes := req.Msg.GetQuotes()

	// A caller holding a destination should not have to know its currency, so
	// a country code — or just coordinates — is accepted as a stand-in.
	countryCode := req.Msg.GetCountryCode()
	if len(quotes) == 0 && countryCode == "" &&
		req.Msg.Latitude != nil && req.Msg.Longitude != nil && h.fxCountry != nil {
		resolved, err := h.fxCountry.CountryCode(ctx, req.Msg.GetLatitude(), req.Msg.GetLongitude())
		if err != nil {
			h.logger.WarnContext(ctx, "fx: country lookup failed; no rate to offer",
				slog.Any("error", err))
			return connect.NewResponse(&lcv1.GetFxRatesResponse{}), nil
		}
		countryCode = resolved
	}

	if len(quotes) == 0 && countryCode != "" {
		if currency := CurrencyForCountry(countryCode); currency != "" {
			quotes = []string{currency}
		} else {
			// We know the country and cannot price it. Saying so beats an
			// empty response the client cannot distinguish from a failure.
			return connect.NewResponse(&lcv1.GetFxRatesResponse{
				Unsupported: []string{countryCode},
			}), nil
		}
	}
	if len(quotes) == 0 {
		return connect.NewResponse(&lcv1.GetFxRatesResponse{}), nil
	}

	rates, unsupported, err := h.fx.Rates(ctx, base, quotes)
	if err != nil {
		// Degrade rather than fail, as everywhere else here: a missing rate is
		// a missing nicety, not a reason to break a trip view.
		h.logger.WarnContext(ctx, "fx: rates unavailable; returning empty",
			slog.String("base", base), slog.Any("error", err))
		return connect.NewResponse(&lcv1.GetFxRatesResponse{Unsupported: unsupported}), nil
	}

	out := &lcv1.GetFxRatesResponse{Unsupported: unsupported}
	for _, r := range rates {
		pr := &lcv1.FxRate{Base: r.Base, Quote: r.Quote, Rate: r.Rate}
		if !r.AsOf.IsZero() {
			pr.AsOf = timestamppb.New(r.AsOf)
		}
		out.Rates = append(out.Rates, pr)
	}
	return connect.NewResponse(out), nil
}

// EstimateDriveCost prices the fuel for a driving leg.
func (h *Handler) EstimateDriveCost(
	ctx context.Context,
	req *connect.Request[lcv1.EstimateDriveCostRequest],
) (*connect.Response[lcv1.EstimateDriveCostResponse], error) {
	_ = ctx

	currency := normaliseCurrency(req.Msg.GetCurrency())
	if currency == "" {
		currency = h.fxBase
	}

	est := EstimateDriveCost(DriveCostInput{
		DistanceKm:     req.Msg.GetDistanceKm(),
		LitresPer100Km: h.litresPer100Km,
		PricePerLitre:  h.pricePerLitre,
		Currency:       currency,
	})

	return connect.NewResponse(&lcv1.EstimateDriveCostResponse{
		Estimate: &lcv1.DriveCostEstimate{
			DistanceKm:  est.DistanceKm,
			Litres:      est.Litres,
			Cost:        est.Cost,
			Currency:    est.Currency,
			Assumptions: est.Assumptions,
		},
	}), nil
}
