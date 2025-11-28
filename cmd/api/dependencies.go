package api

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/handler"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/repository"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

// Dependencies holds all application dependencies
type Dependencies struct {
	Config *config.Config
	DB     *db.DB
	Logger *slog.Logger

	sqlDB *sql.DB

	// Repositories
	AuthRepo repository.AuthRepository

	// Services
	TokenManager service.TokenManager
	AuthService  *service.AuthService

	// Handlers
	AuthHandler *handler.AuthHandler
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
	sqlDB, err := sql.Open("pgx", d.Config.Database.DSN())
	if err != nil {
		return fmt.Errorf("failed to open sql DB: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping sql DB: %w", err)
	}

	d.sqlDB = sqlDB
	d.AuthRepo = repository.NewPostgresAuthRepository(sqlDB)

	d.Logger.Info("repositories initialized")
	return nil
}

// initServices initializes all service layer dependencies
func (d *Dependencies) initServices() error {
	jwtSecret := []byte(d.Config.Auth.JWTSecret)
	if len(jwtSecret) == 0 {
		return fmt.Errorf("jwt secret is required")
	}

	accessTokenTTL := 15 * time.Minute
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

	d.Logger.Info("services initialized")
	return nil
}

// initHandlers initializes all handler dependencies
func (d *Dependencies) initHandlers() error {
	d.AuthHandler = handler.NewAuthHandler(d.AuthService)
	d.Logger.Info("handlers initialized")
	return nil
}

// Cleanup closes all resources
func (d *Dependencies) Cleanup() {
	if d.DB != nil {
		d.DB.Close()
	}
	if d.sqlDB != nil {
		d.sqlDB.Close()
	}
	d.Logger.Info("cleanup completed")
}
