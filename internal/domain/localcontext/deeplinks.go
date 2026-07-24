package localcontext

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// BookingComDeepLink builds Booking.com search URLs (optionally with affiliate id).
type BookingComDeepLink struct {
	AffiliateID string
}

func NewBookingComDeepLinkFromEnv() BookingComDeepLink {
	return BookingComDeepLink{AffiliateID: strings.TrimSpace(os.Getenv("BOOKING_AFFILIATE_ID"))}
}

func (b BookingComDeepLink) SearchURL(city, country string) string {
	q := strings.TrimSpace(city)
	if country != "" {
		q = fmt.Sprintf("%s, %s", q, country)
	}
	v := url.Values{}
	v.Set("ss", q)
	if b.AffiliateID != "" {
		v.Set("aid", b.AffiliateID)
	}
	return "https://www.booking.com/searchresults.html?" + v.Encode()
}

func (b BookingComDeepLink) Options(_ context.Context, _ string) ([]BookingOption, error) {
	return nil, nil
}

// OpenTableDeepLink builds TheFork/OpenTable-style restaurant search URLs.
type OpenTableDeepLink struct {
	AffiliateID string
}

func NewOpenTableDeepLinkFromEnv() OpenTableDeepLink {
	return OpenTableDeepLink{AffiliateID: strings.TrimSpace(os.Getenv("RESERVE_AFFILIATE_ID"))}
}

func (o OpenTableDeepLink) RestaurantURL(city string) string {
	q := url.QueryEscape(strings.TrimSpace(city))
	base := fmt.Sprintf("https://www.thefork.com/search?city=%s", q)
	if o.AffiliateID != "" {
		return base + "&partner=" + url.QueryEscape(o.AffiliateID)
	}
	return base
}

// UberDeepLink builds Uber ride deeplinks between two coordinates.
type UberDeepLink struct {
	ClientID string
}

func NewUberDeepLinkFromEnv() UberDeepLink {
	return UberDeepLink{ClientID: strings.TrimSpace(os.Getenv("UBER_CLIENT_ID"))}
}

func (u UberDeepLink) RideURL(fromLat, fromLon, toLat, toLon float64) string {
	v := url.Values{}
	v.Set("action", "setPickup")
	v.Set("pickup[latitude]", fmt.Sprintf("%.6f", fromLat))
	v.Set("pickup[longitude]", fmt.Sprintf("%.6f", fromLon))
	v.Set("dropoff[latitude]", fmt.Sprintf("%.6f", toLat))
	v.Set("dropoff[longitude]", fmt.Sprintf("%.6f", toLon))
	if u.ClientID != "" {
		v.Set("client_id", u.ClientID)
	}
	return "https://m.uber.com/looking?" + v.Encode()
}

// StubTransportWithDrive adds a driving estimate alongside the walking stub.
type StubTransportWithDrive struct {
	Fallback TransportAdapter
	Uber     UberDeepLink
}

func (s StubTransportWithDrive) Options(ctx context.Context, fromLat, fromLon, toLat, toLon float64) ([]TransportOption, error) {
	km := haversineKm(fromLat, fromLon, toLat, toLon)
	driveMins := int(km/80.0*60.0) + 1
	out := []TransportOption{{
		Mode:         "drive",
		Summary:      fmt.Sprintf("~%.0f km by car", km),
		DurationMins: driveMins,
	}}
	if s.Fallback != nil {
		if extra, err := s.Fallback.Options(ctx, fromLat, fromLon, toLat, toLon); err == nil {
			out = append(out, extra...)
		}
	}
	return out, nil
}

func (s StubTransportWithDrive) RideURL(fromLat, fromLon, toLat, toLon float64) string {
	return s.Uber.RideURL(fromLat, fromLon, toLat, toLon)
}
