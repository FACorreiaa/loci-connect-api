package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/cors"
)

// preflight sends the browser's canonical preflight: header names lowercase
// and sorted, which is what rs/cors's SortedSet requires to match.
func preflight(t *testing.T, origin, requestHeaders string) *httptest.ResponseRecorder {
	t.Helper()
	h := cors.New(corsOptions([]string{"https://lociai.fyi", "https://www.lociai.fyi"})).
		Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	req := httptest.NewRequest(http.MethodOptions, "/loci.auth.AuthService/ValidateSession", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", requestHeaders)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The sign-in preflight from lociai.fyi once PostHog is initialised: the
// Connect headers plus the three posthog-js adds under tracing_headers. This
// is the request production rejected with a bare 204.
func TestPreflightAllowsPostHogTracingHeaders(t *testing.T) {
	rec := preflight(t, "https://lociai.fyi",
		"connect-protocol-version,content-type,x-posthog-distinct-id,x-posthog-session-id,x-posthog-window-id")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://lociai.fyi" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the requesting origin; headers: %v", got, rec.Header())
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatalf("no Access-Control-Allow-Headers on an allowed preflight; headers: %v", rec.Header())
	}
}

func TestPreflightAllowsConnectAndAuthorization(t *testing.T) {
	rec := preflight(t, "https://www.lociai.fyi",
		"authorization,connect-protocol-version,connect-timeout-ms,content-type")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://www.lociai.fyi" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestPreflightRejectsUnknownOrigin(t *testing.T) {
	rec := preflight(t, "https://evil.example", "content-type")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unknown origin was allowed: %q", got)
	}
}

func TestPreflightRejectsUnlistedHeader(t *testing.T) {
	rec := preflight(t, "https://lociai.fyi", "content-type,x-not-ours")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unlisted header was allowed: %q", got)
	}
}

func TestCorsOptionsDefaultsToLocalDev(t *testing.T) {
	opts := corsOptions(nil)
	if len(opts.AllowedOrigins) != 1 || opts.AllowedOrigins[0] != "http://localhost:3000" {
		t.Fatalf("empty origins should default to local dev, got %v", opts.AllowedOrigins)
	}
}
