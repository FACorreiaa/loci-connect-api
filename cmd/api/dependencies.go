package api

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/apikey"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/repository"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	chathandler "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/handler"
	chatrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/repository"
	chatservice "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	cityhandler "github.com/FACorreiaa/loci-connect-api/internal/domain/city/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/compare"
	customauthhandler "github.com/FACorreiaa/loci-connect-api/internal/domain/custom_auth/handler"
	customauthservice "github.com/FACorreiaa/loci-connect-api/internal/domain/custom_auth/service"
	discoverdomain "github.com/FACorreiaa/loci-connect-api/internal/domain/discover"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/entitlement"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/export"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/favorites"
	interestrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/interests"
	interesthandler "github.com/FACorreiaa/loci-connect-api/internal/domain/interests/handler"
	itinerarylist "github.com/FACorreiaa/loci-connect-api/internal/domain/list"
	itineraryhandler "github.com/FACorreiaa/loci-connect-api/internal/domain/list/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/memory"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/mfa"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/payment"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/placeintel"
	poirepo "github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	poihandler "github.com/FACorreiaa/loci-connect-api/internal/domain/poi/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	profiles "github.com/FACorreiaa/loci-connect-api/internal/domain/profiles"
	profilehandler "github.com/FACorreiaa/loci-connect-api/internal/domain/profiles/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/recents"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/recommendation"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	reviewdomain "github.com/FACorreiaa/loci-connect-api/internal/domain/review"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/share"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/statistics"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	tagrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/tags"
	tagshandler "github.com/FACorreiaa/loci-connect-api/internal/domain/tags/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/travelhistory"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/user"
	userhandler "github.com/FACorreiaa/loci-connect-api/internal/domain/user/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/userdata"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/payment/v1/paymentv1connect"
)

// Dependencies holds all application dependencies
type Dependencies struct {
	Config   *config.Config
	DB       *db.DB
	Logger   *slog.Logger
	AppCache cachestore.Store

	// Repositories
	AuthRepo          repository.AuthRepository
	InterestRepo      interestrepo.Repository
	TagRepo           tagrepo.Repository
	ProfileRepo       profiles.Repository
	POIRepo           poirepo.Repository
	CityRepo          cityrepo.Repository
	ChatRepo          chatrepo.Repository
	DiscoverRepo      discoverdomain.Repository
	ListRepo          itinerarylist.Repository
	StatisticsRepo    statistics.Repository
	RecentsRepo       recents.Repository
	UserRepo          user.UserRepo
	UsageRepo         subscription.Repository
	PaymentRepo       payment.Repository
	FavoritesRepo     favorites.Repository
	APIKeyRepo        apikey.Repository
	ReviewRepo        reviewdomain.Repository
	ShareRepo         share.Repository
	TripRepo          trip.Repository
	TravelHistoryRepo travelhistory.Repository

	// Services
	TokenManager service.TokenManager
	AuthService  *service.AuthService
	// MFAService is nil when MFA_SECRET_KEY is unset.
	MFAService          *mfa.Service
	ChatService         chatservice.LlmInteractiontService
	ProfileSvc          profiles.Service
	POISvc              poirepo.Service
	DiscoverSvc         discoverdomain.Service
	ListSvc             itinerarylist.Service
	StatisticsSvc       statistics.Service
	RecentsSvc          recents.Service
	UserSvc             user.UserService
	InterestSvc         interestrepo.Service
	TagsSvc             tagshandler.Service
	SubscriptionService subscription.Service
	APIKeyService       apikey.Service
	PaymentService      payment.Service
	OAuthService        *customauthservice.OAuthService
	PhoneService        *customauthservice.PhoneService
	ReviewSvc           reviewdomain.Service

	// Handlers
	AuthHandler              *handler.AuthHandler
	ChatHandler              *chathandler.ChatHandler
	ProfileHandler           *profilehandler.ProfileHandler
	DiscoverHandler          *discoverdomain.Handler
	ItineraryHandler         *itineraryhandler.ItineraryHandler
	ListHandler              *itineraryhandler.ListHandler
	StatisticsHandler        *statistics.Handler
	RecentsHandler           *recents.Handler
	UserHandler              *userhandler.UserHandler
	InterestHandler          *interesthandler.InterestHandler
	TagsHandler              *tagshandler.TagsHandler
	PaymentHandler           paymentv1connect.PaymentServiceHandler
	FavoritesHandler         *favorites.Handler
	APIKeyHandler            *apikey.Handler
	ExportHandler            *export.Handler
	ShareHandler             *share.Handler
	TripHandler              *trip.Handler
	POIHandler               *poihandler.POIHandler
	CustomAuthHandler        *customauthhandler.CustomAuthHandler
	ReviewHandler            *reviewdomain.Handler
	EntitlementHandler       *entitlement.Handler
	RecommendationHandler    *recommendation.Handler
	PlaceIntelligenceHandler *placeintel.Handler
	MemoryHandler            *memory.Handler
	LocalContextHandler      *localcontext.Handler
	CompareHandler           *compare.Handler
	CityHandler              *cityhandler.CityHandler
	TravelHistoryHandler     *travelhistory.Handler

	PreferenceRecorder preference.Recorder
	PreferenceVectors  preference.VectorStore
	RetrievalAssembler *retrieval.Assembler
}

