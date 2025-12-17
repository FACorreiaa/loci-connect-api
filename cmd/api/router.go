package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"connectrpc.com/connect"
	c "connectrpc.com/cors"

	"connectrpc.com/validate"
	authconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth/authconnect"
	chatconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat/chatconnect"
	discoverconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/discover/discoverconnect"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/export/exportv1connect"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/favorites/v1/favoritesv1connect"
	interestconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/interest/interestconnect"
	itineraryconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/itinerary/itineraryconnect"
	paymentv1connect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/payment/v1/paymentv1connect"
	profileconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/profile/profileconnect"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/recents/recentsv1connect"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/share/sharev1connect"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/statistics/statisticsv1connect"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/tags/tagsv1connect"
	userconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/user/userconnect"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"go.opentelemetry.io/otel"
	"golang.org/x/time/rate"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/payment" // Add import
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
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
		authconnect.AuthServiceForgotPasswordProcedure,
		authconnect.AuthServiceResetPasswordProcedure,
		// Public statistics endpoint
		statisticsv1connect.StatisticsServiceGetMainPageStatisticsProcedure,
		statisticsv1connect.StatisticsServiceStreamMainPageStatisticsProcedure,
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
	subscriptionInterceptor := subscription.NewRateLimitInterceptor(deps.SubscriptionService)

	// Setup interceptor chain
	interceptorChain := connect.WithInterceptors(
		requestIDInterceptor,
		tracingInterceptor,
		validationInterceptor,
		rateLimiter,
		subscriptionInterceptor,
		interceptors.NewRecoveryInterceptor(deps.Logger),
		interceptors.NewLoggingInterceptor(deps.Logger),
		interceptors.NewAuthInterceptor(jwtSecret, publicProcedures...),
		observability.NewMetricsInterceptor(),
	)

	// Register Connect RPC routes
	registerConnectRoutes(mux, deps, interceptorChain)

	// Register Webhooks
	if deps.PaymentService != nil {
		mux.Handle("/webhooks/stripe", payment.WebhookHandler(deps.PaymentService, deps.Logger))
		deps.Logger.Info("registered webhook", "path", "/webhooks/stripe")
	}

	// Register health and metrics routes
	registerUtilityRoutes(mux, deps)

	corsHandler := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000"},           // Restricted to localhost for development
		AllowedMethods:   c.AllowedMethods(),                          // ["GET", "POST", "OPTIONS"]
		AllowedHeaders:   append(c.AllowedHeaders(), "Authorization"), // Adds "Authorization" for safety
		ExposedHeaders:   c.ExposedHeaders(),                          // ["Grpc-Status", "Grpc-Message", "Grpc-Status-Details-Bin"]
		AllowCredentials: true,
		MaxAge:           7200, // Cache preflights for 2 hours
	})

	// Enable CORS for browser clients (Buf Studio, local frontend)
	//corsHandler := cors.New(cors.Options{
	//	AllowedOrigins: []string{
	//		"https://buf.build",
	//		"https://studio.buf.build",
	//		"http://localhost:3000",
	//	},
	//	AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
	//	AllowedHeaders: []string{
	//		"Accept-Encoding",
	//		"Content-Encoding",
	//		"Content-Type",
	//		"Connect-Protocol-Version",
	//		"Connect-Timeout-Ms",
	//		"Grpc-Timeout",
	//		"X-Grpc-Web",
	//		"X-User-Agent",
	//	},
	//	ExposedHeaders: []string{
	//		"Grpc-Status",
	//		"Grpc-Message",
	//		"Grpc-Status-Details-Bin",
	//	},
	//	AllowCredentials: true,
	//})

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

	if deps.ChatHandler != nil {
		chatPath, chatHandler := chatconnect.NewChatServiceHandler(deps.ChatHandler, opts)
		mux.Handle(chatPath, chatHandler)
		deps.Logger.Info("registered Connect RPC service", "path", chatPath)
	}

	if deps.DiscoverHandler != nil {
		discoverPath, discoverHandler := discoverconnect.NewDiscoverServiceHandler(deps.DiscoverHandler, opts)
		mux.Handle(discoverPath, discoverHandler)
		deps.Logger.Info("registered Connect RPC service", "path", discoverPath)
	}

	if deps.ProfileHandler != nil {
		profilePath, profileHandler := profileconnect.NewProfileServiceHandler(deps.ProfileHandler, opts)
		mux.Handle(profilePath, profileHandler)
		deps.Logger.Info("registered Connect RPC service", "path", profilePath)
	}

	if deps.ItineraryHandler != nil {
		itineraryPath, itineraryHandler := itineraryconnect.NewItineraryServiceHandler(deps.ItineraryHandler, opts)
		mux.Handle(itineraryPath, itineraryHandler)
		deps.Logger.Info("registered Connect RPC service", "path", itineraryPath)
	}

	if deps.StatisticsHandler != nil {
		statsPath, statsHandler := statisticsv1connect.NewStatisticsServiceHandler(deps.StatisticsHandler, opts)
		mux.Handle(statsPath, statsHandler)
		deps.Logger.Info("registered Connect RPC service", "path", statsPath)
	}

	if deps.RecentsHandler != nil {
		recentsPath, recentsHandler := recentsv1connect.NewRecentsServiceHandler(deps.RecentsHandler, opts)
		mux.Handle(recentsPath, recentsHandler)
		deps.Logger.Info("registered Connect RPC service", "path", recentsPath)
	}

	if deps.UserHandler != nil {
		userPath, userHandler := userconnect.NewUserServiceHandler(deps.UserHandler, opts)
		mux.Handle(userPath, userHandler)
		deps.Logger.Info("registered Connect RPC service", "path", userPath)
	}

	if deps.InterestHandler != nil {
		interestPath, interestHandler := interestconnect.NewInterestServiceHandler(deps.InterestHandler, opts)
		mux.Handle(interestPath, interestHandler)
		deps.Logger.Info("registered Connect RPC service", "path", interestPath)
	}

	if deps.TagsHandler != nil {
		tagsPath, tagsHandler := tagsv1connect.NewTagsServiceHandler(deps.TagsHandler, opts)
		mux.Handle(tagsPath, tagsHandler)
		deps.Logger.Info("registered Connect RPC service", "path", tagsPath)
	}

	if deps.PaymentHandler != nil {
		paymentPath, paymentHandler := paymentv1connect.NewPaymentServiceHandler(deps.PaymentHandler, opts)
		mux.Handle(paymentPath, paymentHandler)
		deps.Logger.Info("registered Connect RPC service", "path", paymentPath)
	}

	if deps.FavoritesHandler != nil {
		favoritesPath, favoritesHandler := favoritesv1connect.NewFavoritesServiceHandler(deps.FavoritesHandler, opts)
		mux.Handle(favoritesPath, favoritesHandler)
		deps.Logger.Info("registered Connect RPC service", "path", favoritesPath)
	}

	if deps.ExportHandler != nil {
		exportPath, exportHandler := exportv1connect.NewExportServiceHandler(deps.ExportHandler, opts)
		mux.Handle(exportPath, exportHandler)
		deps.Logger.Info("registered Connect RPC service", "path", exportPath)
	}

	if deps.ShareHandler != nil {
		// Register ShareService RPC
		sharePath, shareHandler := sharev1connect.NewShareServiceHandler(deps.ShareHandler, opts)
		mux.Handle(sharePath, shareHandler)
		deps.Logger.Info("registered Connect RPC service", "path", sharePath)

		// Register OG meta HTTP handler for social sharing
		mux.Handle("/share/", deps.ShareHandler.OGMetaHandler())
		deps.Logger.Info("registered OG meta handler", "path", "/share/")
	}

	deps.Logger.Info("Connect RPC routes configured")
}

