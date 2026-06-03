package review

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a review does not exist.
var ErrNotFound = errors.New("review not found")

// Review is the domain model mirroring the reviews table (POI-centric).
type Review struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	POIID       uuid.UUID
	Rating      int
	Title       string
	Content     string
	Photos      []string
	VisitDate   *time.Time
	Helpful     int
	Unhelpful   int
	IsVerified  bool
	IsPublished bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Repository interface {
	Create(ctx context.Context, r *Review) error
	GetByID(ctx context.Context, id uuid.UUID) (*Review, error)
	ListByPOI(ctx context.Context, poiID uuid.UUID, limit, offset int) ([]*Review, int, error)
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Review, int, error)
	Delete(ctx context.Context, reviewID, userID uuid.UUID) error
	SetHelpful(ctx context.Context, userID, reviewID uuid.UUID, isHelpful bool) (int, error)
}

type repository struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewRepository(db *pgxpool.Pool, logger *slog.Logger) Repository {
	return &repository{db: db, logger: logger.With(slog.String("component", "review-repository"))}
}

const reviewCols = `id, user_id, poi_id, rating, title, content, image_urls,
	visit_date, helpful, unhelpful, is_verified, is_published, created_at, updated_at`

func scanReview(row pgx.Row) (*Review, error) {
	var r Review
	err := row.Scan(&r.ID, &r.UserID, &r.POIID, &r.Rating, &r.Title, &r.Content, &r.Photos,
		&r.VisitDate, &r.Helpful, &r.Unhelpful, &r.IsVerified, &r.IsPublished, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (repo *repository) Create(ctx context.Context, r *Review) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	query := `
		INSERT INTO reviews (id, user_id, poi_id, rating, title, content, image_urls, visit_date, is_verified, is_published, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, NOW(), NOW())
		RETURNING created_at, updated_at`
	return repo.db.QueryRow(ctx, query,
		r.ID, r.UserID, r.POIID, r.Rating, r.Title, r.Content, r.Photos, r.VisitDate, r.IsVerified).
		Scan(&r.CreatedAt, &r.UpdatedAt)
}

func (repo *repository) GetByID(ctx context.Context, id uuid.UUID) (*Review, error) {
	r, err := scanReview(repo.db.QueryRow(ctx, `SELECT `+reviewCols+` FROM reviews WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (repo *repository) listBy(ctx context.Context, where string, arg uuid.UUID, limit, offset int) ([]*Review, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var total int
	if err := repo.db.QueryRow(ctx, `SELECT COUNT(*) FROM reviews WHERE `+where+` AND is_published = true`, arg).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := repo.db.Query(ctx,
		`SELECT `+reviewCols+` FROM reviews WHERE `+where+` AND is_published = true ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		arg, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (repo *repository) ListByPOI(ctx context.Context, poiID uuid.UUID, limit, offset int) ([]*Review, int, error) {
	return repo.listBy(ctx, "poi_id = $1", poiID, limit, offset)
}

func (repo *repository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Review, int, error) {
	return repo.listBy(ctx, "user_id = $1", userID, limit, offset)
}

func (repo *repository) Delete(ctx context.Context, reviewID, userID uuid.UUID) error {
	tag, err := repo.db.Exec(ctx, `DELETE FROM reviews WHERE id = $1 AND user_id = $2`, reviewID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHelpful upserts the caller's helpful/unhelpful vote, recomputes the
// counters on the review, and returns the new helpful count.
func (repo *repository) SetHelpful(ctx context.Context, userID, reviewID uuid.UUID, isHelpful bool) (int, error) {
	_, err := repo.db.Exec(ctx, `
		INSERT INTO review_helpfuls (user_id, review_id, is_helpful, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, review_id) DO UPDATE SET is_helpful = EXCLUDED.is_helpful`,
		userID, reviewID, isHelpful)
	if err != nil {
		return 0, err
	}
	var helpful int
	err = repo.db.QueryRow(ctx, `
		UPDATE reviews SET
			helpful = (SELECT COUNT(*) FROM review_helpfuls WHERE review_id = $1 AND is_helpful = true),
			unhelpful = (SELECT COUNT(*) FROM review_helpfuls WHERE review_id = $1 AND is_helpful = false),
			updated_at = NOW()
		WHERE id = $1
		RETURNING helpful`, reviewID).Scan(&helpful)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return helpful, err
}