// InitDependencies initializes all application dependencies
func InitDependencies(cfg *config.Config, logger *slog.Logger) (*Dependencies, error) {
	deps := &Dependencies{
		Config: cfg,
		Logger: logger,
	}

	// Initialize database
	if err := deps.initDatabase(); err != nil {
		return nil, fmt.Errorf("failed to init database: %w", err)
	}

	// Initialize repositories
	if err := deps.initRepositories(); err != nil {
		return nil, fmt.Errorf("failed to init repositories: %w", err)
	}

	// Initialize handler
	if err := deps.initServices(); err != nil {
		return nil, fmt.Errorf("failed to init services: %w", err)
	}

	// Initialize service
	if err := deps.initHandlers(); err != nil {
		return nil, fmt.Errorf("failed to init handlers: %w", err)
	}

	logger.Info("all dependencies initialized successfully")

	return deps, nil
}

// initDatabase initializes the database connection and runs migrations
func (d *Dependencies) initDatabase() error {
	database, err := db.New(db.Config{
		DSN:             d.Config.Database.DSN(),
		MaxConns:        d.Config.Database.MaxConns,
		MinConns:        d.Config.Database.MinConns,
		MaxConnLifetime: d.Config.Database.MaxConnLifetime,
		MaxConnIdleTime: d.Config.Database.MaxConnIdleTime,
	}, d.Logger)
	if err != nil {
		return err
	}

	d.DB = database

	// Run migrations
	if err := d.DB.RunMigrations(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	d.Logger.Info("database connected and migrations completed successfully")
	return nil
}

// initRepositories initializes all repository layer dependencies
func (d *Dependencies) initRepositories() error {
	d.AuthRepo = repository.NewPostgresAuthRepository(d.DB.Pool)
	d.InterestRepo = interestrepo.NewRepositoryImpl(d.DB.Pool, d.Logger)
	d.TagRepo = tagrepo.NewRepositoryImpl(d.DB.Pool, d.Logger)
	d.ProfileRepo = profiles.NewPostgresUserRepo(d.DB.Pool, d.Logger)
	d.POIRepo = poirepo.NewRepository(d.DB.Pool, d.Logger)
	d.CityRepo = cityrepo.NewCityRepository(d.DB.Pool, d.Logger)
	// CityService has existed unregistered while the client already called
	// SearchCities; wire it so the city picker stops failing.
	d.CityHandler = cityhandler.NewCityHandler(cityrepo.NewCityService(d.CityRepo, d.Logger))
	d.ChatRepo = chatrepo.NewRepositoryImpl(d.DB.Pool, d.Logger)
	d.DiscoverRepo = discoverdomain.NewRepositoryImpl(d.DB.Pool, d.Logger)
	d.ListRepo = itinerarylist.NewRepository(d.DB.Pool, d.Logger)
	d.StatisticsRepo = statistics.NewRepository(d.Logger, d.DB.Pool)
	d.RecentsRepo = recents.NewRepository(d.DB.Pool, d.Logger)
	d.UserRepo = user.NewPostgresUserRepo(d.DB.Pool, d.Logger)
	d.UsageRepo = subscription.NewRepository(d.DB.Pool)
	d.PaymentRepo = payment.NewRepository(d.DB.Pool)
	d.FavoritesRepo = favorites.NewRepository(d.DB.Pool, d.Logger)
	d.APIKeyRepo = apikey.NewRepository(d.DB.Pool)
	d.ReviewRepo = reviewdomain.NewRepository(d.DB.Pool, d.Logger)
	d.ShareRepo = share.NewRepository(d.DB.Pool, d.Logger)
	d.TripRepo = trip.NewRepository(d.DB.Pool, d.Logger)
	d.TravelHistoryRepo = travelhistory.NewRepository(d.DB.Pool, d.Logger)
	d.PreferenceRecorder = preference.NewRecorder(d.DB.Pool, d.Logger)
	d.PreferenceVectors = preference.NewVectorStore(d.DB.Pool, d.Logger)

	d.Logger.Info("repositories initialized")
	return nil
}

// initServices initializes all service layer dependencies
func (d *Dependencies) initServices() error {
	jwtSecret := []byte(d.Config.Auth.JWTSecret)
	if len(jwtSecret) == 0 {
		return fmt.Errorf("jwt secret is required")
	}

	// Separate signing keys. Sharing one meant an access token also verified as
	// a refresh token, so anything that leaked a short-lived access token handed
	// over a 30-day session. Config falls back to the access secret outside
	// production and rejects the shared setup in it.
	refreshSecret := []byte(d.Config.Auth.JWTRefreshSecret)
	if len(refreshSecret) == 0 {
		refreshSecret = jwtSecret
	}
	if string(refreshSecret) == string(jwtSecret) {
		d.Logger.Warn("JWT_REFRESH_SECRET is unset or equal to JWT_SECRET; access tokens validate as refresh tokens. Set a distinct value before production.")
	}

	// TTLs come from config so JWT_ACCESS_TOKEN_TTL / JWT_REFRESH_TOKEN_TTL
	// actually take effect; they were previously hardcoded here while the env
	// vars sat unread in .env.
	accessTokenTTL := d.Config.Auth.AccessTokenTTL
	refreshTokenTTL := d.Config.Auth.RefreshTokenTTL

	d.TokenManager = service.NewTokenManager(jwtSecret, refreshSecret, accessTokenTTL, refreshTokenTTL)
	emailService := service.NewEmailService()
	d.AuthService = service.NewAuthService(
		d.AuthRepo,
		d.TokenManager,
		emailService,
		d.Logger,
		refreshTokenTTL,
	)

	// MFA is opt-in on the presence of MFA_SECRET_KEY. Without a key there is no
	// safe way to store TOTP secrets, so the feature stays off rather than
	// degrading to plaintext storage.
	if err := d.initMFA(); err != nil {
		return err
	}

	d.ListSvc = itinerarylist.NewServiceImpl(d.ListRepo, d.Logger, nil, nil, nil) // plans wired below after SubscriptionService
	d.ProfileSvc = profiles.NewUserProfilesService(d.ProfileRepo, d.InterestRepo, d.TagRepo, d.Logger)
	llmSem := concurrency.NewLLMSemaphore(d.Config.AI.MaxConcurrentCalls)
	appCache, err := cachestore.New(cachestore.Config{
		RedisURL:   d.Config.Cache.RedisURL,
		KeyPrefix:  d.Config.Cache.KeyPrefix,
		LLMTTL:     d.Config.Cache.LLMTTL,
		GeoTTL:     d.Config.Cache.GeoTTL,
		CleanupTTL: d.Config.Cache.CleanupTTL,
	}, d.Logger)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	d.AppCache = appCache
	poiSvc := poirepo.NewServiceImpl(d.POIRepo, nil, d.CityRepo, d.DiscoverRepo, d.Config.AI, llmSem, appCache, d.Logger)
	poiSvc.SetPreferenceVectors(d.PreferenceVectors)
	d.POISvc = poiSvc
	chatSvc, err := chatservice.NewLlmInteractiontService(
		d.InterestRepo,
		d.ProfileRepo,
		d.ProfileSvc,
		d.TagRepo,
		d.ChatRepo,
		d.CityRepo,
		d.POIRepo,
		d.POISvc,
		d.ListSvc,
		d.TripRepo,
		d.Logger,
		d.Config.AI,
		llmSem,
		appCache,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize chat service: %w", err)
	}
	chatSvc.SetPreferenceVectors(d.PreferenceVectors)
	// Grounded generation: retrieve real POI rows before prompting, then verify
	// the answer against them. Without this the chat path generates from the
	// city name and preference text alone.
	d.RetrievalAssembler = retrieval.NewAssembler(d.DB.Pool, d.Logger)
	chatSvc.SetRetrievalAssembler(d.RetrievalAssembler)
	d.ChatService = chatSvc
	d.DiscoverSvc = discoverdomain.NewServiceImpl(d.DiscoverRepo, d.Logger)
	d.StatisticsSvc = statistics.NewService(d.StatisticsRepo, d.Logger)
	d.RecentsSvc = recents.NewService(d.RecentsRepo, d.Logger)
	d.UserSvc = user.NewUserService(d.UserRepo, d.Logger)
	d.InterestSvc = interestrepo.NewService(d.InterestRepo, d.Logger)
	d.UserSvc = user.NewUserService(d.UserRepo, d.Logger)
	d.InterestSvc = interestrepo.NewService(d.InterestRepo, d.Logger)
	d.TagsSvc = tagrepo.NewtagsService(d.TagRepo, d.Logger)
	d.SubscriptionService = subscription.NewService(d.UsageRepo, d.Logger, d.Config.Auth.AdminEmail, subscription.Limits{
		FreeDaily: d.Config.Subscription.FreeDailyLLMLimit,
		ProDaily:  d.Config.Subscription.ProDailyLLMLimit,
	})
	// Freemium list/place caps need EffectivePlan — rebind with the live service.
	d.ListSvc = itinerarylist.NewServiceImpl(d.ListRepo, d.Logger, d.SubscriptionService, d.FavoritesRepo, d.PreferenceRecorder)
	d.APIKeyService = apikey.NewService(d.APIKeyRepo)
	d.PaymentService = payment.NewService(d.PaymentRepo, d.Logger, d.UsageRepo, d.SubscriptionService, payment.StripeConfig{
		APIKey:         d.Config.Stripe.APIKey,
		PriceIDMonthly: d.Config.Stripe.PriceIDMonthly,
		PriceIDAnnual:  d.Config.Stripe.PriceIDAnnual,
		FreeDailyLimit: d.Config.Subscription.FreeDailyLLMLimit,
	})

	// Custom auth (OAuth + phone). Both degrade gracefully when their env vars
	// are absent: OAuth registers no providers, phone reports disabled.
	d.OAuthService = customauthservice.NewOAuthService(customauthservice.LoadOAuthConfigFromEnv())
	d.PhoneService = customauthservice.NewPhoneService(customauthservice.LoadTwilioConfigFromEnv())
	d.ReviewSvc = reviewdomain.NewService(d.ReviewRepo, d.Logger)

	// Handlers
	d.PaymentHandler = payment.NewPaymentServiceHandler(d.PaymentService, d.UserRepo, d.Logger)

	d.Logger.Info("services initialized")
	return nil
}

// initMFA wires the MFA service when a secret key is configured.
//
// Absence of MFA_SECRET_KEY is a supported state, not an error: it leaves login
// exactly as it was. A key that is present but the wrong size IS an error —
// booting with a broken key would let users enrol into an MFA they can never
// complete.
func (d *Dependencies) initMFA() error {
	key := d.Config.Auth.MFASecretKey
	if key == "" {
		d.Logger.Warn("MFA_SECRET_KEY not set; two-factor authentication is disabled")
		return nil
	}

	cipher, err := mfa.NewCipher([]byte(key))
	if err != nil {
		return fmt.Errorf("MFA is misconfigured: %w", err)
	}

	d.MFAService = mfa.NewService(
		mfa.NewPostgresRepository(d.DB.Pool),
		cipher,
		mfa.ParsePolicy(d.Config.Auth.MFARequiredForRole),
		d.Logger,
	)

	// Login now challenges enrolled users before issuing any token.
	d.AuthService.WithMFA(d.MFAService)

	d.Logger.Info("two-factor authentication enabled",
		"required_for_roles", d.Config.Auth.MFARequiredForRole)
	return nil
}

// initHandlers initializes all handler dependencies
func (d *Dependencies) initHandlers() error {
	d.AuthHandler = handler.NewAuthHandler(d.AuthService, d.Logger)
	if d.MFAService != nil {
		d.AuthHandler.WithMFA(d.MFAService)
	}
	d.RecommendationHandler = recommendation.NewHandler(d.DB.Pool, d.Logger)
	d.ChatHandler = chathandler.NewChatHandler(d.ChatService, d.Logger, d.RecommendationHandler)
	d.ProfileHandler = profilehandler.NewProfileHandler(d.ProfileSvc)
	d.DiscoverHandler = discoverdomain.NewHandler(d.DiscoverSvc, d.Logger)
	d.ItineraryHandler = itineraryhandler.NewItineraryHandler(d.ListSvc, d.ChatService, d.Logger)
	d.ListHandler = itineraryhandler.NewListHandler(d.ListSvc, d.Logger)
	d.StatisticsHandler = statistics.NewHandler(d.StatisticsSvc, d.Logger)
	d.RecentsHandler = recents.NewHandler(d.RecentsSvc, d.Logger)
	// Memory: the learned profile, the evidence behind it, and the ability to
	// remove either. Forgetting rebuilds the derived vector and traits from the
	// signals that survive, so a deletion cannot leave the rest inconsistent.
	d.MemoryHandler = memory.NewHandler(
		memory.NewService(d.DB.Pool, preference.NewReranker(d.DB.Pool, d.PreferenceVectors, d.Logger).RecomputeUser),
		d.Logger,
	)

	d.UserHandler = userhandler.NewUserHandler(d.UserSvc)
	// The self-service export previously returned the profile alone, while the
	// account also held trips, lists, favorites, itineraries, travel history,
	// chat sessions and the learned taste profile.
	d.UserHandler.SetExporter(userdata.NewExporter(d.DB.Pool, d.Logger))
	d.InterestHandler = interesthandler.NewInterestHandler(d.InterestSvc)
	d.TagsHandler = tagshandler.NewTagsHandler(d.TagsSvc)
	d.FavoritesHandler = favorites.NewHandler(d.FavoritesRepo, d.Logger, d.SubscriptionService, d.PreferenceRecorder, d.ListRepo)
	d.APIKeyHandler = apikey.NewHandler(d.APIKeyService, d.Logger)
	d.ExportHandler = export.NewHandler(d.Logger)
	d.ShareHandler = share.NewHandler(d.Config.Server.BaseURL, d.ShareRepo)
	d.TripHandler = trip.NewHandler(d.TripRepo, d.Config.Server.BaseURL, d.PreferenceRecorder, d.SubscriptionService)
	d.TravelHistoryHandler = travelhistory.NewHandler(d.TravelHistoryRepo, d.Logger)

	// A confirmed visit becomes a travel-history row. Best-effort: the recorder
	// swallows its own failures so recording an event never fails because a
	// derived row could not be written.
	if d.RecommendationHandler != nil {
		d.RecommendationHandler = d.RecommendationHandler.WithTravelHistory(
			travelhistory.NewRecorder(d.TravelHistoryRepo, d.Logger))
	}
	// Give statistics the real visited-city count. Until this line, that field
	// returned hotels+restaurants and was marked "// Placeholder".
	if d.StatisticsHandler != nil {
		d.StatisticsHandler = d.StatisticsHandler.WithVisitedCities(d.TravelHistoryRepo)
	}
	// SuggestPacking is wired below, once the weather adapter exists — a packing
	// list is only worth generating if it knows the forecast.
	d.POIHandler = poihandler.NewPOIHandler(d.POISvc)
	d.CustomAuthHandler = customauthhandler.NewCustomAuthHandler(d.OAuthService, d.PhoneService, d.AuthService)
	d.ReviewHandler = reviewdomain.NewHandler(d.ReviewSvc, d.Logger)
	d.EntitlementHandler = entitlement.NewHandler(d.SubscriptionService, d.ListRepo, d.FavoritesRepo)
	d.PlaceIntelligenceHandler = placeintel.NewHandler(d.DB.Pool, d.Logger)

	// Local context (weather now; booking/transport stubbed). Real OpenWeather
	// when OPENWEATHER_API_KEY is set, else a labelled stub forecast.
	var weather localcontext.WeatherAdapter = localcontext.StubWeather{}
	weatherEst := true
	if weatherKey := os.Getenv("OPENWEATHER_API_KEY"); weatherKey != "" {
		weather = localcontext.NewOpenWeatherAdapter(weatherKey)
		weatherEst = false
	}
	// WithScoring lights up GetGoScore ("should I go this weekend?"). Without it
	// the handler still serves weather; with it the score can resolve a city by
	// name and factor in how much there is to do there.
	d.LocalContextHandler = localcontext.
		NewHandler(weather, weatherEst, d.Logger).
		WithScoring(d.CityRepo, d.POISvc)

	// Give the trip handler the same forecast source, so a packing list can be
	// derived from the trip's actual weather rather than generic advice.
	if d.TripHandler != nil {
		d.TripHandler = d.TripHandler.WithPacking(weather, weatherEst).WithLogger(d.Logger)
	}

	bookingDL := localcontext.NewBookingComDeepLinkFromEnv()
	diningDL := localcontext.NewOpenTableDeepLinkFromEnv()
	uberDL := localcontext.NewUberDeepLinkFromEnv()
	transport := localcontext.StubTransportWithDrive{
		Fallback: localcontext.StubTransport{},
		Uber:     uberDL,
	}
	compareSvc := compare.NewService(
		d.CityRepo,
		d.POISvc,
		weather,
		weatherEst,
		transport,
		bookingDL,
		diningDL,
		d.SubscriptionService,
		d.Logger,
	)
	d.CompareHandler = compare.NewHandler(compareSvc)
	d.Logger.Info("handlers initialized")
	return nil
}

// Cleanup closes all resources
func (d *Dependencies) Cleanup() {
	if closer, ok := d.ChatService.(interface{ Close() }); ok {
		closer.Close()
	}
	if d.AppCache != nil {
		if err := d.AppCache.Close(); err != nil {
			d.Logger.Warn("failed to close cache", slog.Any("error", err))
		}
	}
	if d.DB != nil {
		d.DB.Close()
	}
	d.Logger.Info("cleanup completed")
}
