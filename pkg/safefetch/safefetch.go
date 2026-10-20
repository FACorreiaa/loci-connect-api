// Package safefetch fetches a page from a URL a user handed us, without letting
// that URL reach anything inside the cluster or the tailnet.
//
// The address check runs in the dialer, on the IP the connection is actually
// made to, so DNS rebinding and redirects into private space are both caught;
// the URL check runs again on every redirect hop.
package safefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrBadURL is a URL that is not a plain public http(s) link.
	ErrBadURL = errors.New("safefetch: not a public http(s) URL")
	// ErrBlocked is a URL whose host is, or resolves to, a private address.
	ErrBlocked = errors.New("safefetch: address not allowed")
	// ErrUnsupported is a response that is not text.
	ErrUnsupported = errors.New("safefetch: not a text page")
	// ErrStatus is a non-2xx response.
	ErrStatus = errors.New("safefetch: page answered with an error")
)

const (
	defaultMaxBytes = 2 << 20
	defaultTimeout  = 8 * time.Second
	maxRedirects    = 5
	userAgent       = "Mozilla/5.0 (compatible; LociBot/1.0; +https://lociai.fyi)"
)

// Options tune a Fetcher; zero values take the defaults (2 MB, 8 s).
type Options struct {
	MaxBytes int64
	Timeout  time.Duration
}

// Page is what a fetch returned. Body is cut at MaxBytes; Hops are every URL
// requested, the first one included, in order.
type Page struct {
	FinalURL    string
	ContentType string
	Body        []byte
	Hops        []string
}

// Fetcher is safe for concurrent use.
type Fetcher struct {
	opts    Options
	client  *http.Client
	blocked func(netip.Addr) bool
	portOK  func(string) bool
	nameOK  func(string) bool
}

// New builds a Fetcher.
func New(opts Options) *Fetcher {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	f := &Fetcher{opts: opts, blocked: blockedAddr, portOK: defaultPort, nameOK: publicName}
	f.client = f.newClient()
	return f
}

func defaults() *Fetcher { return New(Options{}) }

func (f *Fetcher) newClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		// Runs after DNS, on the address being connected to.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return ErrBlocked
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || f.blocked(ip) {
				return ErrBlocked
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: f.opts.Timeout,
		Transport: &http.Transport{
			// Never a proxy: the dial check must see the real destination.
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  f.opts.Timeout,
			MaxResponseHeaderBytes: 64 << 10,
			MaxIdleConns:           10,
			IdleConnTimeout:        30 * time.Second,
		},
	}
}

// Get fetches rawURL, following up to five redirects, each one checked. On
// an error after the first request, the Page still carries the hops.
func (f *Fetcher) Get(ctx context.Context, rawURL string) (Page, error) {
	return f.get(ctx, rawURL, nil)
}

// GetUntil is Get that stops at the first redirect hop stop accepts: that
// hop is not requested, and Page.FinalURL is it. It is for links whose
// answer is the redirect itself (a Maps short link), where the page behind
// it is large or refused.
func (f *Fetcher) GetUntil(ctx context.Context, rawURL string, stop func(*url.URL) bool) (Page, error) {
	return f.get(ctx, rawURL, stop)
}

func (f *Fetcher) get(ctx context.Context, rawURL string, stop func(*url.URL) bool) (Page, error) {
	u, err := f.checkURL(rawURL)
	if err != nil {
		return Page{}, err
	}
	hops := []string{u.String()}
	stopped := false
	client := *f.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("%w: too many redirects", ErrBadURL)
		}
		if _, err := f.checkURL(req.URL.String()); err != nil {
			return err
		}
		hops = append(hops, req.URL.String())
		if stop != nil && stop(req.URL) {
			stopped = true
			return http.ErrUseLastResponse
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrBadURL, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,text/plain;q=0.8")
	req.Header.Set("Accept-Language", "en;q=0.9,*;q=0.5")

	resp, err := client.Do(req)
	trail := Page{FinalURL: hops[len(hops)-1], Hops: hops}
	if err != nil {
		return trail, err
	}
	defer resp.Body.Close()
	if stopped {
		return trail, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return trail, fmt.Errorf("%w: %d", ErrStatus, resp.StatusCode)
	}
	ct := ""
	if raw := resp.Header.Get("Content-Type"); raw != "" {
		mt, _, err := mime.ParseMediaType(raw)
		if err != nil {
			return trail, ErrUnsupported
		}
		ct = mt
	}
	switch ct {
	case "", "text/html", "application/xhtml+xml", "application/json", "text/plain":
	default:
		return trail, fmt.Errorf("%w: %s", ErrUnsupported, ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.opts.MaxBytes))
	if err != nil {
		return trail, fmt.Errorf("read page: %w", err)
	}
	return Page{FinalURL: resp.Request.URL.String(), ContentType: ct, Body: body, Hops: hops}, nil
}

// checkURL accepts plain http(s) on the default ports to a public-looking
// host. Names are resolved later, by the dialer, which checks the address.
func (f *Fetcher) checkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrBadURL
	}
	if u.User != nil || u.Hostname() == "" {
		return nil, ErrBadURL
	}
	if !f.portOK(u.Port()) {
		return nil, ErrBadURL
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if f.blocked(ip) {
			return nil, ErrBlocked
		}
		return u, nil
	}
	if !f.nameOK(host) {
		return nil, ErrBlocked
	}
	return u, nil
}

func defaultPort(p string) bool { return p == "" || p == "80" || p == "443" }

// privateSuffixes are names that only mean something inside a network.
var privateSuffixes = []string{
	".localhost", ".local", ".internal", ".svc", ".cluster.local", ".ts.net", ".lan", ".home.arpa", ".intranet", ".corp",
}

func publicName(host string) bool {
	if host == "localhost" || !strings.Contains(host, ".") {
		return false
	}
	for _, s := range privateSuffixes {
		if strings.HasSuffix(host, s) {
			return false
		}
	}
	return true
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT, and the tailnet
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can map onto private v4
}

func blockedAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsUnspecified() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
