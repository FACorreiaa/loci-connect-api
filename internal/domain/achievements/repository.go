package achievements

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Source is why an achievement was granted.
type Source string

const (
	// SourceLive is earned by something the user just did.
	SourceLive Source = "live"
	// SourceBackfill is earned by what the user did before achievements
	// existed: no push, no feed, revealed once.
	SourceBackfill Source = "backfill"
)

// Unit is one earnable thing: a track's tier, or a moment or secret at
// TierNone.
type Unit struct {
	ID   string
	Tier Tier
}

// Earned is a unit the user holds.
type Earned struct {
	Unit
	At     time.Time
	Source Source
	Seen   bool
}

// Rarity is how many people hold each unit, over Base active explorers.
type Rarity struct {
	Holders map[Unit]int
	Base    int
}

// Repository is the achievements' storage.
type Repository interface {
	// Counters reads every counter for userID. keeperScore is the city score
	// the Keeper rank starts at.
	Counters(ctx context.Context, userID uuid.UUID, keeperScore int64) (map[Counter]int64, error)
	// Earned lists what userID holds, and whether their past was evaluated.
	Earned(ctx context.Context, userID uuid.UUID) ([]Earned, bool, error)
	// Grant records units and returns those that were new. A backfill grant
	// also marks the user's past evaluated, even with no units.
	Grant(ctx context.Context, userID uuid.UUID, units []Unit, source Source) ([]Unit, error)
	// MarkSeen marks the user's units of ids seen; no ids marks all.
	MarkSeen(ctx context.Context, userID uuid.UUID, ids []string) error
	// Rarity reads the last refreshed holder counts.
	Rarity(ctx context.Context) (Rarity, error)
	// RefreshRarity recounts holders, and the base of people with any
	// exploration since since. It reports when the counts were last
	// refreshed, and does nothing when that is after notBefore.
	RefreshRarity(ctx context.Context, since, notBefore time.Time) (bool, error)
	// Unbackfilled lists users whose past has not been evaluated.
	Unbackfilled(ctx context.Context, limit int) ([]uuid.UUID, error)
	// Timezone is the user's zone ("UTC" when unknown).
	Timezone(ctx context.Context, userID uuid.UUID) (string, error)
	// ClaimPushSlot takes the user's once-a-day slot for kind on localDate.
	ClaimPushSlot(ctx context.Context, userID uuid.UUID, kind string, localDate time.Time) (bool, error)
}

type pgRepository struct {
	db *pgxpool.Pool
}

// NewRepository returns the Postgres repository.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepository{db: db}
}

