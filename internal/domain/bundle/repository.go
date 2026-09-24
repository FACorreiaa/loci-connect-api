// Package bundle serves City Packs: pre-made itineraries written offline,
// approved by a person, and published for anyone to browse.
//
// Two rules shape everything here.
//
// A pack renders from its own rows. Stops carry a snapshot of the place they
// point at, so a merge or delete in points_of_interest costs enrichment and
// never content. See the comment block in migration 0087.
//
// A locked day never leaves the database. Truncation is a LIMIT in the day
// query, not a filter applied after loading — the cheapest way to be sure that
// content somebody has not paid for cannot reach a response body.
package bundle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no published pack matches.
var ErrNotFound = errors.New("bundle not found")

// Status is where a pack sits in the authoring pipeline.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusApproved  Status = "approved"
	StatusPublished Status = "published"
	StatusRetired   Status = "retired"
)

// PurchaseStatus is the state of one purchase.
type PurchaseStatus string

const (
	PurchasePaid     PurchaseStatus = "paid"
	PurchaseRefunded PurchaseStatus = "refunded"
)

// Bundle is one pack. Days is empty unless the caller asked for detail.
type Bundle struct {
	ID            uuid.UUID
	Slug          string
	Title         string
	Summary       string
	CityID        *uuid.UUID
	CityName      string
	CountryCode   string
	Theme         string
	Months        []int16
	DayCount      int
	StopCount     int
	CoverImageURL *string
	IsPaid        bool
	Status        Status
	SourcePrompt  string
	SourceModel   string
	GeneratedAt   *time.Time
	ApprovedBy    string
	ApprovedAt    *time.Time
	PublishedAt   *time.Time
	VerifiedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Days []Day
}

// Day is one day of a pack.
type Day struct {
	ID        uuid.UUID
	BundleID  uuid.UUID
	DayNumber int
	Title     string
	Summary   string
	Stops     []Stop
}

// Stop is one place on one day.
//
// Everything from Name down is a snapshot taken when the pack was authored.
// POIID is an optional link for enrichment, never a dependency.
type Stop struct {
	ID              uuid.UUID
	DayID           uuid.UUID
	OrderIndex      int
	POIID           *uuid.UUID
	Name            string
	Category        string
	Description     string
	Latitude        *float64
	Longitude       *float64
	Address         string
	Website         *string
	BookingURL      *string
	ImageURL        *string
	Notes           string
	StartMinute     *int
	DurationMinutes *int
}

// Purchase is one person's ownership of one pack.
type Purchase struct {
	UserID                  uuid.UUID
	BundleID                uuid.UUID
	StripeCheckoutSessionID string
	StripePaymentIntentID   string
	AmountCents             int
	Currency                string
}

// ListFilter narrows the catalog. Every field is optional and they AND.
type ListFilter struct {
	CityName string
	Theme    string
	Month    int // 1-12; 0 means any
	OnlyFree bool
	Page     int
	PageSize int
}

// Repository reads and writes packs.
type Repository interface {
	// ListPublished returns published packs and the total matching count.
	ListPublished(ctx context.Context, f ListFilter) ([]Bundle, int, error)
	// GetPublishedBySlug returns a pack's metadata. Retired packs are included
	// so that somebody who bought one can still open it; the service decides
	// whether this caller may.
	GetPublishedBySlug(ctx context.Context, slug string) (*Bundle, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Bundle, error)
	// LoadDays hydrates days and stops. maxDays > 0 stops after that many days
	// and is how the paywall is enforced; 0 loads all of them.
	LoadDays(ctx context.Context, bundleID uuid.UUID, maxDays int) ([]Day, error)

	IsOwned(ctx context.Context, userID, bundleID uuid.UUID) (bool, error)
	OwnedIDs(ctx context.Context, userID uuid.UUID, bundleIDs []uuid.UUID) (map[uuid.UUID]bool, error)
	ListOwned(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]Bundle, int, error)

	// RecordPurchase grants ownership. It is safe to call twice for the same
	// checkout session: the second call is a no-op.
	RecordPurchase(ctx context.Context, p Purchase) error
	// MarkRefunded revokes ownership for a payment intent.
	MarkRefunded(ctx context.Context, paymentIntentID string) error

	// ClaimedTrip returns the trip this user's claim of the pack produced, if
	// there is one.
	ClaimedTrip(ctx context.Context, userID, bundleID uuid.UUID) (tripID uuid.UUID, found bool, err error)
	// RecordClaim records tripID as the user's claim of the pack and returns
	// the trip that holds the claim: tripID, or the one an earlier claim
	// recorded first.
	RecordClaim(ctx context.Context, userID, bundleID, tripID uuid.UUID) (uuid.UUID, error)
}

