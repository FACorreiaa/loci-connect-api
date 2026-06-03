package share

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a share code does not resolve.
var ErrNotFound = errors.New("share not found")

// Share is the persisted share record.
type Share struct {
	Code        string
	ContentType int32
	ContentID   string
	Title       string
	Description string
	ImageURL    string
	CreatedBy   *uuid.UUID
	ViewCount   int32
	CreatedAt   time.Time
}

type Repository interface {
	Create(ctx context.Context, s *Share) error
	GetByCode(ctx context.Context, code string) (*Share, error)
	// IncrementView bumps view_count and returns the share with the new count.
	IncrementView(ctx context.Context, code string) (*Share, error)
}

type repository struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewRepository(db *pgxpool.Pool, logger *slog.Logger) Repository {
	return &repository{db: db, logger: logger.With(slog.String("component", "share-repository"))}
}

const shareCols = `share_code, content_type, content_id, title, description, image_url, created_by, view_count, created_at`

func scanShare(row pgx.Row) (*Share, error) {
	var s Share
	if err := row.Scan(&s.Code, &s.ContentType, &s.ContentID, &s.Title, &s.Description,
		&s.ImageURL, &s.CreatedBy, &s.ViewCount, &s.CreatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *repository) Create(ctx context.Context, s *Share) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO shares (share_code, content_type, content_id, title, description, image_url, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		s.Code, s.ContentType, s.ContentID, s.Title, s.Description, s.ImageURL, s.CreatedBy)
	return err
}

func (r *repository) GetByCode(ctx context.Context, code string) (*Share, error) {
	s, err := scanShare(r.db.QueryRow(ctx, `SELECT `+shareCols+` FROM shares WHERE share_code = $1`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

func (r *repository) IncrementView(ctx context.Context, code string) (*Share, error) {
	s, err := scanShare(r.db.QueryRow(ctx,
		`UPDATE shares SET view_count = view_count + 1 WHERE share_code = $1 RETURNING `+shareCols, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}
