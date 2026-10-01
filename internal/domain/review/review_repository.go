package review

import (
	"context"
	"errors"
	"fmt"
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

// ErrReportOwnReview is returned when a user reports their own review. It
// wraps ErrOwnReview, so it maps to the same PermissionDenied.
var ErrReportOwnReview = fmt.Errorf("cannot report your own review: %w", ErrOwnReview)

// ReportReasons are the reasons ReportReview accepts; the review_reports
// CHECK constraint holds the same list.
var ReportReasons = []string{"spam", "inappropriate", "fake", "offensive", "other"}

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
	// ReportCount is the number of distinct people who reported the review.
	ReportCount int
	// Hidden is true when the review is out of public reads: AutoHideReports
	// or more open reports, or removed by a moderator. Only its author (and
	// the moderation queue) is ever handed a hidden review.
	Hidden bool
	// VotedByMe is per viewer, not stored on the row: the service fills it
	// for the authenticated caller (see VotedBy).
	VotedByMe bool

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
	// Distribution[i] is the number of (i+1)-star reviews the user gave.
	Distribution [5]int
}

// AutoHideReports is how many distinct people must report a review, since a
// moderator last looked at it, before it drops out of public reads.
const AutoHideReports = 3

// ModerationAction is a moderator's decision on a reported review.
type ModerationAction string

const (
	// ModerationKeep closes the open reports; the review stays (or becomes
	// again) public.
	ModerationKeep ModerationAction = "kept"
	// ModerationRemove takes the review out of every public read.
	ModerationRemove ModerationAction = "removed"
)

// ReportedReview is one entry of the moderation queue: a review with open
// reports and what the reporters said.
type ReportedReview struct {
	Review *Review
	// OpenReports is the number of distinct people with an open report.
	OpenReports int
	// Reasons counts the open reports by reason.
	Reasons map[string]int
	// Details are the reporters' non-empty free-text details, newest first.
	Details        []string
	LastReportedAt time.Time
}

type Repository interface {
	Create(ctx context.Context, r *Review) error
	GetByID(ctx context.Context, id uuid.UUID) (*Review, error)
	// The list reads leave hidden reviews out, except viewer's own: an author
	// keeps seeing a review the public no longer does. uuid.Nil (anonymous)
	// sees only public reviews.
	ListByPOI(ctx context.Context, poiID, viewer uuid.UUID, limit, offset int) ([]*Review, int, error)
	ListByUser(ctx context.Context, userID, viewer uuid.UUID, limit, offset int) ([]*Review, int, error)
	ListRecent(ctx context.Context, viewer uuid.UUID, limit, offset int) ([]*Review, int, error)
	Update(ctx context.Context, reviewID, userID uuid.UUID, f UpdateFields) error
	Delete(ctx context.Context, reviewID, userID uuid.UUID) error
	SetHelpful(ctx context.Context, userID, reviewID uuid.UUID, isHelpful bool) (int, error)
	// Statistics counts only public reviews.
	Statistics(ctx context.Context, poiID uuid.UUID) (*Statistics, error)
	// UserStatistics counts hidden reviews only when includeHidden is set
	// (the author looking at their own).
	UserStatistics(ctx context.Context, userID uuid.UUID, includeHidden bool) (*UserStatistics, error)
	// GetByUserAndPOI returns userID's review of poiID, published or not.
	GetByUserAndPOI(ctx context.Context, userID, poiID uuid.UUID) (*Review, error)
	// VotedBy reports which of reviewIDs userID has marked helpful.
	VotedBy(ctx context.Context, userID uuid.UUID, reviewIDs []uuid.UUID) (map[uuid.UUID]bool, error)
	// Report records (or re-records) reporterID's report of reviewID.
	Report(ctx context.Context, reporterID, reviewID uuid.UUID, reason, details string) error
	// ListReported is the moderation queue: reviews with open reports that
	// are not removed, most-reported first.
	ListReported(ctx context.Context, limit, offset int) ([]*ReportedReview, int, error)
	// Moderate records a moderator's decision and settles the open reports.
	// ErrNotFound when the review does not exist.
	Moderate(ctx context.Context, reviewID, moderatorID uuid.UUID, action ModerationAction) error
}

type repository struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewRepository(db *pgxpool.Pool, logger *slog.Logger) Repository {
	return &repository{db: db, logger: logger.With(slog.String("component", "review-repository"))}
}

// openReportsSQL counts the reports on review `r` a moderator has not yet
// settled: all of them, or those made after the last moderation. One row per
// reporter (review_reports_reporter_review_unique), so this is a count of
// distinct people.
const openReportsSQL = `(SELECT COUNT(*) FROM review_reports rr
	WHERE rr.review_id = r.id AND (r.moderated_at IS NULL OR rr.created_at > r.moderated_at))`

