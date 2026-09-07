package providers

import (
	"net"
	"testing"
)

func TestParseGatewayURLAcceptsTailnetAndNormalises(t *testing.T) {
	got, err := ParseGatewayURL("http://hermes-vps-2.tail562587.ts.net:8642")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://hermes-vps-2.tail562587.ts.net:8642/v1" {
		t.Fatalf("got %q", got)
	}
}

func TestParseGatewayURLRejectsLoopbackAndMetadata(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"",
		"not a url",
		"ftp://hermes.example.com/v1",
		"http://user:pass@hermes.example.com/v1",
		"http://127.0.0.1:8642/v1",
		"http://localhost:8642/v1",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]:8642/v1",
	} {
		if _, err := ParseGatewayURL(raw); err == nil {
			t.Errorf("%q: accepted, want reject", raw)
		}
	}
}

func TestAllowedGatewayIP(t *testing.T) {
	t.Parallel()
	allow := []string{"100.105.117.67", "1.1.1.1", "192.168.1.10", "10.0.0.5"}
	deny := []string{"127.0.0.1", "0.0.0.0", "169.254.169.254", "::1"}
	for _, s := range allow {
		if !allowedGatewayIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	for _, s := range deny {
		if allowedGatewayIP(net.ParseIP(s)) {
			t.Errorf("%s should be rejected", s)
		}
	}
}
