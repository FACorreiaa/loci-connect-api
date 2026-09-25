package review

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a review does not exist.
var ErrNotFound = errors.New("review not found")

// ErrAlreadyExists is returned when the user has already reviewed the POI
// (reviews_user_poi_unique).
var ErrAlreadyExists = errors.New("you have already reviewed this place")

// ErrPOINotFound is returned when a review names a POI that does not exist.
var ErrPOINotFound = errors.New("poi not found")

// Postgres SQLSTATEs the repository maps to domain errors.
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

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

	// Enrichment (joined): reviewer + reviewed POI display info.
	ReviewerName   string
	ReviewerAvatar string
	ReviewerSince  time.Time // users.created_at
	POIName        string
}

// UpdateFields are the author-editable fields of a review. An update
// replaces all of them.
type UpdateFields struct {
	Rating    int
	Title     string
	Content   string
	Photos    []string
	VisitDate *time.Time
}

// Statistics summarises the published reviews of one POI.
type Statistics struct {
	POIID         uuid.UUID
	TotalReviews  int
	AverageRating float64
	// Distribution[i] is the number of (i+1)-star reviews.
	Distribution [5]int
	// LastReviewAt is the newest review's created_at; nil with no reviews.
	LastReviewAt *time.Time
}

// UserStatistics summarises one user's published reviews for My reviews.
type UserStatistics struct {
	TotalReviews         int
	AverageRatingGiven   float64
	HelpfulVotesReceived int
}

type Repository interface {
	Create(ctx context.Context, r *Review) error
	GetByID(ctx context.Context, id uuid.UUID) (*Review, error)
	ListByPOI(ctx context.Context, poiID uuid.UUID, limit, offset int) ([]*Review, int, error)
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Review, int, error)
	ListRecent(ctx context.Context, limit, offset int) ([]*Review, int, error)
	Update(ctx context.Context, reviewID, userID uuid.UUID, f UpdateFields) error
	Delete(ctx context.Context, reviewID, userID uuid.UUID) error
	SetHelpful(ctx context.Context, userID, reviewID uuid.UUID, isHelpful bool) (int, error)
	Statistics(ctx context.Context, poiID uuid.UUID) (*Statistics, error)
	UserStatistics(ctx context.Context, userID uuid.UUID) (*UserStatistics, error)
}

type repository struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewRepository(db *pgxpool.Pool, logger *slog.Logger) Repository {
	return &repository{db: db, logger: logger.With(slog.String("component", "review-repository"))}
}

// selectReview joins the reviewer (users) and the reviewed POI for display
// enrichment. Callers add a WHERE on the `r` alias.
const selectReview = `
	SELECT r.id, r.user_id, r.poi_id, r.rating, COALESCE(r.title, ''), r.content, r.image_urls,
	       r.visit_date, r.helpful, r.unhelpful, r.is_verified, r.is_published, r.created_at, r.updated_at,
	       COALESCE(NULLIF(u.username, ''), u.display_name, '') AS reviewer_name,
	       COALESCE(u.profile_image_url, '') AS reviewer_avatar,
	       u.created_at AS reviewer_since,
	       COALESCE(p.name, '') AS poi_name
	FROM reviews r
	JOIN users u ON u.id = r.user_id
	LEFT JOIN points_of_interest p ON p.id = r.poi_id`

func scanReview(row pgx.Row) (*Review, error) {
	var r Review
	err := row.Scan(&r.ID, &r.UserID, &r.POIID, &r.Rating, &r.Title, &r.Content, &r.Photos,
		&r.VisitDate, &r.Helpful, &r.Unhelpful, &r.IsVerified, &r.IsPublished, &r.CreatedAt, &r.UpdatedAt,
		&r.ReviewerName, &r.ReviewerAvatar, &r.ReviewerSince, &r.POIName)
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
	err := repo.db.QueryRow(ctx, query,
		r.ID, r.UserID, r.POIID, r.Rating, r.Title, r.Content, r.Photos, r.VisitDate, r.IsVerified).
		Scan(&r.CreatedAt, &r.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == "reviews_user_poi_unique":
			return ErrAlreadyExists
		case pgErr.Code == pgForeignKeyViolation && pgErr.ConstraintName == "reviews_poi_id_fkey":
			return ErrPOINotFound
		}
	}
	return err
}

