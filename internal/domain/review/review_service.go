package review

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidReview signals a validation failure on review input.
var ErrInvalidReview = errors.New("invalid review")

// CreateReviewInput is the validated input for creating a review.
type CreateReviewInput struct {
	UserID    uuid.UUID
	POIID     uuid.UUID
	Rating    int
	Title     string
	Content   string
	Photos    []string
	VisitDate *time.Time
}

// UpdateReviewInput is the input for replacing the caller's own review.
type UpdateReviewInput struct {
	ReviewID  uuid.UUID
	UserID    uuid.UUID
	Rating    int
	Title     string
	Content   string
	Photos    []string
	VisitDate *time.Time
}

type Service interface {
	CreateReview(ctx context.Context, in CreateReviewInput) (*Review, error)
	GetReview(ctx context.Context, id uuid.UUID) (*Review, error)
	ListPOIReviews(ctx context.Context, poiID uuid.UUID, limit, offset int) ([]*Review, int, error)
	ListUserReviews(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Review, int, error)
	ListRecentReviews(ctx context.Context, limit, offset int) ([]*Review, int, error)
	UpdateReview(ctx context.Context, in UpdateReviewInput) (*Review, error)
	DeleteReview(ctx context.Context, reviewID, userID uuid.UUID) error
	LikeReview(ctx context.Context, userID, reviewID uuid.UUID, isLike bool) (int, error)
	GetStatistics(ctx context.Context, poiID uuid.UUID) (*Statistics, error)
}

type service struct {
	repo   Repository
	logger *slog.Logger
}

func NewService(repo Repository, logger *slog.Logger) Service {
	return &service{repo: repo, logger: logger.With(slog.String("component", "review-service"))}
}

func validateReviewBody(rating int, content string) error {
	if rating < 1 || rating > 5 {
		return errors.Join(ErrInvalidReview, errors.New("rating must be between 1 and 5"))
	}
	if content == "" {
		return errors.Join(ErrInvalidReview, errors.New("content is required"))
	}
	return nil
}

func (s *service) CreateReview(ctx context.Context, in CreateReviewInput) (*Review, error) {
	if err := validateReviewBody(in.Rating, in.Content); err != nil {
		return nil, err
	}
	r := &Review{
		UserID:    in.UserID,
		POIID:     in.POIID,
		Rating:    in.Rating,
		Title:     in.Title,
		Content:   in.Content,
		Photos:    in.Photos,
		VisitDate: in.VisitDate,
	}
	if err := s.repo.Create(ctx, r); err != nil {
		return nil, err
	}
	// Re-fetch so the returned review carries the joined reviewer/POI display info.
	return s.repo.GetByID(ctx, r.ID)
}

func (s *service) GetReview(ctx context.Context, id uuid.UUID) (*Review, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *service) ListPOIReviews(ctx context.Context, poiID uuid.UUID, limit, offset int) ([]*Review, int, error) {
	return s.repo.ListByPOI(ctx, poiID, limit, offset)
}

func (s *service) ListUserReviews(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Review, int, error) {
	return s.repo.ListByUser(ctx, userID, limit, offset)
}

func (s *service) ListRecentReviews(ctx context.Context, limit, offset int) ([]*Review, int, error) {
	return s.repo.ListRecent(ctx, limit, offset)
}

func (s *service) DeleteReview(ctx context.Context, reviewID, userID uuid.UUID) error {
	return s.repo.Delete(ctx, reviewID, userID)
}

func (s *service) LikeReview(ctx context.Context, userID, reviewID uuid.UUID, isLike bool) (int, error) {
	return s.repo.SetHelpful(ctx, userID, reviewID, isLike)
}

func (s *service) UpdateReview(ctx context.Context, in UpdateReviewInput) (*Review, error) {
	if err := validateReviewBody(in.Rating, in.Content); err != nil {
		return nil, err
	}
	err := s.repo.Update(ctx, in.ReviewID, in.UserID, UpdateFields{
		Rating:    in.Rating,
		Title:     in.Title,
		Content:   in.Content,
		Photos:    in.Photos,
		VisitDate: in.VisitDate,
	})
	if err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, in.ReviewID)
}

func (s *service) GetStatistics(ctx context.Context, poiID uuid.UUID) (*Statistics, error) {
	return s.repo.Statistics(ctx, poiID)
}
