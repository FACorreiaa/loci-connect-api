package ranking

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type repository struct {
	db *pgxpool.Pool
}

// NewRepository stores rankings in user_poi_rankings.
func NewRepository(db *pgxpool.Pool) Repository {
	return &repository{db: db}
}

// bucketRow is one row of a bucket, in position order.
type bucketRow struct {
	poiID   string
	name    string
	pending bool
}

// current is the stored state of one ranking.
type current struct {
	cityKey string
	verdict Verdict
	lo, hi  *int
}

// inTx runs fn in a transaction holding a per-user advisory lock, so two
// devices ranking at once cannot interleave their renumbering.
func (r *repository) inTx(ctx context.Context, userID uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ranking tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('ranking:' || $1::text))`, userID); err != nil {
		return fmt.Errorf("lock rankings: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func getCurrent(ctx context.Context, tx pgx.Tx, userID uuid.UUID, poiID string) (*current, error) {
	var c current
	err := tx.QueryRow(ctx, `
		SELECT city_key, verdict, search_lo, search_hi
		FROM user_poi_rankings WHERE user_id = $1 AND poi_id = $2`, userID, poiID).Scan(&c.cityKey, &c.verdict, &c.lo, &c.hi)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load ranking: %w", err)
	}
	return &c, nil
}

func loadBucket(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string, v Verdict) ([]bucketRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT r.poi_id, COALESCE(p.name, r.poi_name), r.search_lo IS NOT NULL
		FROM user_poi_rankings r
		LEFT JOIN points_of_interest p ON p.id = r.poi_id::uuid
		WHERE r.user_id = $1 AND r.city_key = $2 AND r.verdict = $3
		ORDER BY r.position`, userID, key, v)
	if err != nil {
		return nil, fmt.Errorf("load bucket: %w", err)
	}
	defer rows.Close()
	var out []bucketRow
	for rows.Next() {
		var b bucketRow
		if err := rows.Scan(&b.poiID, &b.name, &b.pending); err != nil {
			return nil, fmt.Errorf("scan bucket: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// renumber gives the listed places positions 0..n-1 in that order.
func renumber(ctx context.Context, tx pgx.Tx, userID uuid.UUID, ordered []string) error {
	if len(ordered) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE user_poi_rankings r
		SET position = o.ord - 1
		FROM unnest($2::text[]) WITH ORDINALITY AS o(poi_id, ord)
		WHERE r.user_id = $1 AND r.poi_id = o.poi_id`, userID, ordered)
	if err != nil {
		return fmt.Errorf("renumber bucket: %w", err)
	}
	return nil
}

// placedExcept splits a bucket into placed rows (excluding poiID) and the ids
// of other places still being ranked, in position order.
func placedExcept(bucket []bucketRow, poiID string) (placed []bucketRow, pendingIDs []string) {
	for _, b := range bucket {
		switch {
		case b.poiID == poiID:
		case b.pending:
			pendingIDs = append(pendingIDs, b.poiID)
		default:
			placed = append(placed, b)
		}
	}
	return placed, pendingIDs
}

func rowIDs(rows []bucketRow) []string {
	out := make([]string, len(rows))
	for i, b := range rows {
		out[i] = b.poiID
	}
	return out
}

// removeFromBucket deletes a ranking and closes the gap it leaves.
func removeFromBucket(ctx context.Context, tx pgx.Tx, userID uuid.UUID, poiID string, c *current) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_poi_rankings WHERE user_id = $1 AND poi_id = $2`, userID, poiID); err != nil {
		return fmt.Errorf("delete ranking: %w", err)
	}
	rest, err := loadBucket(ctx, tx, userID, c.cityKey, c.verdict)
	if err != nil {
		return err
	}
	return renumber(ctx, tx, userID, rowIDs(rest))
}

// settle puts poiID at index lo among the placed rows, ahead of the places
// still being ranked, and ends its comparisons.
func settle(ctx context.Context, tx pgx.Tx, userID uuid.UUID, poiID string, placed []bucketRow, pending []string, lo int) error {
	order := make([]string, 0, len(placed)+len(pending)+1)
	order = append(order, rowIDs(placed[:lo])...)
	order = append(order, poiID)
	order = append(order, rowIDs(placed[lo:])...)
	order = append(order, pending...)
	if _, err := tx.Exec(ctx, `
		UPDATE user_poi_rankings SET search_lo = NULL, search_hi = NULL, updated_at = NOW()
		WHERE user_id = $1 AND poi_id = $2`, userID, poiID); err != nil {
		return fmt.Errorf("place ranking: %w", err)
	}
	return renumber(ctx, tx, userID, order)
}

// resume answers a repeated verdict: nothing to ask when the place is already
// placed, else the next comparison of its unfinished search (settling it when
// the bucket has shrunk so that nothing is left to ask).
func resume(ctx context.Context, tx pgx.Tx, userID uuid.UUID, poiID string, c *current) (*Comparison, error) {
	if c.lo == nil || c.hi == nil {
		return nil, nil
	}
	bucket, err := loadBucket(ctx, tx, userID, c.cityKey, c.verdict)
	if err != nil {
		return nil, err
	}
	placed, pending := placedExcept(bucket, poiID)
	lo, hi := clampRange(*c.lo, *c.hi, len(placed))
	if mid, done := nextProbe(lo, hi); !done {
		return &Comparison{POIID: placed[mid].poiID, Name: placed[mid].name}, nil
	}
	return nil, settle(ctx, tx, userID, poiID, placed, pending, lo)
}

func (r *repository) Rate(ctx context.Context, userID uuid.UUID, in RateInput) (Place, *Comparison, error) {
	key := cityKey(in.CityName)
	var next *Comparison
	err := r.inTx(ctx, userID, func(tx pgx.Tx) error {
		old, err := getCurrent(ctx, tx, userID, in.POIID)
		if err != nil {
			return err
		}
		if old != nil && old.cityKey == key && old.verdict == in.Verdict {
			// The same answer again (a re-arrival, or a notification answered
			// after the in-app card): keep its place, or pick up the
			// comparisons where they stopped.
			next, err = resume(ctx, tx, userID, in.POIID, old)
			return err
		}
		if old != nil {
			if err := removeFromBucket(ctx, tx, userID, in.POIID, old); err != nil {
				return err
			}
		}
		bucket, err := loadBucket(ctx, tx, userID, key, in.Verdict)
		if err != nil {
			return err
		}
		placed, _ := placedExcept(bucket, in.POIID)
		var lo, hi *int
		if len(placed) > 0 {
			zero, n := 0, len(placed)
			lo, hi = &zero, &n
			mid, _ := nextProbe(0, n)
			next = &Comparison{POIID: placed[mid].poiID, Name: placed[mid].name}
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO user_poi_rankings
				(user_id, poi_id, poi_name, category, city_key, city_name, verdict, position, search_lo, search_hi)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			userID, in.POIID, strings.TrimSpace(in.POIName), strings.TrimSpace(in.Category),
			key, strings.TrimSpace(in.CityName), in.Verdict, len(bucket), lo, hi)
		if err != nil {
			return fmt.Errorf("insert ranking: %w", err)
		}
		return nil
	})
	if err != nil {
		return Place{}, nil, err
	}
	place, err := r.place(ctx, userID, key, in.POIID)
	return place, next, err
}

func (r *repository) Compare(ctx context.Context, userID uuid.UUID, poiID, otherID string, better bool) (Place, *Comparison, error) {
	var (
		key  string
		next *Comparison
	)
	err := r.inTx(ctx, userID, func(tx pgx.Tx) error {
		c, err := getCurrent(ctx, tx, userID, poiID)
		if err != nil {
			return err
		}
		if c == nil {
			return ErrNotFound
		}
		if c.lo == nil || c.hi == nil {
			return ErrNotPending
		}
		key = c.cityKey
		bucket, err := loadBucket(ctx, tx, userID, c.cityKey, c.verdict)
		if err != nil {
			return err
		}
		placed, pending := placedExcept(bucket, poiID)

		lo, hi := clampRange(*c.lo, *c.hi, len(placed))
		idx := -1
		for i, b := range placed {
			if b.poiID == otherID {
				idx = i
				break
			}
		}
		lo, hi = narrow(lo, hi, idx, better)

		if mid, done := nextProbe(lo, hi); !done {
			next = &Comparison{POIID: placed[mid].poiID, Name: placed[mid].name}
			_, err := tx.Exec(ctx, `
				UPDATE user_poi_rankings SET search_lo = $3, search_hi = $4, updated_at = NOW()
				WHERE user_id = $1 AND poi_id = $2`, userID, poiID, lo, hi)
			if err != nil {
				return fmt.Errorf("narrow ranking: %w", err)
			}
			return nil
		}

		return settle(ctx, tx, userID, poiID, placed, pending, lo)
	})
	if err != nil {
		return Place{}, nil, err
	}
	place, err := r.place(ctx, userID, key, poiID)
	return place, next, err
}

// clampRange keeps a stored range valid for a bucket that may have shrunk or
// grown since it was stored.
func clampRange(lo, hi, n int) (int, int) {
	lo = max(0, min(lo, n))
	hi = max(lo, min(hi, n))
	return lo, hi
}

func (r *repository) Delete(ctx context.Context, userID uuid.UUID, poiID string) (bool, error) {
	var deleted bool
	err := r.inTx(ctx, userID, func(tx pgx.Tx) error {
		c, err := getCurrent(ctx, tx, userID, poiID)
		if err != nil || c == nil {
			return err
		}
		deleted = true
		return removeFromBucket(ctx, tx, userID, poiID, c)
	})
	return deleted, err
}

func (r *repository) List(ctx context.Context, userID uuid.UUID, city string, limit int) ([]Place, error) {
	return r.list(ctx, userID, cityKey(city), "", limit)
}

func (r *repository) place(ctx context.Context, userID uuid.UUID, key, poiID string) (Place, error) {
	places, err := r.list(ctx, userID, key, poiID, 1)
	if err != nil {
		return Place{}, err
	}
	if len(places) == 0 {
		return Place{}, ErrNotFound
	}
	return places[0], nil
}

// list reads rankings with their rank in the city and their place in the
// bucket. key "" means every city; poiID narrows to one place after ranking,
// so its rank still counts the whole city.
func (r *repository) list(ctx context.Context, userID uuid.UUID, key, poiID string, limit int) ([]Place, error) {
	rows, err := r.db.Query(ctx, `
		SELECT poi_id, name, category, city_name, verdict, rank, idx, size, updated_at, pending
		FROM (
			SELECT
				r.poi_id,
				COALESCE(p.name, r.poi_name) AS name,
				COALESCE(NULLIF(p.category, ''), r.category) AS category,
				MIN(r.city_name) OVER (PARTITION BY r.city_key) AS city_name,
				r.city_key,
				r.verdict,
				ROW_NUMBER() OVER (PARTITION BY r.city_key ORDER BY r.verdict, r.position) AS rank,
				ROW_NUMBER() OVER (PARTITION BY r.city_key, r.verdict ORDER BY r.position) - 1 AS idx,
				COUNT(*) OVER (PARTITION BY r.city_key, r.verdict) AS size,
				r.updated_at,
				r.search_lo IS NOT NULL AS pending
			FROM user_poi_rankings r
			LEFT JOIN points_of_interest p ON p.id = r.poi_id::uuid
			WHERE r.user_id = $1 AND ($2 = '' OR r.city_key = $2)
		) ranked
		WHERE $3 = '' OR poi_id = $3
		ORDER BY city_key, rank
		LIMIT $4`, userID, key, poiID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rankings: %w", err)
	}
	defer rows.Close()
	var out []Place
	for rows.Next() {
		var (
			p         Place
			idx, size int
		)
		if err := rows.Scan(&p.POIID, &p.Name, &p.Category, &p.CityName, &p.Verdict, &p.Rank, &idx, &size, &p.UpdatedAt, &p.Pending); err != nil {
			return nil, fmt.Errorf("scan ranking: %w", err)
		}
		p.Score = Score(p.Verdict, idx, size)
		out = append(out, p)
	}
	return out, rows.Err()
}