var _ Repository = (*RepositoryImpl)(nil)

// RepositoryImpl is the Postgres implementation.
type RepositoryImpl struct {
	pgpool *pgxpool.Pool
	logger *slog.Logger
}

// NewRepository builds a bundle repository.
func NewRepository(pgpool *pgxpool.Pool, logger *slog.Logger) *RepositoryImpl {
	return &RepositoryImpl{pgpool: pgpool, logger: logger}
}

const bundleColumns = `
    id, slug, title, summary, city_id, city_name, country_code, theme, months,
    day_count, stop_count, cover_image_url, is_paid, status, source_prompt,
    source_model, generated_at, approved_by, approved_at, published_at,
    verified_at, created_at, updated_at`

func scanBundle(row pgx.Row) (*Bundle, error) {
	var b Bundle
	err := row.Scan(
		&b.ID, &b.Slug, &b.Title, &b.Summary, &b.CityID, &b.CityName,
		&b.CountryCode, &b.Theme, &b.Months, &b.DayCount, &b.StopCount,
		&b.CoverImageURL, &b.IsPaid, &b.Status, &b.SourcePrompt, &b.SourceModel,
		&b.GeneratedAt, &b.ApprovedBy, &b.ApprovedAt, &b.PublishedAt,
		&b.VerifiedAt, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func paginationBounds(page, pageSize int) (limit, offset int) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}
	return pageSize, (page - 1) * pageSize
}

// ListPublished returns published packs and the total matching count.
func (r *RepositoryImpl) ListPublished(ctx context.Context, f ListFilter) ([]Bundle, int, error) {
	where := []string{"status = 'published'"}
	args := []any{}

	if f.CityName != "" {
		args = append(args, f.CityName)
		where = append(where, fmt.Sprintf("LOWER(city_name) = LOWER($%d)", len(args)))
	}
	if f.Theme != "" {
		args = append(args, f.Theme)
		where = append(where, fmt.Sprintf("theme = $%d", len(args)))
	}
	if f.Month > 0 {
		// An empty months array means "any time of year", so it matches every
		// month rather than none. A pack with no season is not out of season.
		args = append(args, int16(f.Month))
		where = append(where, fmt.Sprintf("(months = '{}' OR $%d = ANY(months))", len(args)))
	}
	if f.OnlyFree {
		where = append(where, "is_paid = FALSE")
	}

	clause := strings.Join(where, " AND ")

	var total int
	if err := r.pgpool.QueryRow(ctx,
		"SELECT COUNT(*) FROM bundles WHERE "+clause, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count bundles: %w", err)
	}

	limit, offset := paginationBounds(f.Page, f.PageSize)
	args = append(args, limit, offset)

	query := fmt.Sprintf(
		"SELECT %s FROM bundles WHERE %s ORDER BY published_at DESC NULLS LAST, title ASC LIMIT $%d OFFSET $%d",
		bundleColumns, clause, len(args)-1, len(args),
	)

	rows, err := r.pgpool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list bundles: %w", err)
	}
	defer rows.Close()

	out := []Bundle{}
	for rows.Next() {
		b, err := scanBundle(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan bundle: %w", err)
		}
		out = append(out, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate bundles: %w", err)
	}
	return out, total, nil
}

