//go:build e2e

package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	authpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth/authconnect"
	userpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/user"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/user/userconnect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// bootServer wires the full dependency graph against the ephemeral Postgres
// container and serves the assembled router (full interceptor chain) over an
// httptest server.
func bootServer(t *testing.T) (string, func()) {
	t.Helper()
	host, port := testsupport.HostPort(t)

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:    "localhost",
			Port:    8080,
			BaseURL: "http://localhost:8080",
			// Non-zero so the rate-limit interceptor is constructed (the router
			// only builds it when both knobs are > 0); high enough not to trip.
			RateLimitPerSecond: 10000,
			RateLimitBurst:     10000,
		},
		Database: config.DatabaseConfig{
			Host:     host,
			Port:     port,
			User:     "postgres",
			Password: "postgres",
			Database: "loci_test",
			SSLMode:  "disable",
		},
		Auth: config.AuthConfig{
			JWTSecret:  "e2e-test-jwt-secret-0123456789abcdef",
			AdminEmail: "",
		},
		Observability: config.ObservabilityConfig{MetricsEnabled: false},
		// Dummy Gemini creds: genai.NewClient only builds a client (no network),
		// so InitDependencies boots; the e2e flow never calls LLM endpoints.
		Gemini: config.GeminiConfig{APIKey: "e2e-dummy-key", Model: "gemini-1.5-flash"},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps, err := InitDependencies(cfg, logger)
	require.NoError(t, err, "InitDependencies should boot against the test DB")

	srv := httptest.NewServer(SetupRouter(deps))

	cleanup := func() {
		srv.Close()
		deps.Cleanup()
	}
	return srv.URL, cleanup
}

func TestE2E_HealthAndReady(t *testing.T) {
	baseURL, cleanup := bootServer(t)
	defer cleanup()

	for _, path := range []string{"/health", "/ready"} {
		resp, err := http.Get(baseURL + path)
		require.NoError(t, err, "GET %s", path)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "GET %s", path)
	}
}

func TestE2E_RegisterLoginAndAuthInterceptor(t *testing.T) {
	baseURL, cleanup := bootServer(t)
	defer cleanup()

	ctx := context.Background()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	authClient := authconnect.NewAuthServiceClient(httpClient, baseURL)
	userClient := userconnect.NewUserServiceClient(httpClient, baseURL)

	email := fmt.Sprintf("e2e-%s@example.com", uuid.NewString())
	const password = "Sup3rStr0ngP@ss!"
	username := "e2euser" + uuid.NewString()[:8]

	// 1. Register (public procedure).
	_, err := authClient.Register(ctx, connect.NewRequest(&authpb.RegisterRequest{
		Email:    email,
		Username: username,
		Password: password,
	}))
	require.NoError(t, err, "register should succeed")

	// 2. Login (public procedure) -> JWT.
	loginResp, err := authClient.Login(ctx, connect.NewRequest(&authpb.LoginRequest{
		Email:    email,
		Password: password,
	}))
	require.NoError(t, err, "login should succeed")
	token := loginResp.Msg.GetAccessToken()
	require.NotEmpty(t, token, "login must return an access token")

	// 3. Protected RPC without a token -> the auth interceptor rejects it.
	_, err = userClient.GetUserProfile(ctx, connect.NewRequest(&userpb.GetUserProfileRequest{}))
	require.Error(t, err, "protected RPC without token must fail")
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err),
		"missing token should yield Unauthenticated")

	// 4. Protected RPC with the minted token -> auth interceptor accepts it.
	//    The call may still fail downstream, but it must NOT be Unauthenticated.
	authedReq := connect.NewRequest(&userpb.GetUserProfileRequest{})
	authedReq.Header().Set("Authorization", "Bearer "+token)
	_, err = userClient.GetUserProfile(ctx, authedReq)
	if err != nil {
		assert.NotEqual(t, connect.CodeUnauthenticated, connect.CodeOf(err),
			"valid token must pass the auth interceptor, got %v", err)
	}
}
