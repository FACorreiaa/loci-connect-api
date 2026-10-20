package safefetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlockedAddr(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.1", "100.127.255.254",
		"169.254.169.254", "0.0.0.0", "224.0.0.1", "255.255.255.255",
		"::1", "::", "fc00::1", "fd7a:115c:a1e0::1", "fe80::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
	} {
		require.True(t, blockedAddr(netip.MustParseAddr(s)), s)
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111", "100.128.0.1"} {
		require.False(t, blockedAddr(netip.MustParseAddr(s)), s)
	}
}

func TestCheckURL(t *testing.T) {
	for _, raw := range []string{
		"ftp://example.com/a", "file:///etc/passwd", "https://", "javascript:alert(1)",
		"https://localhost/", "https://loci-api.horus.svc.cluster.local/", "https://metadata.google.internal/",
		"https://lumina-green.tail1234.ts.net/", "https://example.com:8443/", "https://user:pw@example.com/",
		"https://127.0.0.1/", "https://[::1]/", "https://169.254.169.254/latest/meta-data/",
	} {
		_, err := defaults().checkURL(raw)
		require.Error(t, err, raw)
	}
	for _, raw := range []string{"https://www.tiktok.com/@a/video/1", "http://example.com/x", "https://example.com:443/"} {
		_, err := defaults().checkURL(raw)
		require.NoError(t, err, raw)
	}
}

// testFetcher lets the httptest loopback server through, and nothing else
// private.
func testFetcher(maxBytes int64) *Fetcher {
	f := New(Options{MaxBytes: maxBytes})
	f.blocked = func(a netip.Addr) bool { return !a.IsLoopback() && blockedAddr(a) }
	f.portOK = func(string) bool { return true }
	f.nameOK = func(string) bool { return true }
	f.client = f.newClient()
	return f
}

func TestGet_RefusesLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }))
	defer srv.Close()
	f := New(Options{})
	f.portOK = func(string) bool { return true }
	f.nameOK = func(string) bool { return true }
	f.client = f.newClient()
	_, err := f.Get(context.Background(), srv.URL)
	require.ErrorIs(t, err, ErrBlocked)
}

// The dial check runs on every hop, so a public page cannot bounce the
// fetch into the cluster.
func TestGet_RedirectToPrivateIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/admin", http.StatusFound)
	}))
	defer srv.Close()
	_, err := testFetcher(0).Get(context.Background(), srv.URL)
	require.ErrorIs(t, err, ErrBlocked)
}

func TestGet_TruncatesAtTheCapAndRecordsHops(t *testing.T) {
	big := strings.Repeat("a", 3000)
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/page", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(big))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	page, err := testFetcher(1000).Get(context.Background(), srv.URL+"/start")
	require.NoError(t, err)
	require.Len(t, page.Body, 1000)
	require.Equal(t, srv.URL+"/page", page.FinalURL)
	require.Equal(t, []string{srv.URL + "/start", srv.URL + "/page"}, page.Hops)
	require.Equal(t, "text/html", page.ContentType)
}

func TestGet_RefusesNonTextAndErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/img", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89})
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := testFetcher(0).Get(context.Background(), srv.URL+"/img")
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = testFetcher(0).Get(context.Background(), srv.URL+"/gone")
	require.ErrorIs(t, err, ErrStatus)
}
