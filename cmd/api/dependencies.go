package api

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/repository"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	chathandler "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/handler"
	chatrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/repository"
	chatservice "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	discoverdomain "github.com/FACorreiaa/loci-connect-api/internal/domain/discover"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/favorites"
	interestrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/interests"
	interesthandler "github.com/FACorreiaa/loci-connect-api/internal/domain/interests/handler"
	itinerarylist "github.com/FACorreiaa/loci-connect-api/internal/domain/list"
	itineraryhandler "github.com/FACorreiaa/loci-connect-api/internal/domain/list/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/payment"
	poirepo "github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	profiles "github.com/FACorreiaa/loci-connect-api/internal/domain/profiles"
	profilehandler "github.com/FACorreiaa/loci-connect-api/internal/domain/profiles/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/recents"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/statistics"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	tagrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/tags"
	tagshandler "github.com/FACorreiaa/loci-connect-api/internal/domain/tags/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/user"
	userhandler "github.com/FACorreiaa/loci-connect-api/internal/domain/user/handler"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/payment/v1/paymentv1connect"
)

// Dependencies holds all application dependencies
type Dependencies struct {
	Config *config.Config
	DB     *db.DB
	Logger *slog.Logger

	// Repositories
	AuthRepo       repository.AuthRepository
	InterestRepo   interestrepo.Repository
	TagRepo        tagrepo.Repository
	ProfileRepo    profiles.Repository
	POIRepo        poirepo.Repository
	CityRepo       cityrepo.Repository
	ChatRepo       chatrepo.Repository
	DiscoverRepo   discoverdomain.Repository
	ListRepo       itinerarylist.Repository
	StatisticsRepo statistics.Repository
	RecentsRepo    recents.Repository
	UserRepo       user.UserRepo
	UsageRepo      subscription.Repository
	PaymentRepo    payment.Repository
	FavoritesRepo  favorites.Repository

	// Services
	TokenManager        service.TokenManager
	AuthService         *service.AuthService
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
	PaymentService      payment.Service

	// Handlers
	AuthHandler       *handler.AuthHandler
	ChatHandler       *chathandler.ChatHandler
	ProfileHandler    *profilehandler.ProfileHandler
	DiscoverHandler   *discoverdomain.Handler
	ItineraryHandler  *itineraryhandler.ItineraryHandler
	StatisticsHandler *statistics.Handler
	RecentsHandler    *recents.Handler
	UserHandler       *userhandler.UserHandler
	InterestHandler   *interesthandler.InterestHandler
	TagsHandler       *tagshandler.TagsHandler
	PaymentHandler    paymentv1connect.PaymentServiceHandler
	FavoritesHandler  *favorites.Handler
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
		MaxConns:        25,
		MinConns:        5,
		MaxConnLifetime: 5 * time.Minute,
		MaxConnIdleTime: 10 * time.Minute,
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
	d.ChatRepo = chatrepo.NewRepositoryImpl(d.DB.Pool, d.Logger)
	d.DiscoverRepo = discoverdomain.NewRepositoryImpl(d.DB.Pool, d.Logger)
	d.ListRepo = itinerarylist.NewRepository(d.DB.Pool, d.Logger)
	d.StatisticsRepo = statistics.NewRepository(d.Logger, d.DB.Pool)
	d.RecentsRepo = recents.NewRepository(d.DB.Pool, d.Logger)
	d.UserRepo = user.NewPostgresUserRepo(d.DB.Pool, d.Logger)
	d.UsageRepo = subscription.NewRepository(d.DB.Pool)
	d.PaymentRepo = payment.NewRepository(d.DB.Pool)
	d.FavoritesRepo = favorites.NewRepository(d.DB.Pool, d.Logger)

	d.Logger.Info("repositories initialized")
	return nil
}