// counterSQL counts each counter over the user's own rows; $1 is the user and
// $2 the Keeper city score. Every query is a scalar so they run as one
// statement. Night visits are not here: they depend on the user's zone (see
// nightVisitsSQL).
var counterSQL = []struct {
	c   Counter
	sql string
}{
	{CountCities, `SELECT COUNT(DISTINCT lower(btrim(city_name))) FROM user_visited_cities
		WHERE user_id = $1 AND btrim(city_name) <> ''`},
	// By name, which most rows have; the code only where the name is missing.
	{CountCountries, `SELECT COUNT(DISTINCT COALESCE(NULLIF(lower(btrim(country)), ''), lower(btrim(country_code))))
		FROM user_visited_cities
		WHERE user_id = $1 AND (btrim(COALESCE(country_code, '')) <> '' OR btrim(COALESCE(country, '')) <> '')`},
	{CountPlaces, `SELECT COUNT(DISTINCT poi_id) FROM user_visited_pois WHERE user_id = $1`},
	{CountVerifiedVisits, `SELECT COUNT(*) FROM points_events WHERE user_id = $1 AND kind = 3`},
	{CountNeighborhoods, `SELECT COUNT(*) FROM points_events WHERE user_id = $1 AND kind = 12`},
	{CountDays, `SELECT COUNT(*) FROM points_events WHERE user_id = $1 AND kind = 7`},
	{CountStops, `SELECT COUNT(*) FROM points_events WHERE user_id = $1 AND kind = 11`},
	{CountTrips, `SELECT COUNT(*) FROM points_events WHERE user_id = $1 AND kind = 8`},
	{CountDiaries, `SELECT COUNT(*) FROM trip_day_diaries d JOIN trips t ON t.id = d.trip_id
		WHERE t.user_id = $1 AND btrim(d.body) <> ''`},
	{CountPhotos, `SELECT COUNT(*) FROM trip_photos WHERE user_id = $1 AND status = 'ready'`},
	{CountReviews, `SELECT COUNT(*) FROM reviews WHERE user_id = $1 AND is_published`},
	{CountHelpfuls, `SELECT COUNT(*) FROM review_helpfuls h JOIN reviews r ON r.id = h.review_id
		WHERE r.user_id = $1 AND h.user_id <> $1 AND h.is_helpful`},
	{CountClaims, `SELECT COUNT(*) FROM place_claims WHERE user_id = $1 AND status = 'accepted'`},
	{CountSaves, `SELECT COUNT(*) FROM user_favorites WHERE user_id = $1`},
	{CountPosts, `SELECT (SELECT COUNT(*) FROM board_posts WHERE author_id = $1 AND deleted_at IS NULL)
		+ (SELECT COUNT(*) FROM board_comments WHERE author_id = $1 AND deleted_at IS NULL)`},
	{CountWeekStreak, `SELECT COALESCE(MAX(longest_week_streak), 0) FROM user_progress WHERE user_id = $1`},
	// A finished trip is 'trip:<id>' in the ledger.
	{CountGrandTours, `SELECT COUNT(*) FROM points_events pe
		WHERE pe.user_id = $1 AND pe.kind = 8
		  AND (SELECT COUNT(*) FROM trip_cities tc WHERE tc.trip_id::text = substr(pe.ref_key, 6)) >= 3`},
	{CountSubmissions, `SELECT COUNT(*) FROM place_submissions WHERE user_id = $1 AND status = 'accepted'`},
	{CountPacks, `SELECT COUNT(*) FROM bundle_claims WHERE user_id = $1`},
	{CountRankings, `SELECT COUNT(*) FROM user_poi_rankings WHERE user_id = $1`},
	{CountCuratedLists, `SELECT COUNT(*) FROM lists l
		WHERE l.user_id = $1 AND (SELECT COUNT(*) FROM list_items i WHERE i.list_id = l.id) >= 5`},
	// A City Pack claim is a copy too; it counts for Pack Mule, not here.
	{CountCopied, `SELECT COUNT(*) FROM trips t
		WHERE t.user_id = $1 AND t.copied_from_trip_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM bundle_claims b WHERE b.trip_id = t.id)`},
	{CountCopiedByOthers, `SELECT COUNT(*) FROM trips o JOIN trips c ON c.copied_from_trip_id = o.id
		WHERE o.user_id = $1 AND c.user_id <> $1
		  AND NOT EXISTS (SELECT 1 FROM bundle_claims b WHERE b.trip_id = c.id)`},
	{CountInvited, `SELECT COUNT(*) FROM users WHERE invited_by_user_id = $1`},
	{CountFriends, `SELECT COUNT(*) FROM follows a
		JOIN follows b ON b.follower_id = a.followee_id AND b.followee_id = a.follower_id
		WHERE a.follower_id = $1`},
	// A finished day ('tripday:<id>') of six or more stops, every one done.
	{CountFullDays, `SELECT COUNT(*) FROM points_events pe
		JOIN trip_days d ON d.id::text = substr(pe.ref_key, 9)
		WHERE pe.user_id = $1 AND pe.kind = 7
		  AND (SELECT COUNT(*) FROM trip_stops s WHERE s.day_id = d.id) >= 6
		  AND NOT EXISTS (
		      SELECT 1 FROM trip_stops s
		      WHERE s.day_id = d.id
		        AND NOT EXISTS (SELECT 1 FROM trip_stop_marks m WHERE m.stop_id = s.id AND m.status = 2))`},
	{CountHomecomings, `SELECT COUNT(*) FROM (
		SELECT 1 FROM user_visited_pois
		WHERE user_id = $1 AND btrim(city_name) <> ''
		GROUP BY lower(btrim(city_name))
		HAVING COUNT(DISTINCT EXTRACT(YEAR FROM visited_at)) >= 3) h`},
	{CountKeeperCities, `SELECT COUNT(*) FROM field_city_totals WHERE user_id = $1 AND score >= $2`},
}

// nightVisitsSQL counts visits made on the spot between midnight and 4 a.m.
// in the user's zone. It runs on its own: a zone Postgres does not know fails
// the statement, and that should cost only this one secret.
const nightVisitsSQL = `
	SELECT COUNT(*) FROM user_visited_pois
	WHERE user_id = $1 AND source IN ('trip', 'visit_event')
	  AND EXTRACT(HOUR FROM visited_at AT TIME ZONE $2) < 4`

func counterQuery() string {
	cols := make([]string, len(counterSQL))
	for i, q := range counterSQL {
		cols[i] = "(" + q.sql + ")"
	}
	return "SELECT " + strings.Join(cols, ",\n")
}

func (r *pgRepository) Counters(ctx context.Context, userID uuid.UUID, keeperScore int64) (map[Counter]int64, error) {
	vals := make([]int64, len(counterSQL))
	dest := make([]any, len(vals))
	for i := range vals {
		dest[i] = &vals[i]
	}
	if err := r.db.QueryRow(ctx, counterQuery(), userID, keeperScore).Scan(dest...); err != nil {
		return nil, fmt.Errorf("count achievements: %w", err)
	}
	out := make(map[Counter]int64, len(vals)+1)
	for i, q := range counterSQL {
		out[q.c] = vals[i]
	}
	tz, err := r.Timezone(ctx, userID)
	if err != nil {
		return nil, err
	}
	var night int64
	if err := r.db.QueryRow(ctx, nightVisitsSQL, userID, tz).Scan(&night); err == nil {
		out[CountNightVisits] = night
	}
	return out, nil
}

