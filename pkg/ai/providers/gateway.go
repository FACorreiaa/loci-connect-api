package providers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ParseGatewayURL normalises a user-supplied Hermes base URL.
//
// Accepted: http(s) to a public host, a Tailscale MagicDNS name (*.ts.net),
// Tailscale CGNAT (100.64.0.0/10), or RFC1918 LAN. Rejected: loopback,
// link-local / cloud metadata, credentials in the URL, and anything that is
// not http(s). Path defaults to /v1.
func ParseGatewayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("gateway URL is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", fmt.Errorf("enter a full URL, such as http://hermes-box.ts.net:8642/v1")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("the gateway URL must be http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("put the key in the API key field, not in the URL")
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" || blockedHost(host) {
		return "", fmt.Errorf("that host is not a Hermes gateway Loci will call")
	}

	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/v1"
	}

	out := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: path}
	return out.String(), nil
}

// GatewayHTTPClient dials only addresses ParseGatewayURL would accept.
//
// The check runs again at connect time so a DNS name that pointed at a
// public host when saved cannot later be rebound onto loopback or metadata.
func GatewayHTTPClient(inner *http.Client) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range ips {
				if !allowedGatewayIP(ip.IP) {
					last = fmt.Errorf("refusing to dial %s", ip.IP)
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			if last == nil {
				last = fmt.Errorf("no addresses for %s", host)
			}
			return nil, last
		},
	}

	timeout := 5 * time.Minute
	if inner != nil && inner.Timeout > 0 {
		timeout = inner.Timeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func blockedHost(host string) bool {
	switch host {
	case "localhost", "localhost.localdomain", "metadata.google.internal":
		return true
	}
	if strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return !allowedGatewayIP(ip)
	}
	return false
}

func allowedGatewayIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	// Tailscale userspace / CGNAT — the usual self-hosted Hermes address.
	if tailscaleIP(ip) {
		return true
	}
	// LAN Hermes next to a self-hosted Loci.
	if ip.IsPrivate() {
		return true
	}
	return ip.IsGlobalUnicast()
}

func tailscaleIP(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	// 100.64.0.0/10
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}