// hiddenSQL is true for a review out of public reads.
var hiddenSQL = fmt.Sprintf(`(r.moderation_status = 'removed' OR %s >= %d)`, openReportsSQL, AutoHideReports)

// visibleToSQL keeps public reviews and the viewer's own; the viewer is the
// placeholder named by the caller.
func visibleToSQL(viewerParam string) string {
	return `(NOT ` + hiddenSQL + ` OR r.user_id = ` + viewerParam + `)`
}

// selectReview joins the reviewer (users) and the reviewed POI for display
// enrichment. Callers add a WHERE on the `r` alias.
var selectReview = `
	SELECT r.id, r.user_id, r.poi_id, r.rating, COALESCE(r.title, ''), r.content, r.image_urls,
	       r.visit_date, r.helpful, r.unhelpful, r.is_verified, r.is_published, r.created_at, r.updated_at,
	       COALESCE(NULLIF(u.username, ''), u.display_name, '') AS reviewer_name,
	       COALESCE(u.profile_image_url, '') AS reviewer_avatar,
	       u.created_at AS reviewer_since,
	       COALESCE(p.name, '') AS poi_name,
	       (SELECT COUNT(*) FROM review_reports rr WHERE rr.review_id = r.id)::int AS report_count,
	       ` + hiddenSQL + ` AS hidden
	FROM reviews r
	JOIN users u ON u.id = r.user_id
	LEFT JOIN points_of_interest p ON p.id = r.poi_id`

func scanReview(row pgx.Row) (*Review, error) {
	var r Review
	err := row.Scan(&r.ID, &r.UserID, &r.POIID, &r.Rating, &r.Title, &r.Content, &r.Photos,
		&r.VisitDate, &r.Helpful, &r.Unhelpful, &r.IsVerified, &r.IsPublished, &r.CreatedAt, &r.UpdatedAt,
		&r.ReviewerName, &r.ReviewerAvatar, &r.ReviewerSince, &r.POIName, &r.ReportCount, &r.Hidden)
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
		FROM reviews r
		WHERE r.poi_id = $1 AND r.is_published = true AND NOT `+hiddenSQL, poiID).
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
func (repo *repository) UserStatistics(ctx context.Context, userID uuid.UUID, includeHidden bool) (*UserStatistics, error) {
	st := &UserStatistics{}
	err := repo.db.QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(AVG(rating), 0)::float8,
		       COALESCE(SUM(helpful), 0),
		       COUNT(*) FILTER (WHERE rating = 1),
		       COUNT(*) FILTER (WHERE rating = 2),
		       COUNT(*) FILTER (WHERE rating = 3),
		       COUNT(*) FILTER (WHERE rating = 4),
		       COUNT(*) FILTER (WHERE rating = 5)
		FROM reviews r
		WHERE r.user_id = $1 AND r.is_published = true AND ($2 OR NOT `+hiddenSQL+`)`, userID, includeHidden).
		Scan(&st.TotalReviews, &st.AverageRatingGiven, &st.HelpfulVotesReceived,
			&st.Distribution[0], &st.Distribution[1], &st.Distribution[2], &st.Distribution[3], &st.Distribution[4])
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