func (r *pgRepository) Earned(ctx context.Context, userID uuid.UUID) ([]Earned, bool, error) {
	var backfilled bool
	err := r.db.QueryRow(ctx, `SELECT achievements_backfilled_at IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&backfilled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read backfill: %w", err)
	}
	rows, err := r.db.Query(ctx, `
		SELECT achievement_id, tier, earned_at, source, seen_at IS NOT NULL
		FROM user_achievements WHERE user_id = $1`, userID)
	if err != nil {
		return nil, false, fmt.Errorf("list achievements: %w", err)
	}
	defer rows.Close()
	var out []Earned
	for rows.Next() {
		var e Earned
		if err := rows.Scan(&e.ID, &e.Tier, &e.At, &e.Source, &e.Seen); err != nil {
			return nil, false, fmt.Errorf("scan achievement: %w", err)
		}
		out = append(out, e)
	}
	return out, backfilled, rows.Err()
}

func (r *pgRepository) Grant(ctx context.Context, userID uuid.UUID, units []Unit, source Source) ([]Unit, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var granted []Unit
	for _, u := range units {
		ct, err := tx.Exec(ctx, `
			INSERT INTO user_achievements (user_id, achievement_id, tier, source) VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`, userID, u.ID, u.Tier, source)
		if err != nil {
			return nil, fmt.Errorf("grant achievement: %w", err)
		}
		if ct.RowsAffected() == 1 {
			granted = append(granted, u)
		}
	}
	if source == SourceBackfill {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET achievements_backfilled_at = NOW()
			WHERE id = $1 AND achievements_backfilled_at IS NULL`, userID); err != nil {
			return nil, fmt.Errorf("mark backfilled: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit grant: %w", err)
	}
	return granted, nil
}

func (r *pgRepository) MarkSeen(ctx context.Context, userID uuid.UUID, ids []string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE user_achievements SET seen_at = NOW()
		WHERE user_id = $1 AND seen_at IS NULL AND (cardinality($2::text[]) = 0 OR achievement_id = ANY($2))`,
		userID, ids)
	if err != nil {
		return fmt.Errorf("mark achievements seen: %w", err)
	}
	return nil
}

func (r *pgRepository) Rarity(ctx context.Context) (Rarity, error) {
	rows, err := r.db.Query(ctx, `SELECT achievement_id, tier, holders FROM achievement_stats`)
	if err != nil {
		return Rarity{}, fmt.Errorf("read rarity: %w", err)
	}
	defer rows.Close()
	out := Rarity{Holders: map[Unit]int{}}
	for rows.Next() {
		var (
			u Unit
			n int
		)
		if err := rows.Scan(&u.ID, &u.Tier, &n); err != nil {
			return Rarity{}, fmt.Errorf("scan rarity: %w", err)
		}
		if u.ID == "" {
			out.Base = n
			continue
		}
		out.Holders[u] = n
	}
	return out, rows.Err()
}

func (r *pgRepository) RefreshRarity(ctx context.Context, since, notBefore time.Time) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin rarity: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The base row doubles as the lock: two runners never recount at once.
	if _, err := tx.Exec(ctx, `
		INSERT INTO achievement_stats (achievement_id, tier, holders, refreshed_at)
		VALUES ('', 0, 0, 'epoch') ON CONFLICT DO NOTHING`); err != nil {
		return false, fmt.Errorf("ensure rarity base: %w", err)
	}
	var last time.Time
	if err := tx.QueryRow(ctx, `
		SELECT refreshed_at FROM achievement_stats WHERE achievement_id = '' AND tier = 0 FOR UPDATE`).Scan(&last); err != nil {
		return false, fmt.Errorf("lock rarity: %w", err)
	}
	if last.After(notBefore) {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM achievement_stats WHERE achievement_id <> ''`); err != nil {
		return false, fmt.Errorf("clear rarity: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO achievement_stats (achievement_id, tier, holders)
		SELECT achievement_id, tier, COUNT(*) FROM user_achievements GROUP BY achievement_id, tier`); err != nil {
		return false, fmt.Errorf("count holders: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE achievement_stats SET refreshed_at = NOW(),
		       holders = (SELECT COUNT(DISTINCT user_id) FROM points_events WHERE created_at >= $1)
		WHERE achievement_id = '' AND tier = 0`, since); err != nil {
		return false, fmt.Errorf("count rarity base: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit rarity: %w", err)
	}
	return true, nil
}

func (r *pgRepository) Unbackfilled(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id FROM users WHERE achievements_backfilled_at IS NULL ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list unbackfilled: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (r *pgRepository) Timezone(ctx context.Context, userID uuid.UUID) (string, error) {
	tz := "UTC"
	err := r.db.QueryRow(ctx, `SELECT timezone FROM user_progress WHERE user_id = $1`, userID).Scan(&tz)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("read timezone: %w", err)
	}
	if tz == "" {
		tz = "UTC"
	}
	return tz, nil
}

func (r *pgRepository) ClaimPushSlot(ctx context.Context, userID uuid.UUID, kind string, localDate time.Time) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		INSERT INTO progress_push_log (user_id, kind, local_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		userID, kind, localDate)
	if err != nil {
		return false, fmt.Errorf("claim push slot: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}