// Update replaces the editable fields of the caller's own review. A review
// that does not exist or belongs to someone else is ErrNotFound, so callers
// cannot probe other users' review ids.
func (repo *repository) Update(ctx context.Context, reviewID, userID uuid.UUID, f UpdateFields) error {
	tag, err := repo.db.Exec(ctx, `
		UPDATE reviews
		SET rating = $3, title = $4, content = $5, image_urls = $6, visit_date = $7
		WHERE id = $1 AND user_id = $2`,
		reviewID, userID, f.Rating, f.Title, f.Content, f.Photos, f.VisitDate)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Statistics counts a POI's published reviews by star. A POI with no reviews
// (or one that does not exist) yields zeros.
func (repo *repository) Statistics(ctx context.Context, poiID uuid.UUID) (*Statistics, error) {
	st := &Statistics{POIID: poiID}
	err := repo.db.QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(AVG(rating), 0)::float8,
		       COUNT(*) FILTER (WHERE rating = 1),
		       COUNT(*) FILTER (WHERE rating = 2),
		       COUNT(*) FILTER (WHERE rating = 3),
		       COUNT(*) FILTER (WHERE rating = 4),
		       COUNT(*) FILTER (WHERE rating = 5),
		       MAX(created_at)
		FROM reviews
		WHERE poi_id = $1 AND is_published = true`, poiID).
		Scan(&st.TotalReviews, &st.AverageRating,
			&st.Distribution[0], &st.Distribution[1], &st.Distribution[2], &st.Distribution[3], &st.Distribution[4],
			&st.LastReviewAt)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// UserStatistics totals a user's published reviews. A user with none yields
// zeros, never an error.
func (repo *repository) UserStatistics(ctx context.Context, userID uuid.UUID) (*UserStatistics, error) {
	st := &UserStatistics{}
	err := repo.db.QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(AVG(rating), 0)::float8,
		       COALESCE(SUM(helpful), 0)
		FROM reviews
		WHERE user_id = $1 AND is_published = true`, userID).
		Scan(&st.TotalReviews, &st.AverageRatingGiven, &st.HelpfulVotesReceived)
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (repo *repository) GetByID(ctx context.Context, id uuid.UUID) (*Review, error) {
	r, err := scanReview(repo.db.QueryRow(ctx, selectReview+` WHERE r.id = $1`, id))
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
	if err := repo.db.QueryRow(ctx, `SELECT COUNT(*) FROM reviews r WHERE `+where+` AND r.is_published = true`, arg).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := repo.db.Query(ctx,
		selectReview+` WHERE `+where+` AND r.is_published = true ORDER BY r.created_at DESC LIMIT $2 OFFSET $3`,
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
	return repo.listBy(ctx, "r.poi_id = $1", poiID, limit, offset)
}

func (repo *repository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Review, int, error) {
	return repo.listBy(ctx, "r.user_id = $1", userID, limit, offset)
}

// ListRecent returns the most recent published reviews across all content.
func (repo *repository) ListRecent(ctx context.Context, limit, offset int) ([]*Review, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var total int
	if err := repo.db.QueryRow(ctx, `SELECT COUNT(*) FROM reviews r WHERE r.is_published = true`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := repo.db.Query(ctx,
		selectReview+` WHERE r.is_published = true ORDER BY r.created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset)
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

// SetHelpful records (isHelpful) or withdraws (!isHelpful) the caller's
// helpful vote, recomputes the review's counters, and returns the new
// helpful count. Vote and recount run in one transaction with the review
// row locked, so concurrent votes cannot leave a stale count.
func (repo *repository) SetHelpful(ctx context.Context, userID, reviewID uuid.UUID, isHelpful bool) (int, error) {
	var helpful int
	err := pgx.BeginFunc(ctx, repo.db, func(tx pgx.Tx) error {
		var locked uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM reviews WHERE id = $1 FOR UPDATE`, reviewID).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if isHelpful {
			_, err = tx.Exec(ctx, `
				INSERT INTO review_helpfuls (user_id, review_id, is_helpful, created_at)
				VALUES ($1, $2, true, NOW())
				ON CONFLICT (user_id, review_id) DO UPDATE SET is_helpful = true`,
				userID, reviewID)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM review_helpfuls WHERE user_id = $1 AND review_id = $2`,
				userID, reviewID)
		}
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			UPDATE reviews SET
				helpful = (SELECT COUNT(*) FROM review_helpfuls WHERE review_id = $1 AND is_helpful = true),
				unhelpful = (SELECT COUNT(*) FROM review_helpfuls WHERE review_id = $1 AND is_helpful = false)
			WHERE id = $1
			RETURNING helpful`, reviewID).Scan(&helpful)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation &&
		pgErr.ConstraintName == "review_helpfuls_review_id_fkey" {
		// Belt and braces: the row lock above should already rule this out.
		return 0, ErrNotFound
	}
	return helpful, err
}