// GetPublishedBySlug returns a published or retired pack by slug.
func (r *RepositoryImpl) GetPublishedBySlug(ctx context.Context, slug string) (*Bundle, error) {
	query := "SELECT " + bundleColumns +
		" FROM bundles WHERE slug = $1 AND status IN ('published', 'retired')"
	b, err := scanBundle(r.pgpool.QueryRow(ctx, query, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get bundle by slug: %w", err)
	}
	return b, nil
}

// GetByID returns any pack by id, whatever its status. Authoring and checkout
// both need to see a row the catalog would hide.
func (r *RepositoryImpl) GetByID(ctx context.Context, id uuid.UUID) (*Bundle, error) {
	query := "SELECT " + bundleColumns + " FROM bundles WHERE id = $1"
	b, err := scanBundle(r.pgpool.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get bundle by id: %w", err)
	}
	return b, nil
}

// LoadDays hydrates days and their stops.
//
// maxDays is the paywall. Passing 1 returns day 1 and leaves every later day
// in the database, so a response can never carry content the caller has not
// paid for — not even in a field the UI happens not to render.
func (r *RepositoryImpl) LoadDays(ctx context.Context, bundleID uuid.UUID, maxDays int) ([]Day, error) {
	dayQuery := `
        SELECT id, bundle_id, day_number, title, summary
        FROM bundle_days
        WHERE bundle_id = $1
        ORDER BY day_number ASC`
	args := []any{bundleID}
	if maxDays > 0 {
		dayQuery += " LIMIT $2"
		args = append(args, maxDays)
	}

	rows, err := r.pgpool.Query(ctx, dayQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("load bundle days: %w", err)
	}
	defer rows.Close()

	days := []Day{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var d Day
		if err := rows.Scan(&d.ID, &d.BundleID, &d.DayNumber, &d.Title, &d.Summary); err != nil {
			return nil, fmt.Errorf("scan bundle day: %w", err)
		}
		d.Stops = []Stop{}
		index[d.ID] = len(days)
		days = append(days, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bundle days: %w", err)
	}
	if len(days) == 0 {
		return days, nil
	}

	dayIDs := make([]uuid.UUID, 0, len(days))
	for _, d := range days {
		dayIDs = append(dayIDs, d.ID)
	}

	stopRows, err := r.pgpool.Query(ctx, `
        SELECT id, bundle_day_id, order_index, poi_id, name, category, description,
               latitude, longitude, address, website, booking_url, image_url,
               notes, start_minute, duration_minutes
        FROM bundle_stops
        WHERE bundle_day_id = ANY($1)
        ORDER BY bundle_day_id, order_index ASC`, dayIDs)
	if err != nil {
		return nil, fmt.Errorf("load bundle stops: %w", err)
	}
	defer stopRows.Close()

	for stopRows.Next() {
		var s Stop
		if err := stopRows.Scan(
			&s.ID, &s.DayID, &s.OrderIndex, &s.POIID, &s.Name, &s.Category,
			&s.Description, &s.Latitude, &s.Longitude, &s.Address, &s.Website,
			&s.BookingURL, &s.ImageURL, &s.Notes, &s.StartMinute, &s.DurationMinutes,
		); err != nil {
			return nil, fmt.Errorf("scan bundle stop: %w", err)
		}
		if i, ok := index[s.DayID]; ok {
			days[i].Stops = append(days[i].Stops, s)
		}
	}
	if err := stopRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bundle stops: %w", err)
	}
	return days, nil
}

// ClaimedTrip reads bundle_claims. A deleted trip takes its claim row with it.
func (r *RepositoryImpl) ClaimedTrip(ctx context.Context, userID, bundleID uuid.UUID) (uuid.UUID, bool, error) {
	var tripID uuid.UUID
	err := r.pgpool.QueryRow(ctx, `
        SELECT trip_id FROM bundle_claims
        WHERE user_id = $1 AND bundle_id = $2`, userID, bundleID).Scan(&tripID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("read bundle claim: %w", err)
	}
	return tripID, true, nil
}

// RecordClaim inserts the claim, or leaves an existing one alone and returns
// its trip.
func (r *RepositoryImpl) RecordClaim(ctx context.Context, userID, bundleID, tripID uuid.UUID) (uuid.UUID, error) {
	var held uuid.UUID
	err := r.pgpool.QueryRow(ctx, `
        WITH ins AS (
            INSERT INTO bundle_claims (user_id, bundle_id, trip_id)
            VALUES ($1, $2, $3)
            ON CONFLICT (user_id, bundle_id) DO NOTHING
            RETURNING trip_id
        )
        SELECT trip_id FROM ins
        UNION ALL
        SELECT trip_id FROM bundle_claims
        WHERE user_id = $1 AND bundle_id = $2
          AND NOT EXISTS (SELECT 1 FROM ins)
        LIMIT 1`, userID, bundleID, tripID).Scan(&held)
	if errors.Is(err, pgx.ErrNoRows) {
		// The conflicting claim committed after this statement's snapshot, so
		// the fallback SELECT could not see it. A fresh read can.
		existing, found, readErr := r.ClaimedTrip(ctx, userID, bundleID)
		if readErr != nil {
			return uuid.Nil, readErr
		}
		if !found {
			return uuid.Nil, fmt.Errorf("record bundle claim: claim for bundle %s vanished", bundleID)
		}
		return existing, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("record bundle claim: %w", err)
	}
	return held, nil
}

// IsOwned reports whether the user holds a live purchase for this pack.
//
// It reads bundle_purchases and nothing else. A Pro subscription grants no
// packs, and owning a pack requires no subscription.
func (r *RepositoryImpl) IsOwned(ctx context.Context, userID, bundleID uuid.UUID) (bool, error) {
	var exists bool
	err := r.pgpool.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM bundle_purchases
            WHERE user_id = $1 AND bundle_id = $2 AND status = 'paid'
        )`, userID, bundleID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check bundle ownership: %w", err)
	}
	return exists, nil
}

// OwnedIDs reports which of the given packs the user owns, in one round trip,
// so a catalog page does not issue a query per card.
func (r *RepositoryImpl) OwnedIDs(ctx context.Context, userID uuid.UUID, bundleIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	owned := map[uuid.UUID]bool{}
	if len(bundleIDs) == 0 {
		return owned, nil
	}
	rows, err := r.pgpool.Query(ctx, `
        SELECT bundle_id FROM bundle_purchases
        WHERE user_id = $1 AND bundle_id = ANY($2) AND status = 'paid'`,
		userID, bundleIDs)
	if err != nil {
		return nil, fmt.Errorf("list owned bundle ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan owned bundle id: %w", err)
		}
		owned[id] = true
	}
	return owned, rows.Err()
}

// ListOwned returns the packs a user has bought.
func (r *RepositoryImpl) ListOwned(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]Bundle, int, error) {
	var total int
	if err := r.pgpool.QueryRow(ctx,
		"SELECT COUNT(*) FROM bundle_purchases WHERE user_id = $1 AND status = 'paid'",
		userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count owned bundles: %w", err)
	}

	limit, offset := paginationBounds(page, pageSize)
	rows, err := r.pgpool.Query(ctx, `
        SELECT `+bundleColumns+`
        FROM bundles b
        JOIN bundle_purchases p ON p.bundle_id = b.id
        WHERE p.user_id = $1 AND p.status = 'paid'
        ORDER BY p.purchased_at DESC
        LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list owned bundles: %w", err)
	}
	defer rows.Close()

	out := []Bundle{}
	for rows.Next() {
		b, err := scanBundle(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan owned bundle: %w", err)
		}
		out = append(out, *b)
	}
	return out, total, rows.Err()
}

