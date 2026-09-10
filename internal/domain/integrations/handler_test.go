package integrations

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	integrationsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/integrations"
)

func asCaller(t *testing.T) context.Context {
	t.Helper()
	return interceptors.ContextWithClaims(t.Context(), &interceptors.Claims{UserID: uuid.NewString()})
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// With no encryption key there is no service. The page is told so.
func TestWithoutAServiceListSaysDisabledAndWritesRefuse(t *testing.T) {
	h := NewHandler(nil, quiet())
	ctx := asCaller(t)

	list, err := h.ListConnections(ctx, connect.NewRequest(&integrationsv1.ListConnectionsRequest{}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list.Msg.GetEnabled() || len(list.Msg.GetConnections()) != 0 {
		t.Errorf("got %+v, want enabled=false and nothing listed", list.Msg)
	}

	_, err = h.Connect(ctx, connect.NewRequest(&integrationsv1.ConnectRequest{Provider: "hermes", Endpoint: testEndpoint}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("connect: code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
	_, err = h.TestConnection(ctx, connect.NewRequest(&integrationsv1.TestConnectionRequest{Provider: "hermes"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("test: code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
	if _, err := h.Disconnect(ctx, connect.NewRequest(&integrationsv1.DisconnectRequest{Provider: "hermes"})); err != nil {
		t.Errorf("disconnect with nothing connected anywhere: %v", err)
	}
}

func TestConnectThenListShowsTheServerAndNeverTheToken(t *testing.T) {
	svc, _ := newService(t)
	h := NewHandler(svc, quiet())
	ctx := asCaller(t)

	got, err := h.Connect(ctx, connect.NewRequest(&integrationsv1.ConnectRequest{
		Provider: "hermes", Endpoint: testEndpoint, AccessToken: testToken,
	}))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if !got.Msg.GetConnection().GetHasToken() {
		t.Error("the connection does not report having a token")
	}
	if strings.Contains(got.Msg.String(), testToken) {
		t.Fatal("the connect response carries the token")
	}

	list, err := h.ListConnections(ctx, connect.NewRequest(&integrationsv1.ListConnectionsRequest{}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !list.Msg.GetEnabled() || len(list.Msg.GetConnections()) != 1 {
		t.Fatalf("list = %+v", list.Msg)
	}
	conn := list.Msg.GetConnections()[0]
	if conn.GetProvider() != "hermes" || !strings.HasPrefix(conn.GetEndpoint(), "http://hermes-vps-2") {
		t.Errorf("connection = %+v", conn)
	}
	if strings.Contains(list.Msg.String(), testToken) {
		t.Fatal("the list response carries the token")
	}
}

// Everything the person can fix comes back as InvalidArgument with a sentence.
func TestBadInputIsInvalidArgumentWithAReason(t *testing.T) {
	cases := []struct {
		name string
		req  *integrationsv1.ConnectRequest
	}{
		{"unknown provider", &integrationsv1.ConnectRequest{Provider: "dropbox", Endpoint: testEndpoint}},
		{"loopback", &integrationsv1.ConnectRequest{Provider: "hermes", Endpoint: "http://127.0.0.1:9000/mcp"}},
		{"metadata", &integrationsv1.ConnectRequest{Provider: "hermes", Endpoint: "http://169.254.169.254/mcp"}},
		{"empty", &integrationsv1.ConnectRequest{Provider: "hermes"}},
		{"scheme", &integrationsv1.ConnectRequest{Provider: "hermes", Endpoint: "file:///etc/passwd"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newService(t)
			h := NewHandler(svc, quiet())

			_, err := h.Connect(asCaller(t), connect.NewRequest(tc.req))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (%v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if strings.Contains(err.Error(), "integrations:") {
				t.Errorf("the package prefix leaked into user text: %q", err)
			}
			if len(repo.rows) != 0 {
				t.Error("a row was written for input that was refused")
			}
		})
	}
}

// A server that cannot be reached is the answer to the question the button
// asks, not a failure of the RPC — and the answer must not name the token.
func TestAnUnreachableServerIsReportedNotThrown(t *testing.T) {
	svc, _ := newService(t)
	h := NewHandler(svc, quiet())
	ctx := asCaller(t)

	if _, err := h.Connect(ctx, connect.NewRequest(&integrationsv1.ConnectRequest{
		Provider: "hermes", Endpoint: testEndpoint, AccessToken: testToken,
	})); err != nil {
		t.Fatalf("connect: %v", err)
	}

	got, err := h.TestConnection(ctx, connect.NewRequest(&integrationsv1.TestConnectionRequest{Provider: "hermes"}))
	if err != nil {
		t.Fatalf("test returned an RPC error: %v", err)
	}
	if got.Msg.GetOk() {
		t.Fatal("a server that does not exist tested ok")
	}
	if got.Msg.GetError() == "" {
		t.Error("no reason given")
	}
	if strings.Contains(got.Msg.GetError(), testToken) {
		t.Error("the reason names the token")
	}
}

func TestTestingAnUnconnectedIntegrationIsNotFound(t *testing.T) {
	svc, _ := newService(t)
	h := NewHandler(svc, quiet())

	_, err := h.TestConnection(asCaller(t), connect.NewRequest(&integrationsv1.TestConnectionRequest{Provider: "calendar"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestDisconnectRemovesTheServerAndIsIdempotent(t *testing.T) {
	svc, repo := newService(t)
	h := NewHandler(svc, quiet())
	ctx := asCaller(t)

	if _, err := h.Connect(ctx, connect.NewRequest(&integrationsv1.ConnectRequest{
		Provider: "hermes", Endpoint: testEndpoint, AccessToken: testToken,
	})); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := h.Disconnect(ctx, connect.NewRequest(&integrationsv1.DisconnectRequest{Provider: "hermes"})); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if len(repo.rows) != 0 {
		t.Error("the connection is still stored")
	}
	if _, err := h.Disconnect(ctx, connect.NewRequest(&integrationsv1.DisconnectRequest{Provider: "hermes"})); err != nil {
		t.Errorf("second disconnect: %v", err)
	}
}

func TestEveryRPCRequiresACaller(t *testing.T) {
	h := NewHandler(nil, quiet())
	ctx := t.Context()

	calls := map[string]func() error{
		"list": func() error {
			_, err := h.ListConnections(ctx, connect.NewRequest(&integrationsv1.ListConnectionsRequest{}))
			return err
		},
		"connect": func() error {
			_, err := h.Connect(ctx, connect.NewRequest(&integrationsv1.ConnectRequest{}))
			return err
		},
		"disconnect": func() error {
			_, err := h.Disconnect(ctx, connect.NewRequest(&integrationsv1.DisconnectRequest{}))
			return err
		},
		"test": func() error {
			_, err := h.TestConnection(ctx, connect.NewRequest(&integrationsv1.TestConnectionRequest{}))
			return err
		},
	}
	for name, call := range calls {
		if err := call(); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("%s: code = %v, want Unauthenticated", name, connect.CodeOf(err))
		}
	}
}
