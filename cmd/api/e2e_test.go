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
	authpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth/authconnect"
	recommendationpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recommendation"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recommendation/recommendationconnect"
	userpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/user"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/user/userconnect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// bootServer wires the full dependency graph against the ephemeral Postgres
// container and serves the assembled router (full interceptor chain) over an
// httptest server.
func bootServer(t *testing.T) (string, *Dependencies, func()) {
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
			// pgxpool rejects MaxConns < 1; the hand-built config skips
			// config.Load's defaults, so set the pool sizing explicitly.
			MaxConns: 10,
			MinConns: 2,
			// Likewise the lifetimes. Left at zero, pgxpool refuses every
			// acquisition ("too many failed attempts acquiring connection")
			// and InitDependencies fails with "database connection failed
			// after retries" — against a container that is up and already
			// migrated. Mirrors config.Load's defaults.
			MaxConnLifetime: 5 * time.Minute,
			MaxConnIdleTime: 10 * time.Minute,
		},
		Auth: config.AuthConfig{
			JWTSecret: "e2e-test-jwt-secret-0123456789abcdef",
			// A distinct refresh secret, because sharing one key lets an access
			// token verify as a refresh token — which production refuses to boot
			// with, and the e2e harness should not quietly differ on.
			JWTRefreshSecret: "e2e-test-refresh-secret-abcdef0123456789",
			// Token lifetimes, for the same reason as the pool lifetimes above:
			// this config is hand-built, so it gets none of config.Load's
			// defaults. Left at zero every token is issued already expired, and
			// the auth tests fail with "token has invalid claims: token is
			// expired" on a token minted a millisecond earlier.
			AccessTokenTTL:  time.Hour,
			RefreshTokenTTL: 30 * 24 * time.Hour,
			AdminEmail:      "",
		},
		Observability: config.ObservabilityConfig{MetricsEnabled: false},
		// Dummy Gemini creds: genai.NewClient only builds a client (no network),
		// so InitDependencies boots; the e2e flow never calls LLM endpoints.
		AI: config.AIConfig{
			Provider:       config.AIProviderGemini,
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
	return srv.URL, deps, cleanup
}

func TestE2E_HealthAndReady(t *testing.T) {
	baseURL, _, cleanup := bootServer(t)
	defer cleanup()

	for _, path := range []string{"/health", "/ready"} {
		resp, err := http.Get(baseURL + path)
		require.NoError(t, err, "GET %s", path)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "GET %s", path)
	}
}

