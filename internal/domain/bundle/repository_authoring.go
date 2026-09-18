package bundle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Draft is a freshly generated pack, before anyone has looked at it.
type Draft struct {
	Slug         string
	Title        string
	Summary      string
	CityID       *uuid.UUID
	CityName     string
	CountryCode  string
	Theme        string
	Months       []int16
	IsPaid       bool
	SourcePrompt string
	SourceModel  string
	Days         []Day
}

// Authoring is the write side, used only by the offline forge. It is a
// separate interface from Repository so that nothing serving requests can
// reach it by accident.
type Authoring interface {
	CreateDraft(ctx context.Context, d Draft) (uuid.UUID, error)
	GetAnyBySlug(ctx context.Context, slug string) (*Bundle, error)
	ListByStatus(ctx context.Context, status Status) ([]Bundle, error)
	SetStatus(ctx context.Context, id uuid.UUID, status Status, approvedBy string) error
	SlugExists(ctx context.Context, slug string) (bool, error)
	LinkStopPOI(ctx context.Context, stopID, poiID uuid.UUID) error
}

var _ Authoring = (*RepositoryImpl)(nil)

// CreateDraft writes a generated pack and everything under it in one
// transaction. A pack that is half-written is worse than one that is missing,
// because the review step would show something the generator never produced.
func (r *RepositoryImpl) CreateDraft(ctx context.Context, d Draft) (uuid.UUID, error) {
	tx, err := r.pgpool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is a no-op after commit

	stopCount := 0
	for _, day := range d.Days {
		stopCount += len(day.Stops)
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
        INSERT INTO bundles (
            slug, title, summary, city_id, city_name, country_code, theme,
            months, day_count, stop_count, is_paid, status,
            source_prompt, source_model, generated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'draft',$12,$13,now())
        RETURNING id`,
		d.Slug, d.Title, d.Summary, d.CityID, d.CityName, d.CountryCode, d.Theme,
		d.Months, len(d.Days), stopCount, d.IsPaid, d.SourcePrompt, d.SourceModel,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert bundle: %w", err)
	}

	for _, day := range d.Days {
		var dayID uuid.UUID
		if err := tx.QueryRow(ctx, `
            INSERT INTO bundle_days (bundle_id, day_number, title, summary)
            VALUES ($1,$2,$3,$4) RETURNING id`,
			id, day.DayNumber, day.Title, day.Summary,
		).Scan(&dayID); err != nil {
			return uuid.Nil, fmt.Errorf("insert bundle day %d: %w", day.DayNumber, err)
		}

		for i, s := range day.Stops {
			if _, err := tx.Exec(ctx, `
                INSERT INTO bundle_stops (
                    bundle_day_id, order_index, poi_id, name, category, description,
                    latitude, longitude, address, website, booking_url, image_url,
                    notes, start_minute, duration_minutes
                ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
				dayID, i, s.POIID, s.Name, s.Category, s.Description,
				s.Latitude, s.Longitude, s.Address, s.Website, s.BookingURL,
				s.ImageURL, s.Notes, s.StartMinute, s.DurationMinutes,
			); err != nil {
				return uuid.Nil, fmt.Errorf("insert stop %q: %w", s.Name, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit draft: %w", err)
	}
	return id, nil
}

// GetAnyBySlug returns a pack whatever its status, for the review tools.
func (r *RepositoryImpl) GetAnyBySlug(ctx context.Context, slug string) (*Bundle, error) {
	b, err := scanBundle(r.pgpool.QueryRow(ctx,
		"SELECT "+bundleColumns+" FROM bundles WHERE slug = $1", slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get bundle by slug: %w", err)
	}
	return b, nil
}

// ListByStatus lists packs in one state, oldest first.
func (r *RepositoryImpl) ListByStatus(ctx context.Context, status Status) ([]Bundle, error) {
	rows, err := r.pgpool.Query(ctx,
		"SELECT "+bundleColumns+" FROM bundles WHERE status = $1 ORDER BY created_at ASC",
		string(status))
	if err != nil {
		return nil, fmt.Errorf("list bundles by status: %w", err)
	}
	defer rows.Close()

	out := []Bundle{}
	for rows.Next() {
		b, err := scanBundle(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bundle: %w", err)
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// SetStatus moves a pack through the pipeline, stamping the timestamps that
// go with each state.
func (r *RepositoryImpl) SetStatus(ctx context.Context, id uuid.UUID, status Status, approvedBy string) error {
	now := time.Now()
	var (
		approvedAt  *time.Time
		publishedAt *time.Time
		verifiedAt  *time.Time
	)
	switch status {
	case StatusApproved:
		approvedAt, verifiedAt = &now, &now
	case StatusPublished:
		publishedAt = &now
	case StatusDraft, StatusRetired:
		// No stamp: a pack can be sent back to draft or withdrawn without
		// rewriting when it was approved.
	}

	ct, err := r.pgpool.Exec(ctx, `
        UPDATE bundles SET
            status       = $2,
            approved_by  = COALESCE(NULLIF($3, ''), approved_by),
            approved_at  = COALESCE($4, approved_at),
            published_at = COALESCE($5, published_at),
            verified_at  = COALESCE($6, verified_at)
        WHERE id = $1`,
		id, string(status), approvedBy, approvedAt, publishedAt, verifiedAt)
	if err != nil {
		return fmt.Errorf("set bundle status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SlugExists reports whether a slug is taken, so a re-run does not collide.
func (r *RepositoryImpl) SlugExists(ctx context.Context, slug string) (bool, error) {
	var exists bool
	err := r.pgpool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM bundles WHERE slug = $1)", slug).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check slug: %w", err)
	}
	return exists, nil
}

// LinkStopPOI attaches a canonical POI row to a stop after the fact. Used by
// the relink pass, which repairs links a POI merge dropped.
func (r *RepositoryImpl) LinkStopPOI(ctx context.Context, stopID, poiID uuid.UUID) error {
	_, err := r.pgpool.Exec(ctx,
		"UPDATE bundle_stops SET poi_id = $2 WHERE id = $1", stopID, poiID)
	if err != nil {
		return fmt.Errorf("link stop poi: %w", err)
	}
	return nil
}
