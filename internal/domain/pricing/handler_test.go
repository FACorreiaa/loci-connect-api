package pricing

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	pricingv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/pricing/v1"

	"github.com/FACorreiaa/loci-connect-api/pkg/hotelrates"
)

type stubProvider struct {
	gotStay hotelrates.Stay
	rates   []hotelrates.Rate
	err     error
}

func (s *stubProvider) Rates(_ context.Context, q []hotelrates.Query, stay hotelrates.Stay) ([]hotelrates.Rate, error) {
	s.gotStay = stay
	if s.err != nil {
		return nil, s.err
	}
	return s.rates, nil
}

func request(checkin, checkout string) *connect.Request[pricingv1.GetHotelRatesRequest] {
	return connect.NewRequest(&pricingv1.GetHotelRatesRequest{
		Hotels:   []*pricingv1.HotelRateQuery{{PoiId: "p1", Name: "Casa Fuster"}, {PoiId: "p2", Name: "Hotel Arts"}},
		Checkin:  checkin,
		Checkout: checkout,
		Adults:   2,
	})
}

func future(days int) string { return time.Now().UTC().AddDate(0, 0, days).Format(time.DateOnly) }

func TestGetHotelRatesOffReturnsUnmatched(t *testing.T) {
	h := NewHandler(nil, nil)
	resp, err := h.GetHotelRates(context.Background(), request(future(3), future(5)))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.GetNights() != 2 || len(resp.Msg.GetRates()) != 2 {
		t.Fatalf("resp %+v", resp.Msg)
	}
	for _, r := range resp.Msg.GetRates() {
		if r.GetStatus() != pricingv1.HotelRateStatus_HOTEL_RATE_STATUS_UNMATCHED {
			t.Errorf("rate %+v", r)
		}
	}
	if resp.Msg.GetRates()[1].GetPoiId() != "p2" {
		t.Error("order")
	}
}

func TestGetHotelRatesMapsProviderRates(t *testing.T) {
	now := time.Now()
	p := &stubProvider{rates: []hotelrates.Rate{
		{Key: "p1", Status: hotelrates.StatusPriced, NightlyMinor: 15000, TotalMinor: 30000, Currency: "EUR", Provider: "liteapi", BookingURL: "https://b/x", FetchedAt: now},
		{Key: "p2", Status: hotelrates.StatusSoldOut, Provider: "liteapi", FetchedAt: now},
	}}
	resp, err := NewHandler(p, nil).GetHotelRates(context.Background(), request(future(3), future(5)))
	if err != nil {
		t.Fatal(err)
	}
	if p.gotStay.Currency != "EUR" || p.gotStay.Adults != 2 {
		t.Errorf("stay %+v", p.gotStay)
	}
	r := resp.Msg.GetRates()
	if r[0].GetStatus() != pricingv1.HotelRateStatus_HOTEL_RATE_STATUS_PRICED || r[0].GetNightlyAmountMinor() != 15000 ||
		r[0].GetBookingUrl() != "https://b/x" || r[0].GetFetchedAt() == nil {
		t.Errorf("r0 %+v", r[0])
	}
	if r[1].GetStatus() != pricingv1.HotelRateStatus_HOTEL_RATE_STATUS_SOLD_OUT {
		t.Errorf("r1 %+v", r[1])
	}
}

func TestGetHotelRatesProviderDownIsUnavailable(t *testing.T) {
	p := &stubProvider{err: errors.Join(hotelrates.ErrUnavailable, errors.New("503"))}
	_, err := NewHandler(p, nil).GetHotelRates(context.Background(), request(future(3), future(5)))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("err %v", err)
	}
}

func TestGetHotelRatesRejectsBadDates(t *testing.T) {
	h := NewHandler(nil, nil)
	for _, c := range [][2]string{{future(5), future(3)}, {future(-10), future(-8)}, {"x", future(2)}} {
		if _, err := h.GetHotelRates(context.Background(), request(c[0], c[1])); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%v: err %v", c, err)
		}
	}
}