// registerUtilityRoutes registers health check, metrics, and other utility routes
func registerUtilityRoutes(mux *http.ServeMux, deps *Dependencies) {
	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if err := deps.DB.Health(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, writeErr := w.Write([]byte("database unhealthy")); writeErr != nil {
				deps.Logger.Error("failed to write health response", slog.Any("error", writeErr))
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			deps.Logger.Error("failed to write health response", slog.Any("error", err))
		}
	})
	deps.Logger.Info("registered health check", "path", "/health")

	// Extended health with details on dependencies/env
	mux.HandleFunc("/health/details", func(w http.ResponseWriter, _ *http.Request) {
		type status struct {
			Status string `json:"status"`
			Detail string `json:"detail,omitempty"`
		}
		result := map[string]status{
			"db":    {Status: "ok"},
			"env":   {Status: "ok"},
			"ready": {Status: "ok"},
		}

		if err := deps.DB.Health(); err != nil {
			result["db"] = status{Status: "fail", Detail: err.Error()}
			result["ready"] = status{Status: "fail", Detail: "db unavailable"}
		}

		if os.Getenv("GEMINI_API_KEY") == "" {
			result["env"] = status{Status: "warn", Detail: "GEMINI_API_KEY missing"}
		}

		for _, v := range result {
			if v.Status == "fail" {
				w.WriteHeader(http.StatusServiceUnavailable)
				if err := json.NewEncoder(w).Encode(result); err != nil {
					deps.Logger.Error("failed to encode health details", slog.Any("error", err))
				}
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(result); err != nil {
			deps.Logger.Error("failed to encode health details", slog.Any("error", err))
		}
	})
	deps.Logger.Info("registered health details", "path", "/health/details")

	// Readiness check endpoint
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ready")); err != nil {
			deps.Logger.Error("failed to write readiness response", slog.Any("error", err))
		}
	})
	deps.Logger.Info("registered readiness check", "path", "/ready")

	// Metrics endpoint (Prometheus)
	if deps.Config.Observability.MetricsEnabled {
		mux.Handle("/metrics", promhttp.Handler())
		deps.Logger.Info("registered metrics endpoint", "path", "/metrics")
	}
}
