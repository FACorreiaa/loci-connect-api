package localcontext

import (
	"strings"
	"testing"
)

func TestBookingComDeepLink_SearchURL(t *testing.T) {
	b := BookingComDeepLink{AffiliateID: "test-aid"}
	u := b.SearchURL("Évora", "Portugal")
	if !strings.Contains(u, "booking.com") {
		t.Fatalf("expected booking.com URL, got %s", u)
	}
	if !strings.Contains(u, "aid=test-aid") {
		t.Fatalf("expected affiliate param, got %s", u)
	}
}

func TestUberDeepLink_RideURL(t *testing.T) {
	u := UberDeepLink{ClientID: "cid"}.RideURL(41.1, -8.6, 38.5, -7.9)
	if !strings.Contains(u, "uber.com") {
		t.Fatalf("expected uber URL, got %s", u)
	}
	if !strings.Contains(u, "client_id=cid") {
		t.Fatalf("expected client_id, got %s", u)
	}
}