// RecordPurchase grants ownership.
//
// ON CONFLICT DO NOTHING on the checkout session id is the second idempotency
// gate. webhook_events already makes a replayed Stripe delivery a no-op; this
// one holds even when the same purchase arrives by another route, such as a
// reconcile sweep after a webhook was missed.
func (r *RepositoryImpl) RecordPurchase(ctx context.Context, p Purchase) error {
	_, err := r.pgpool.Exec(ctx, `
        INSERT INTO bundle_purchases (
            user_id, bundle_id, stripe_checkout_session_id,
            stripe_payment_intent_id, amount_cents, currency, status
        ) VALUES ($1, $2, $3, $4, $5, $6, 'paid')
        ON CONFLICT (stripe_checkout_session_id) DO NOTHING`,
		p.UserID, p.BundleID, p.StripeCheckoutSessionID,
		p.StripePaymentIntentID, p.AmountCents, p.Currency)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// The partial unique index fired: they already own it from an
			// earlier session. Nothing to do, and not an error.
			return nil
		}
		return fmt.Errorf("record bundle purchase: %w", err)
	}
	return nil
}

// MarkRefunded revokes ownership for a refunded payment.
func (r *RepositoryImpl) MarkRefunded(ctx context.Context, paymentIntentID string) error {
	_, err := r.pgpool.Exec(ctx, `
        UPDATE bundle_purchases
        SET status = 'refunded', refunded_at = now()
        WHERE stripe_payment_intent_id = $1 AND status = 'paid'`, paymentIntentID)
	if err != nil {
		return fmt.Errorf("mark bundle purchase refunded: %w", err)
	}
	return nil
}