func TestE2E_RegisterLoginAndAuthInterceptor(t *testing.T) {
	baseURL, _, cleanup := bootServer(t)
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
	baseURL, _, cleanup := bootServer(t)
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

func recommendationVector(axis int, value float32) []float32 {
	vector := make([]float32, 768)
	vector[axis] = value
	return vector
}

func TestE2E_RecommendationFlywheel(t *testing.T) {
	baseURL, deps, cleanup := bootServer(t)
	defer cleanup()

	ctx := context.Background()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	authClient := authconnect.NewAuthServiceClient(httpClient, baseURL)
	recommendationClient := recommendationconnect.NewRecommendationServiceClient(httpClient, baseURL)

	email := fmt.Sprintf("flywheel-%s@example.com", uuid.NewString())
	const password = "Sup3rStr0ngP@ss!"
	_, err := authClient.Register(ctx, connect.NewRequest(&authpb.RegisterRequest{
		Email: email, Username: "flywheel" + uuid.NewString()[:8], Password: password,
	}))
	require.NoError(t, err)
	login, err := authClient.Login(ctx, connect.NewRequest(&authpb.LoginRequest{Email: email, Password: password}))
	require.NoError(t, err)
	userID, err := uuid.Parse(login.Msg.GetUserId())
	require.NoError(t, err)

	cityID := uuid.New()
	likedPOI := uuid.New()
	otherPOI := uuid.New()
	oppositePOI := uuid.New()
	_, err = deps.DB.Pool.Exec(ctx, `
		INSERT INTO cities (id, name, country) VALUES ($1, 'Flywheel City', 'PT')`, cityID)
	require.NoError(t, err)
	for index, fixture := range []struct {
		id        uuid.UUID
		name      string
		embedding []float32
	}{
		{id: likedPOI, name: "Hidden Garden", embedding: recommendationVector(0, 1)},
		{id: otherPOI, name: "Night Market", embedding: recommendationVector(1, 1)},
		{id: oppositePOI, name: "Busy Plaza", embedding: recommendationVector(0, -1)},
	} {
		_, err = deps.DB.Pool.Exec(ctx, `
			INSERT INTO points_of_interest (
				id, name, description, location, city_id, category, embedding
			) VALUES ($1, $2, 'E2E recommendation candidate',
				ST_SetSRID(ST_MakePoint($3, $4), 4326), $5, 'attraction', $6::vector)`,
			fixture.id, fixture.name, -9.14+float64(index)/100, 38.72+float64(index)/100,
			cityID, preference.FormatVector(fixture.embedding))
		require.NoError(t, err)
	}

	trace := &recommendationpb.RecommendationTrace{
		RunId: "e2e-flywheel-run", ItemId: likedPOI.String(), Rank: 2,
		AlgorithmVersion:  "discover-preference-rerank-v1",
		ExperimentVariant: preference.ExperimentVariant(userID),
		Surface:           recommendationpb.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER,
		Channel:           recommendationpb.RecommendationChannel_RECOMMENDATION_CHANNEL_WEB,
	}
	require.NoError(t, deps.RecommendationHandler.IssueTraces(ctx, userID, []*recommendationpb.RecommendationTrace{trace}))

	poiID := likedPOI.String()
	events := []*recommendationpb.RecommendationEvent{
		{
			ClientEventId: uuid.NewString(), EventType: recommendationpb.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_FAVORITED,
			Trace: trace, OccurredAt: timestamppb.Now(), PoiId: &poiID,
		},
		{
			ClientEventId: uuid.NewString(), EventType: recommendationpb.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_FAVORITED,
			Trace: trace, OccurredAt: timestamppb.Now(), PoiId: &poiID,
		},
	}
	authedEvents := connect.NewRequest(&recommendationpb.RecordEventsRequest{Events: events})
	authedEvents.Header().Set("Authorization", "Bearer "+login.Msg.GetAccessToken())
	recorded, err := recommendationClient.RecordEvents(ctx, authedEvents)
	require.NoError(t, err)
	assert.Equal(t, int32(1), recorded.Msg.GetAccepted())
	assert.Equal(t, int32(1), recorded.Msg.GetDuplicates())

	otherAccess, _ := registerAndLogin(t, ctx, authClient)
	forged := connect.NewRequest(&recommendationpb.RecordEventsRequest{Events: []*recommendationpb.RecommendationEvent{{
		ClientEventId: uuid.NewString(), EventType: recommendationpb.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_FAVORITED,
		Trace: trace, OccurredAt: timestamppb.Now(), PoiId: &poiID,
	}}})
	forged.Header().Set("Authorization", "Bearer "+otherAccess)
	_, err = recommendationClient.RecordEvents(ctx, forged)
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	var feedbackCount int
	require.NoError(t, deps.DB.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM preference_feedback WHERE user_id = $1 AND poi_id = $2`, userID, poiID).Scan(&feedbackCount))
	assert.Equal(t, 1, feedbackCount, "logical retry must create one learning signal")

	reranker := preference.NewReranker(deps.DB.Pool, deps.PreferenceVectors, deps.Logger)
	stats, err := reranker.Run(ctx, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.UsersUpdated, 1)
	learned, found, err := deps.PreferenceVectors.GetEmbedding(ctx, userID)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, learned, 768)
	assert.InDelta(t, 1, learned[0], 0.0001)

	ranker, ok := deps.POIRepo.(interface {
		RankPOIsByPreference(context.Context, uuid.UUID, []uuid.UUID) ([]uuid.UUID, error)
	})
	require.True(t, ok)
	ranked, err := ranker.RankPOIsByPreference(ctx, userID, []uuid.UUID{otherPOI, oppositePOI, likedPOI})
	require.NoError(t, err)
	require.Len(t, ranked, 3)
	assert.Equal(t, likedPOI, ranked[0], "learned taste must move the liked place to rank one")
}
