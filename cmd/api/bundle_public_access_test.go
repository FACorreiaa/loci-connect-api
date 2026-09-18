package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	bundlev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/bundle/v1"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/bundle/v1/bundlev1connect"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// The City Packs catalog is the one surface meant to be read by somebody who
// has never signed in. If the procedure constants below ever fall out of
// publicProcedures — a re-sort, a rename, a merge — the catalog starts
// answering Unauthenticated and the public page goes dark with no other test
// noticing. These assertions are what catches that.
func bundleListHandler(secret []byte, sawUser *string) http.Handler {
	auth := interceptors.NewAuthInterceptor(secret,
		bundlev1connect.BundleServiceListBundlesProcedure,
		bundlev1connect.BundleServiceGetBundleProcedure,
	)

	return connect.NewUnaryHandler(
		bundlev1connect.BundleServiceListBundlesProcedure,
		func(ctx context.Context, _ *connect.Request[bundlev1.ListBundlesRequest]) (*connect.Response[bundlev1.ListBundlesResponse], error) {
			if id, ok := interceptors.GetUserIDFromContext(ctx); ok {
				*sawUser = id
			}
			return connect.NewResponse(&bundlev1.ListBundlesResponse{}), nil
		},
		connect.WithInterceptors(auth),
	)
}

func TestBundleCatalog_AnonymousCallerIsServed(t *testing.T) {
	secret := []byte("test-secret")
	var sawUser string

	srv := httptest.NewServer(bundleListHandler(secret, &sawUser))
	defer srv.Close()

	client := bundlev1connect.NewBundleServiceClient(srv.Client(), srv.URL)
	_, err := client.ListBundles(context.Background(), connect.NewRequest(&bundlev1.ListBundlesRequest{}))
	if err != nil {
		t.Fatalf("anonymous ListBundles must succeed, got: %v", err)
	}
	if sawUser != "" {
		t.Fatalf("anonymous call must carry no user, got %q", sawUser)
	}
}

// The same endpoint has to recognise a signed-in caller, because that is how
// one response can say whether this person already owns the pack.
func TestBundleCatalog_SignedInCallerIsIdentified(t *testing.T) {
	secret := []byte("test-secret")
	userID := uuid.New()
	var sawUser string

	srv := httptest.NewServer(bundleListHandler(secret, &sawUser))
	defer srv.Close()

	client := bundlev1connect.NewBundleServiceClient(srv.Client(), srv.URL)
	req := connect.NewRequest(&bundlev1.ListBundlesRequest{})
	req.Header().Set("Authorization", "Bearer "+signTestJWT(t, secret, userID, "buyer@example.test"))

	if _, err := client.ListBundles(context.Background(), req); err != nil {
		t.Fatalf("authenticated ListBundles must succeed, got: %v", err)
	}
	if sawUser != userID.String() {
		t.Fatalf("token must reach the handler: got %q want %q", sawUser, userID)
	}
}

// A visitor whose session expired while the tab sat open still has a token in
// local storage. On an optional-auth procedure that is rejected rather than
// treated as anonymous, so the public catalog can 401 for somebody who never
// signed in as far as they are concerned. The client's refresh interceptor is
// what recovers it; this test pins the server behaviour the client has to
// expect.
func TestBundleCatalog_ExpiredTokenIsRejectedNotIgnored(t *testing.T) {
	secret := []byte("test-secret")
	var sawUser string

	srv := httptest.NewServer(bundleListHandler(secret, &sawUser))
	defer srv.Close()

	expired := jwt.NewWithClaims(jwt.SigningMethodHS256, &interceptors.Claims{
		UserID: uuid.NewString(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	})
	signed, err := expired.SignedString(secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	client := bundlev1connect.NewBundleServiceClient(srv.Client(), srv.URL)
	req := connect.NewRequest(&bundlev1.ListBundlesRequest{})
	req.Header().Set("Authorization", "Bearer "+signed)

	_, err = client.ListBundles(context.Background(), req)
	if err == nil {
		t.Fatal("an expired token must not be silently accepted")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("expired token: got code %v, want Unauthenticated", got)
	}
}
