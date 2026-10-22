package social

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type feedRepository struct {
	db *pgxpool.Pool
}

// NewFeedRepository is the Postgres FeedRepository.
func NewFeedRepository(db *pgxpool.Pool) FeedRepository {
	return &feedRepository{db: db}
}

func (r *feedRepository) Post(ctx context.Context, e FeedEvent) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("encode feed payload: %w", err)
	}
	// A better achievement the same day takes the day's place; anything else
	// happens once.
	onConflict := `DO NOTHING`
	if e.Rank > 0 {
		onConflict = `DO UPDATE SET payload = EXCLUDED.payload
		WHERE COALESCE((feed_events.payload->>'rank')::int, 0) < COALESCE((EXCLUDED.payload->>'rank')::int, 0)`
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO feed_events (actor_id, kind, ref_key, payload, trip_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (actor_id, kind, ref_key) `+onConflict,
		e.Actor, int16(e.Kind), e.RefKey, payload, e.TripID)
	if err != nil {
		return fmt.Errorf("write feed event: %w", err)
	}
	return nil
}

// feedFrom is the events $1 may see: those of people they follow and who are
// still active, not across a block either way, and for a trip only while the
// trip is public, or for friends and the two are friends.
const feedFrom = `
	FROM feed_events e
	JOIN follows f ON f.follower_id = $1 AND f.followee_id = e.actor_id
	JOIN users u ON u.id = e.actor_id AND u.is_active
	LEFT JOIN trips t ON t.id = e.trip_id
	LEFT JOIN cities c ON e.kind = 2 AND c.id = (e.payload->>'city_id')::uuid
	WHERE NOT EXISTS (
	        SELECT 1 FROM user_blocks b
	        WHERE (b.blocker_id = $1 AND b.blocked_id = e.actor_id)
	           OR (b.blocker_id = e.actor_id AND b.blocked_id = $1))
	  AND (e.trip_id IS NULL
	       OR t.visibility = 4
	       OR (t.visibility = 2 AND EXISTS (
	             SELECT 1 FROM follows m WHERE m.follower_id = e.actor_id AND m.followee_id = $1)))`

const feedColumns = `
	SELECT e.id, e.actor_id, e.kind, e.payload, e.trip_id, e.created_at,
	       COALESCE(t.title, ''), COALESCE(t.city_name, ''),
	       (SELECT COUNT(*) FROM trip_days d WHERE d.trip_id = e.trip_id),
	       COALESCE(c.name, e.payload->>'city_name', ''), COALESCE(c.country, ''),
	       (SELECT COUNT(*) FROM cheers ch WHERE ch.event_id = e.id),
	       EXISTS (SELECT 1 FROM cheers ch WHERE ch.event_id = e.id AND ch.user_id = $1)`

func scanFeedRow(row pgx.Row) (FeedRow, error) {
	var (
		out       FeedRow
		kind      int16
		days, cnt int64
	)
	err := row.Scan(&out.ID, &out.Actor, &kind, &out.Payload, &out.TripID, &out.CreatedAt,
		&out.Trip.Title, &out.Trip.CityName, &days, &out.CityName, &out.Country, &cnt, &out.CheeredByMe)
	out.Kind = FeedKind(kind)
	out.Trip.Days = int32(days) //nolint:gosec // days of one trip
	out.Cheers = int32(cnt)     //nolint:gosec // cheers on one event
	return out, err
}

func (r *feedRepository) Feed(ctx context.Context, viewer uuid.UUID, after *feedCursor, limit int) ([]FeedRow, error) {
	args := []any{viewer, limit}
	page := ""
	if after != nil {
		page = ` AND (e.created_at, e.id) < ($3, $4)`
		args = append(args, after.At, after.ID)
	}
	rows, err := r.db.Query(ctx, feedColumns+feedFrom+page+`
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("read feed: %w", err)
	}
	defer rows.Close()
	var out []FeedRow
	for rows.Next() {
		fr, err := scanFeedRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan feed: %w", err)
		}
		out = append(out, fr)
	}
	return out, rows.Err()
}

func (r *feedRepository) Event(ctx context.Context, viewer, id uuid.UUID) (FeedRow, error) {
	fr, err := scanFeedRow(r.db.QueryRow(ctx, feedColumns+feedFrom+` AND e.id = $2`, viewer, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return FeedRow{}, ErrNotFound
	}
	if err != nil {
		return FeedRow{}, fmt.Errorf("read feed event: %w", err)
	}
	return fr, nil
}

func (r *feedRepository) RecentCheerers(ctx context.Context, ids []uuid.UUID, per int) (map[uuid.UUID][]uuid.UUID, error) {
	out := make(map[uuid.UUID][]uuid.UUID, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT event_id, user_id FROM (
		  SELECT ch.event_id, ch.user_id,
		         row_number() OVER (PARTITION BY ch.event_id ORDER BY ch.created_at DESC, ch.user_id) AS n
		  FROM cheers ch
		  JOIN users u ON u.id = ch.user_id AND u.is_active
		  WHERE ch.event_id = ANY($1)) x
		WHERE n <= $2
		ORDER BY event_id, n`, ids, per)
	if err != nil {
		return nil, fmt.Errorf("read cheerers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ev, user uuid.UUID
		if err := rows.Scan(&ev, &user); err != nil {
			return nil, fmt.Errorf("scan cheerer: %w", err)
		}
		out[ev] = append(out[ev], user)
	}
	return out, rows.Err()
}

func (r *feedRepository) Cheer(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	ct, err := r.db.Exec(ctx, `INSERT INTO cheers (event_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, userID)
	if err != nil {
		return false, fmt.Errorf("cheer: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

func (r *feedRepository) Uncheer(ctx context.Context, id, userID uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM cheers WHERE event_id = $1 AND user_id = $2`, id, userID); err != nil {
		return fmt.Errorf("uncheer: %w", err)
	}
	return nil
}

func (r *feedRepository) CheerState(ctx context.Context, id, userID uuid.UUID) (int32, bool, error) {
	var (
		n    int64
		mine bool
	)
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(bool_or(user_id = $2), FALSE) FROM cheers WHERE event_id = $1`, id, userID).
		Scan(&n, &mine)
	if err != nil {
		return 0, false, fmt.Errorf("read cheers: %w", err)
	}
	return int32(n), mine, nil //nolint:gosec // cheers on one event
}

func (r *feedRepository) ClaimCheerNotice(ctx context.Context, id uuid.UUID) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE feed_events SET cheer_notified_at = NOW() WHERE id = $1 AND cheer_notified_at IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("claim cheer notice: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

func (r *feedRepository) Timezone(ctx context.Context, userID uuid.UUID) (string, error) {
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
