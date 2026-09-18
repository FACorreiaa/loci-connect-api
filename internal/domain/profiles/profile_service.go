package profiles

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/interests"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tags"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Ensure implementation satisfies the interface
var _ Service = (*ServiceImpl)(nil)

// profilessService defines the business logic contract for user operations.
type Service interface {
	// GetSearchProfiles User  Profiles
	GetSearchProfiles(ctx context.Context, userID uuid.UUID) ([]locitypes.UserPreferenceProfileResponse, error)
	GetSearchProfile(ctx context.Context, userID, profileID uuid.UUID) (*locitypes.UserPreferenceProfileResponse, error)
	GetDefaultSearchProfile(ctx context.Context, userID uuid.UUID) (*locitypes.UserPreferenceProfileResponse, error)
	CreateSearchProfile(ctx context.Context, userID uuid.UUID, params locitypes.CreateUserPreferenceProfileParams) (*locitypes.UserPreferenceProfileResponse, error)
	UpdateSearchProfile(ctx context.Context, userID, profileID uuid.UUID, params locitypes.UpdateSearchProfileParams) error
	DeleteSearchProfile(ctx context.Context, userID, profileID uuid.UUID) error
	SetDefaultSearchProfile(ctx context.Context, userID, profileID uuid.UUID) error

	// Domain preferences are now handled in the main UpdateSearchProfile method
}

// ServiceImpl provides the implementation for UserService.
type ServiceImpl struct {
	logger   *slog.Logger
	prefRepo Repository
	intRepo  interests.Repository
	tagRepo  tags.Repository
}

func NewUserProfilesService(prefRepo Repository, intRepo interests.Repository, tagRepo tags.Repository, logger *slog.Logger) *ServiceImpl {
	return &ServiceImpl{
		prefRepo: prefRepo,
		intRepo:  intRepo,
		tagRepo:  tagRepo,
		logger:   logger,
	}
}