// initServices initializes all service layer dependencies
func (d *Dependencies) initServices() error {
	jwtSecret := []byte(d.Config.Auth.JWTSecret)
	if len(jwtSecret) == 0 {
		return fmt.Errorf("jwt secret is required")
	}

	accessTokenTTL := 1 * time.Hour // Increased from 15 minutes for better UX
	refreshTokenTTL := 30 * 24 * time.Hour

	d.TokenManager = service.NewTokenManager(jwtSecret, jwtSecret, accessTokenTTL, refreshTokenTTL)
	emailService := service.NewEmailService()
	d.AuthService = service.NewAuthService(
		d.AuthRepo,
		d.TokenManager,
		emailService,
		d.Logger,
		refreshTokenTTL,
	)

	d.ListSvc = itinerarylist.NewServiceImpl(d.ListRepo, d.Logger)
	d.ProfileSvc = profiles.NewUserProfilesService(d.ProfileRepo, d.InterestRepo, d.TagRepo, d.Logger)
	d.POISvc = poirepo.NewServiceImpl(d.POIRepo, nil, d.CityRepo, d.DiscoverRepo, d.Logger)
	d.ChatService = chatservice.NewLlmInteractiontService(
		d.InterestRepo,
		d.ProfileRepo,
		d.ProfileSvc,
		d.TagRepo,
		d.ChatRepo,
		d.CityRepo,
		d.POIRepo,
		d.POISvc,
		d.ListSvc,
		d.Logger,
		d.Config.Gemini.APIKey,
		d.Config.Gemini.Model,
	)
	d.DiscoverSvc = discoverdomain.NewServiceImpl(d.DiscoverRepo, d.Logger)
	d.StatisticsSvc = statistics.NewService(d.StatisticsRepo, d.Logger)
	d.RecentsSvc = recents.NewService(d.RecentsRepo, d.Logger)
	d.UserSvc = user.NewUserService(d.UserRepo, d.Logger)
	d.InterestSvc = interestrepo.NewService(d.InterestRepo, d.Logger)
	d.UserSvc = user.NewUserService(d.UserRepo, d.Logger)
	d.InterestSvc = interestrepo.NewService(d.InterestRepo, d.Logger)
	d.TagsSvc = tagrepo.NewtagsService(d.TagRepo, d.Logger)
	d.SubscriptionService = subscription.NewService(d.UsageRepo, d.Logger, d.Config.Auth.AdminEmail)
	// Using STRIPE_API_KEY from environment
	d.PaymentService = payment.NewService(d.PaymentRepo, d.Logger, os.Getenv("STRIPE_API_KEY"))

	// Handlers
	d.PaymentHandler = payment.NewPaymentServiceHandler(d.PaymentService, d.UserRepo, d.Logger)

	d.Logger.Info("services initialized")
	return nil

	// Needs imports and struct fields.
	// Since replace_file_content is single block, I will use multi_replace for this file.

}

// initHandlers initializes all handler dependencies
func (d *Dependencies) initHandlers() error {
	d.AuthHandler = handler.NewAuthHandler(d.AuthService)
	d.ChatHandler = chathandler.NewChatHandler(d.ChatService, d.Logger)
	d.ProfileHandler = profilehandler.NewProfileHandler(d.ProfileSvc)
	d.DiscoverHandler = discoverdomain.NewHandler(d.DiscoverSvc, d.Logger)
	d.ItineraryHandler = itineraryhandler.NewItineraryHandler(d.ListSvc, d.ChatService, d.Logger)
	d.StatisticsHandler = statistics.NewHandler(d.StatisticsSvc, d.Logger)
	d.RecentsHandler = recents.NewHandler(d.RecentsSvc, d.Logger)
	d.UserHandler = userhandler.NewUserHandler(d.UserSvc)
	d.InterestHandler = interesthandler.NewInterestHandler(d.InterestSvc)
	d.TagsHandler = tagshandler.NewTagsHandler(d.TagsSvc)
	d.FavoritesHandler = favorites.NewHandler(d.FavoritesRepo, d.Logger)
	d.Logger.Info("handlers initialized")
	return nil
}

// Cleanup closes all resources
func (d *Dependencies) Cleanup() {
	if d.DB != nil {
		d.DB.Close()
	}
	d.Logger.Info("cleanup completed")
}
