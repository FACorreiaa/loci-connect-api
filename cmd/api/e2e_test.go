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
			RateLimitPerSecond:   10000,
			RateLimitBurst:       10000,
			DefaultRPCTimeout:    30 * time.Second,
			ChatRPCTimeout:       3 * time.Minute,
			ChatStreamMaxTimeout: 10 * time.Minute,
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
		Gemini: config.GeminiConfig{
			APIKey:         "e2e-dummy-key",
			Model:          "gemini-1.5-flash",
			EmbeddingModel: "gemini-embedding-exp-03-07",
			MaxRetries:     3,
		},
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

// registerAndLogin creates a fresh user and returns its access + refresh tokens.
func registerAndLogin(t *testing.T, ctx context.Context, authClient authconnect.AuthServiceClient) (access, refresh string) {
	t.Helper()
	email := fmt.Sprintf("e2e-%s@example.com", uuid.NewString())
	const password = "Sup3rStr0ngP@ss!"
	_, err := authClient.Register(ctx, connect.NewRequest(&authpb.RegisterRequest{
		Email: email, Username: "e2e" + uuid.NewString()[:8], Password: password,
	}))
	require.NoError(t, err, "register")
	loginResp, err := authClient.Login(ctx, connect.NewRequest(&authpb.LoginRequest{
		Email: email, Password: password,
	}))
	require.NoError(t, err, "login")
	access = loginResp.Msg.GetAccessToken()
	refresh = loginResp.Msg.GetRefreshToken()
	require.NotEmpty(t, access, "access token")
	require.NotEmpty(t, refresh, "refresh token")
	return access, refresh
}

// authPasses returns true if a protected RPC with the given token is NOT
// rejected by the auth interceptor (it may still fail for other reasons).
func authPasses(ctx context.Context, userClient userconnect.UserServiceClient, token string) bool {
	req := connect.NewRequest(&userpb.GetUserProfileRequest{})
	if token != "" {
		req.Header().Set("Authorization", "Bearer "+token)
	}
	_, err := userClient.GetUserProfile(ctx, req)
	return err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated
}

// TestE2E_AuthLifecycle exercises the full token lifecycle that the flaky
// "login -> navigate -> disconnected" reports hinge on: a fresh access token
// works, refresh rotates the token pair, the new access token works, and the
// OLD refresh token is rejected after rotation (so any client retry with a
// stale refresh token is a hard logout — the behaviour to design around).
func TestE2E_AuthLifecycle(t *testing.T) {
	baseURL, cleanup := bootServer(t)
	defer cleanup()

	ctx := context.Background()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	authClient := authconnect.NewAuthServiceClient(httpClient, baseURL)
	userClient := userconnect.NewUserServiceClient(httpClient, baseURL)

	access1, refresh1 := registerAndLogin(t, ctx, authClient)

	// 1. Fresh access token passes auth.
	assert.True(t, authPasses(ctx, userClient, access1), "fresh access token should pass auth")

	// 2. Refresh rotates the pair.
	refreshResp, err := authClient.RefreshToken(ctx, connect.NewRequest(&authpb.RefreshTokenRequest{
		RefreshToken: refresh1,
	}))
	require.NoError(t, err, "refresh with a valid refresh token should succeed")
	access2 := refreshResp.Msg.GetAccessToken()
	refresh2 := refreshResp.Msg.GetRefreshToken()
	require.NotEmpty(t, access2, "refreshed access token")
	require.NotEmpty(t, refresh2, "refreshed refresh token")
	assert.NotEqual(t, refresh1, refresh2, "refresh token should rotate")

	// 3. New access token passes auth.
	assert.True(t, authPasses(ctx, userClient, access2), "refreshed access token should pass auth")

	// 4. The OLD refresh token is now dead (its session was deleted on rotation).
	_, err = authClient.RefreshToken(ctx, connect.NewRequest(&authpb.RefreshTokenRequest{
		RefreshToken: refresh1,
	}))
	require.Error(t, err, "a rotated/old refresh token must be rejected")

	// 5. The new refresh token still works (single rotation forward).
	_, err = authClient.RefreshToken(ctx, connect.NewRequest(&authpb.RefreshTokenRequest{
		RefreshToken: refresh2,
	}))
	require.NoError(t, err, "the current refresh token should still refresh")

	// 6. A garbage token is rejected.
	assert.False(t, authPasses(ctx, userClient, "not-a-jwt"), "garbage token must be Unauthenticated")
}