func (repo *repository) listBy(ctx context.Context, where string, arg, viewer uuid.UUID, limit, offset int) ([]*Review, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	filter := ` WHERE ` + where + ` AND r.is_published = true AND ` + visibleToSQL("$2")
	var total int
	if err := repo.db.QueryRow(ctx, `SELECT COUNT(*) FROM reviews r`+filter, arg, viewer).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := repo.db.Query(ctx,
		selectReview+filter+` ORDER BY r.created_at DESC LIMIT $3 OFFSET $4`,
		arg, viewer, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return collectReviews(rows, total)
}

func collectReviews(rows pgx.Rows, total int) ([]*Review, int, error) {
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

func (repo *repository) ListByPOI(ctx context.Context, poiID, viewer uuid.UUID, limit, offset int) ([]*Review, int, error) {
	return repo.listBy(ctx, "r.poi_id = $1", poiID, viewer, limit, offset)
}

func (repo *repository) ListByUser(ctx context.Context, userID, viewer uuid.UUID, limit, offset int) ([]*Review, int, error) {
	return repo.listBy(ctx, "r.user_id = $1", userID, viewer, limit, offset)
}

// ListRecent returns the most recent published reviews across all content.
func (repo *repository) ListRecent(ctx context.Context, viewer uuid.UUID, limit, offset int) ([]*Review, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	filter := ` WHERE r.is_published = true AND ` + visibleToSQL("$1")
	var total int
	if err := repo.db.QueryRow(ctx, `SELECT COUNT(*) FROM reviews r`+filter, viewer).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := repo.db.Query(ctx,
		selectReview+filter+` ORDER BY r.created_at DESC LIMIT $2 OFFSET $3`,
		viewer, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return collectReviews(rows, total)
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

func (repo *repository) GetByUserAndPOI(ctx context.Context, userID, poiID uuid.UUID) (*Review, error) {
	r, err := scanReview(repo.db.QueryRow(ctx, selectReview+` WHERE r.user_id = $1 AND r.poi_id = $2`, userID, poiID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (repo *repository) VotedBy(ctx context.Context, userID uuid.UUID, reviewIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool)
	if userID == uuid.Nil || len(reviewIDs) == 0 {
		return out, nil
	}
	rows, err := repo.db.Query(ctx, `
		SELECT review_id FROM review_helpfuls
		WHERE user_id = $1 AND review_id = ANY($2) AND is_helpful = true`, userID, reviewIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Report upserts the reporter's report: a second report of the same review
// replaces the reason and details instead of adding a row. Reporting your own
// review is ErrReportOwnReview; a missing review is ErrNotFound.
func (repo *repository) Report(ctx context.Context, reporterID, reviewID uuid.UUID, reason, details string) error {
	var author uuid.UUID
	err := repo.db.QueryRow(ctx, `SELECT user_id FROM reviews WHERE id = $1`, reviewID).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if author == reporterID {
		return ErrReportOwnReview
	}
	_, err = repo.db.Exec(ctx, `
		INSERT INTO review_reports (review_id, reporter_id, reason, details)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (reporter_id, review_id)
		DO UPDATE SET reason = EXCLUDED.reason, details = EXCLUDED.details, created_at = NOW()`,
		reviewID, reporterID, reason, details)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation &&
		pgErr.ConstraintName == "review_reports_review_id_fkey" {
		// Deleted between the lookup and the insert.
		return ErrNotFound
	}
	return err
}

// ListReported reads the moderation queue in two statements: the page of
// reviews, ordered by open-report count, then every open report on that page
// at once for the reasons and details.
func (repo *repository) ListReported(ctx context.Context, limit, offset int) ([]*ReportedReview, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	const queued = ` WHERE r.moderation_status <> 'removed' AND ` + openReportsSQL + ` > 0`
	var total int
	if err := repo.db.QueryRow(ctx, `SELECT COUNT(*) FROM reviews r`+queued).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reported reviews: %w", err)
	}
	rows, err := repo.db.Query(ctx,
		selectReview+queued+` ORDER BY `+openReportsSQL+` DESC, r.created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list reported reviews: %w", err)
	}
	reviews, _, err := collectReviews(rows, total)
	if err != nil {
		return nil, 0, fmt.Errorf("scan reported review: %w", err)
	}
	if len(reviews) == 0 {
		return nil, total, nil
	}

	out := make([]*ReportedReview, 0, len(reviews))
	byID := make(map[uuid.UUID]*ReportedReview, len(reviews))
	ids := make([]uuid.UUID, 0, len(reviews))
	for _, r := range reviews {
		rr := &ReportedReview{Review: r, Reasons: map[string]int{}}
		out = append(out, rr)
		byID[r.ID] = rr
		ids = append(ids, r.ID)
	}
	reports, err := repo.db.Query(ctx, `
		SELECT rr.review_id, rr.reason, rr.details, rr.created_at
		FROM review_reports rr
		JOIN reviews r ON r.id = rr.review_id
		WHERE rr.review_id = ANY($1)
		  AND (r.moderated_at IS NULL OR rr.created_at > r.moderated_at)
		ORDER BY rr.created_at DESC`, ids)
	if err != nil {
		return nil, 0, fmt.Errorf("load review reports: %w", err)
	}
	defer reports.Close()
	for reports.Next() {
		var (
			reviewID        uuid.UUID
			reason, details string
			at              time.Time
		)
		if err := reports.Scan(&reviewID, &reason, &details, &at); err != nil {
			return nil, 0, fmt.Errorf("scan review report: %w", err)
		}
		rr := byID[reviewID]
		if rr == nil {
			continue
		}
		rr.OpenReports++
		rr.Reasons[reason]++
		if details != "" {
			rr.Details = append(rr.Details, details)
		}
		if at.After(rr.LastReportedAt) {
			rr.LastReportedAt = at
		}
	}
	return out, total, reports.Err()
}

// Moderate sets the review's moderation state. KEEP on a removed review puts
// it back, which is how a mistaken removal is undone.
func (repo *repository) Moderate(ctx context.Context, reviewID, moderatorID uuid.UUID, action ModerationAction) error {
	if action != ModerationKeep && action != ModerationRemove {
		return errors.Join(ErrInvalidReview, fmt.Errorf("unknown moderation action %q", action))
	}
	var moderator *uuid.UUID
	if moderatorID != uuid.Nil {
		moderator = &moderatorID
	}
	tag, err := repo.db.Exec(ctx, `
		UPDATE reviews
		SET moderation_status = $2, moderated_at = clock_timestamp(), moderated_by = $3
		WHERE id = $1`, reviewID, string(action), moderator)
	if err != nil {
		return fmt.Errorf("moderate review: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
