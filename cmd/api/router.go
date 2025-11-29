package api

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	authconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth/authconnect"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"go.opentelemetry.io/otel"
	"golang.org/x/time/rate"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// SetupRouter configures all routes and returns the HTTP service
func SetupRouter(deps *Dependencies) http.Handler {
	mux := http.NewServeMux()

	jwtSecret := []byte(deps.Config.Auth.JWTSecret)
	if len(jwtSecret) == 0 {
		deps.Logger.Warn("JWT secret is empty; authentication interceptor will reject requests")
	}

	publicProcedures := []string{
		authconnect.AuthServiceRegisterProcedure,
		authconnect.AuthServiceLoginProcedure,
		authconnect.AuthServiceRefreshTokenProcedure,
		authconnect.AuthServiceValidateSessionProcedure,
	}

	tracer := otel.GetTracerProvider().Tracer("loci/api")

	var rateLimiter connect.Interceptor
	if deps.Config.Server.RateLimitPerSecond > 0 && deps.Config.Server.RateLimitBurst > 0 {
		limiter := rate.NewLimiter(
			rate.Limit(float64(deps.Config.Server.RateLimitPerSecond)),
			deps.Config.Server.RateLimitBurst,
		)
		rateLimiter = interceptors.NewRateLimitInterceptor(limiter)
	}

	requestIDInterceptor := interceptors.NewRequestIDInterceptor("X-Request-ID")
	tracingInterceptor := interceptors.NewTracingInterceptor(tracer)
	validationInterceptor := validate.NewInterceptor()

	// Setup interceptor chain
	interceptorChain := connect.WithInterceptors(
		requestIDInterceptor,
		tracingInterceptor,
		validationInterceptor,
		rateLimiter,
		interceptors.NewRecoveryInterceptor(deps.Logger),
		interceptors.NewLoggingInterceptor(deps.Logger),
		interceptors.NewAuthInterceptor(jwtSecret, publicProcedures...),
		observability.NewMetricsInterceptor(),
	)

	// Register Connect RPC routes
	registerConnectRoutes(mux, deps, interceptorChain)

	// Register health and metrics routes
	registerUtilityRoutes(mux, deps)

	// Enable CORS for browser clients (Buf Studio, local frontend)
	corsHandler := cors.New(cors.Options{
		AllowedOrigins: []string{
			"https://buf.build",
			"https://studio.buf.build",
			"http://localhost:3000",
		},
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders: []string{
			"Accept-Encoding",
			"Content-Encoding",
			"Content-Type",
			"Connect-Protocol-Version",
			"Connect-Timeout-Ms",
			"Grpc-Timeout",
			"X-Grpc-Web",
			"X-User-Agent",
		},
		ExposedHeaders: []string{
			"Grpc-Status",
			"Grpc-Message",
			"Grpc-Status-Details-Bin",
		},
		AllowCredentials: true,
	})

	return corsHandler.Handler(mux)
}

// registerConnectRoutes registers all Connect RPC service service
func registerConnectRoutes(mux *http.ServeMux, deps *Dependencies, opts connect.HandlerOption) {
	authServicePath, authServiceHandler := authconnect.NewAuthServiceHandler(
		deps.AuthHandler,
		opts,
	)
	mux.Handle(authServicePath, authServiceHandler)
	deps.Logger.Info("registered Connect RPC service", "path", authServicePath)

	deps.Logger.Info("Connect RPC routes configured")
}

// registerUtilityRoutes registers health check, metrics, and other utility routes
func registerUtilityRoutes(mux *http.ServeMux, deps *Dependencies) {
	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if err := deps.DB.Health(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("database unhealthy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	deps.Logger.Info("registered health check", "path", "/health")

	// Readiness check endpoint
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
	})
	deps.Logger.Info("registered readiness check", "path", "/ready")

	// Metrics endpoint (Prometheus)
	if deps.Config.Observability.MetricsEnabled {
		mux.Handle("/metrics", promhttp.Handler())
		deps.Logger.Info("registered metrics endpoint", "path", "/metrics")
	}
}