// GetSearchProfiles retrieves all preference profiles for a user.
func (s *ServiceImpl) GetSearchProfiles(ctx context.Context, userID uuid.UUID) ([]locitypes.UserPreferenceProfileResponse, error) {
	ctx, span := otel.Tracer("UserService").Start(ctx, "GetSearchProfiles", trace.WithAttributes(
		attribute.String("user.id", userID.String()),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "GetSearchProfiles"), slog.String("userID", userID.String()))
	l.DebugContext(ctx, "Fetching user preference profiles")

	profiles, err := s.prefRepo.GetSearchProfiles(ctx, userID)
	if err != nil {
		l.ErrorContext(ctx, "Failed to fetch user preference profiles", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to fetch user preference profiles")
		return nil, fmt.Errorf("error fetching user preference profiles: %w", err)
	}

	l.InfoContext(ctx, "User preference profiles fetched successfully", slog.Int("count", len(profiles)))
	span.SetStatus(codes.Ok, "User preference profiles fetched successfully")
	return profiles, nil
}

// GetSearchProfile retrieves a specific preference profile by ID.
func (s *ServiceImpl) GetSearchProfile(ctx context.Context, userID, profileID uuid.UUID) (*locitypes.UserPreferenceProfileResponse, error) {
	ctx, span := otel.Tracer("UserService").Start(ctx, "GetSearchProfile", trace.WithAttributes(
		attribute.String("profile.id", profileID.String()),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "GetSearchProfile"), slog.String("profileID", profileID.String()))
	l.DebugContext(ctx, "Fetching user preference profile")

	profile, err := s.prefRepo.GetSearchProfile(ctx, userID, profileID)
	if err != nil {
		l.ErrorContext(ctx, "Failed to fetch user preference profile", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to fetch user preference profile")
		return nil, fmt.Errorf("error fetching user preference profile: %w", err)
	}

	l.InfoContext(ctx, "User preference profile fetched successfully")
	span.SetStatus(codes.Ok, "User preference profile fetched successfully")
	return profile, nil
}

// GetDefaultSearchProfile retrieves the default preference profile for a user.
func (s *ServiceImpl) GetDefaultSearchProfile(ctx context.Context, userID uuid.UUID) (*locitypes.UserPreferenceProfileResponse, error) {
	ctx, span := otel.Tracer("UserService").Start(ctx, "GetDefaultSearchProfile", trace.WithAttributes(
		attribute.String("user.id", userID.String()),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "GetDefaultSearchProfile"), slog.String("userID", userID.String()))
	l.DebugContext(ctx, "Fetching default user preference profile")

	profile, err := s.prefRepo.GetDefaultSearchProfile(ctx, userID)
	if err != nil {
		l.ErrorContext(ctx, "Failed to fetch default user preference profile", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to fetch default user preference profile")
		return nil, fmt.Errorf("error fetching default user preference profile: %w", err)
	}

	l.InfoContext(ctx, "Default user preference profile fetched successfully")
	span.SetStatus(codes.Ok, "Default user preference profile fetched successfully")
	return profile, nil
}

func (s *ServiceImpl) CreateSearchProfile(ctx context.Context, userID uuid.UUID, p locitypes.CreateUserPreferenceProfileParams) (*locitypes.UserPreferenceProfileResponse, error) {
	ctx, span := otel.Tracer("ProfilesService").Start(ctx, "CreateSearchProfile")
	defer span.End()

	// If this is the first profile for the user, set it as default
	profiles, err := s.prefRepo.GetSearchProfiles(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("error checking existing profiles: %w", err)
	}
	if len(profiles) == 0 {
		defaultValue := true
		p.IsDefault = &defaultValue
	}

	return s.prefRepo.CreateSearchProfile(ctx, userID, p)
}

// DeleteSearchProfile deletes a preference profile.
func (s *ServiceImpl) DeleteSearchProfile(ctx context.Context, userID, profileID uuid.UUID) error {
	ctx, span := otel.Tracer("UserService").Start(ctx, "DeleteSearchProfile", trace.WithAttributes(
		attribute.String("profile.id", profileID.String()),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "DeleteSearchProfile"), slog.String("profileID", profileID.String()))
	l.DebugContext(ctx, "Deleting user preference profile")

	err := s.prefRepo.DeleteSearchProfile(ctx, userID, profileID)
	if err != nil {
		l.ErrorContext(ctx, "Failed to delete user preference profile", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to delete user preference profile")
		return fmt.Errorf("error deleting user preference profile: %w", err)
	}

	l.InfoContext(ctx, "User preference profile deleted successfully")
	span.SetStatus(codes.Ok, "User preference profile deleted successfully")
	return nil
}

// SetDefaultSearchProfile sets a profile as the default for a user.
func (s *ServiceImpl) SetDefaultSearchProfile(ctx context.Context, userID, profileID uuid.UUID) error {
	ctx, span := otel.Tracer("UserService").Start(ctx, "SetDefaultSearchProfile", trace.WithAttributes(
		attribute.String("profile.id", profileID.String()),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "SetDefaultSearchProfile"), slog.String("profileID", profileID.String()))
	l.DebugContext(ctx, "Setting profile as default")

	err := s.prefRepo.SetDefaultSearchProfile(ctx, userID, profileID)
	if err != nil {
		l.ErrorContext(ctx, "Failed to set default user preference profile", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to set default user preference profile")
		return fmt.Errorf("error setting default user preference profile: %w", err)
	}

	l.InfoContext(ctx, "User preference profile set as default successfully")
	span.SetStatus(codes.Ok, "User preference profile set as default successfully")
	return nil
}

// UpdateSearchProfile implements profilessService.
func (s *ServiceImpl) UpdateSearchProfile(ctx context.Context, userID, profileID uuid.UUID, params locitypes.UpdateSearchProfileParams) error {
	ctx, span := otel.Tracer("UserService").Start(ctx, "UpdateSearchProfile", trace.WithAttributes(
		attribute.String("profile.id", profileID.String()),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "SetDefaultSearchProfile"), slog.String("profileID", profileID.String()))
	l.DebugContext(ctx, "Setting profile as default")

	if err := s.prefRepo.UpdateSearchProfile(ctx, userID, profileID, params); err != nil {
		l.ErrorContext(ctx, "Failed to set default user preference profile", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to set default user preference profile")
		return fmt.Errorf("error setting default user preference profile: %w", err)
	}

	l.InfoContext(ctx, "User search profile updated successfully")
	span.SetStatus(codes.Ok, "User preference profile updated successfully")
	return nil
}
