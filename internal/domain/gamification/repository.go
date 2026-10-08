package gamification

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

// Repository is the points layer's storage.
type Repository interface {
	// Totals is the user's progress row; zero values when they have none.
	Totals(ctx context.Context, userID uuid.UUID) (Totals, error)
	// SetTimezone records the zone the device reported.
	SetTimezone(ctx context.Context, userID uuid.UUID, tz string) error
	// Insert writes one ledger row and the totals it implies, in one
	// transaction: the lifetime total, the week's score in the event's city
	// and overall, and the city's lifetime score. inserted is false when the
	// same award already existed or the kind's daily cap was reached; then
	// nothing changes.
	Insert(ctx context.Context, e Event, dailyCap int) (inserted bool, totals Totals, err error)
	// Counts are what badges are judged on.
	Counts(ctx context.Context, userID uuid.UUID, longestStreak int) (Counts, error)
	// Badges is when each earned badge was awarded.
	Badges(ctx context.Context, userID uuid.UUID) (map[string]time.Time, error)
	// GrantBadge records a badge; false when it was already earned.
	GrantBadge(ctx context.Context, userID uuid.UUID, badgeID string) (bool, error)
	// Today is how many awards of each kind the user has on localDate.
	Today(ctx context.Context, userID uuid.UUID, localDate time.Time) (map[Kind]int, error)
	// Visible keeps the ids whose leaderboard_visible switch is on (users
	// with no settings row count as visible: the default is on).
	Visible(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	// Scores is each id's value for metric over period (zero From: all time).
	Scores(ctx context.Context, ids []uuid.UUID, metric Metric, period Period) (map[uuid.UUID]int64, error)
	// Progresses is the totals of several users.
	Progresses(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Totals, error)
	// History is the ledger, newest first, older than before (zero: from the
	// top). fieldOnly leaves out rows that count nothing.
	History(ctx context.Context, userID uuid.UUID, limit int, before time.Time, fieldOnly bool) ([]Event, error)
	// Place is a stored place, for checking a visit and naming its city and
	// neighborhood. Found is false for an unknown id.
	Place(ctx context.Context, poiID string) (Place, error)
	// SetNeighborhood records a place's neighborhood ("" when the lookup found
	// none), so it is looked up once.
	SetNeighborhood(ctx context.Context, poiID, name string) error
	// ClaimPushSlot reserves today's push of kind for userID; false when one
	// was already sent today.
	ClaimPushSlot(ctx context.Context, userID uuid.UUID, kind string, localDate time.Time) (bool, error)

	// Trip is the user's own trip with its days, stops and marks. ErrNotFound
	// when it is not theirs.
	Trip(ctx context.Context, userID, tripID uuid.UUID) (*TripState, error)
	// SetMark records a stop done or skipped, or clears it for StopOpen.
	SetMark(ctx context.Context, tripID, stopID uuid.UUID, status StopStatus) error
	// CompleteDay stamps a day finished; false when it already was.
	CompleteDay(ctx context.Context, dayID uuid.UUID) (bool, error)

	// CityByName finds a city row by name; nil when there is none.
	CityByName(ctx context.Context, name string) (*uuid.UUID, error)
	// CityName names a city row; "" when there is none.
	CityName(ctx context.Context, cityID uuid.UUID) (string, error)
	// DefaultCity is the city a board opens on: the trip covering today, else
	// the latest trip, else the latest search. nil when there is none.
	DefaultCity(ctx context.Context, userID uuid.UUID, today time.Time) (*uuid.UUID, string, error)
	// CityBoardVisible reports the user's city_board_visible switch.
	CityBoardVisible(ctx context.Context, userID uuid.UUID) (bool, error)
	// Board is one week's board: the top ten, the viewer and the row above.
	Board(ctx context.Context, q BoardQuery) (BoardPage, error)
	// LifetimeScores is each id's lifetime score in a city (uuid.Nil: overall).
	LifetimeScores(ctx context.Context, cityID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]int64, error)
	// CityTotals is the user's lifetime score per city, highest first.
	CityTotals(ctx context.Context, userID uuid.UUID) ([]CityTotal, error)
	// SeasonScores is the user's row in each season for one city (uuid.Nil:
	// overall); missing seasons are zero.
	SeasonScores(ctx context.Context, userID, cityID uuid.UUID, seasons []int) (map[int]SeasonScore, error)
	// Lifetime is the user's places kept and days finished, all time.
	Lifetime(ctx context.Context, userID uuid.UUID) (SeasonScore, error)

	// SeasonClosed reports whether a season's snapshot is final.
	SeasonClosed(ctx context.Context, seasonID int) (bool, error)
	// OpenSeasons are seasons before `before` with scores and no snapshot.
	OpenSeasons(ctx context.Context, before int) ([]int, error)
	// CloseSeason snapshots a season's final positions. Idempotent.
	CloseSeason(ctx context.Context, seasonID int) error
	// DueKeeps are places saved for the field score at least KeepAfter ago,
	// still saved, and not yet paid as kept.
	DueKeeps(ctx context.Context, limit int) ([]Keep, error)
	// PlacesWithoutNeighborhood are places on trips or visited that were never
	// looked up.
	PlacesWithoutNeighborhood(ctx context.Context, limit int) ([]PlaceRef, error)
	// NoteSources is what Loci already wrote about a saved item: its saved
	// description and, for a stored place, the place's own.
	NoteSources(ctx context.Context, userID uuid.UUID, contentType, itemID string) ([]string, error)
}

// Place is a stored place.
type Place struct {
	Found               bool
	Name                string
	HasLocation         bool
	Lat, Lon            float64
	CityID              *uuid.UUID
	CityName            string
	Neighborhood        string
	NeighborhoodChecked bool
}

// PlaceRef is a place to look a neighborhood up for.
type PlaceRef struct {
	ID       string
	Lat, Lon float64
}

// TripState is a trip as the field score sees it.
type TripState struct {
	ID       uuid.UUID
	Title    string
	CityID   *uuid.UUID
	CityName string
	Days     []DayState
}

// DayState is one day of a trip.
type DayState struct {
	ID          uuid.UUID
	Number      int
	CityID      *uuid.UUID
	CityName    string
	CompletedAt *time.Time
	Stops       []StopState
}

// StopState is one stop and how the traveller marked it.
type StopState struct {
	ID     uuid.UUID
	POIID  string
	Name   string
	Status StopStatus
}

// Marked reports whether any stop of the day has been marked.
func (d DayState) Marked() bool {
	for _, s := range d.Stops {
		if s.Status == StopDone || s.Status == StopSkipped {
			return true
		}
	}
	return false
}

// Finished reports whether every stop is done or skipped and at least one is
// done: a day skipped whole was not walked.
func (d DayState) Finished() bool {
	done := false
	for _, s := range d.Stops {
		switch s.Status {
		case StopDone:
			done = true
		case StopSkipped:
		default:
			return false
		}
	}
	return done
}

// Done reports whether every day with stops is finished. Days without stops
// (travel days) are not asked to be.
func (t TripState) Done() bool {
	any := false
	for _, d := range t.Days {
		if len(d.Stops) == 0 {
			continue
		}
		any = true
		if d.CompletedAt == nil {
			return false
		}
	}
	return any
}

// City is the day's city, falling back to the trip's.
func (t TripState) City(d DayState) (*uuid.UUID, string) {
	if d.CityID != nil {
		return d.CityID, d.CityName
	}
	return t.CityID, t.CityName
}

// BoardQuery asks for one board.
type BoardQuery struct {
	CityID   uuid.UUID // uuid.Nil: the overall rows
	SeasonID int
	Metric   FieldMetric
	Viewer   uuid.UUID
	// Among limits the board to these users (the friends board). Nil means
	// everyone on the city's board who has not opted out.
	Among []uuid.UUID
	// Snapshot reads a closed season's final positions.
	Snapshot bool
}

// BoardRow is one row of a board.
type BoardRow struct {
	UserID   uuid.UUID
	Value    int64
	Position int
}

// BoardPage is what a board shows: never the whole list.
type BoardPage struct {
	Top    []BoardRow
	Me     *BoardRow
	Above  *BoardRow
	Scored int
}

// CityTotal is a lifetime score in one city.
type CityTotal struct {
	CityID uuid.UUID
	Name   string
	Score  int64
}

// SeasonScore is one week's row.
type SeasonScore struct {
	Score        int64
	PlacesKept   int
	DaysFinished int
}

// Keep is a saved place due its kept award.
type Keep struct {
	UserID      uuid.UUID
	ContentType string
	ItemID      string
	Name        string
	CityID      *uuid.UUID
}

type pgRepository struct {
	db *pgxpool.Pool
}

// NewRepository returns the Postgres repository.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepository{db: db}
}

func (r *pgRepository) Totals(ctx context.Context, userID uuid.UUID) (Totals, error) {
	t := Totals{Timezone: "UTC"}
	err := r.db.QueryRow(ctx, `
		SELECT total_points, current_streak, longest_streak, last_active_date, timezone
		FROM user_progress WHERE user_id = $1`, userID).
		Scan(&t.TotalPoints, &t.CurrentStreak, &t.LongestStreak, &t.LastActiveDate, &t.Timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, nil
	}
	if err != nil {
		return t, fmt.Errorf("read progress: %w", err)
	}
	return t, nil
}

func (r *pgRepository) SetTimezone(ctx context.Context, userID uuid.UUID, tz string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_progress (user_id, timezone) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET timezone = EXCLUDED.timezone, updated_at = NOW()`, userID, tz)
	if err != nil {
		return fmt.Errorf("set timezone: %w", err)
	}
	return nil
}

func (r *pgRepository) Insert(ctx context.Context, e Event, dailyCap int) (bool, Totals, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, Totals{}, fmt.Errorf("begin award: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the user's progress row first, so two awards for the same user
	// serialise and the daily cap below cannot be raced past.
	t := Totals{Timezone: "UTC"}
	if _, err := tx.Exec(ctx, `INSERT INTO user_progress (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, e.UserID); err != nil {
		return false, Totals{}, fmt.Errorf("ensure progress: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT total_points, current_streak, longest_streak, last_active_date, timezone
		FROM user_progress WHERE user_id = $1 FOR UPDATE`, e.UserID).
		Scan(&t.TotalPoints, &t.CurrentStreak, &t.LongestStreak, &t.LastActiveDate, &t.Timezone); err != nil {
		return false, Totals{}, fmt.Errorf("lock progress: %w", err)
	}

	if dailyCap > 0 {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM points_events WHERE user_id = $1 AND kind = $2 AND local_date = $3`,
			e.UserID, e.Kind, e.LocalDate).Scan(&n); err != nil {
			return false, Totals{}, fmt.Errorf("count today: %w", err)
		}
		if n >= dailyCap {
			return false, t, nil
		}
	}

	ct, err := tx.Exec(ctx, `
		INSERT INTO points_events (user_id, kind, ref_key, points, field_points, label, local_date, season_id, city_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, kind, ref_key) DO NOTHING`,
		e.UserID, e.Kind, e.RefKey, e.Points, e.FieldPoints, e.Label, e.LocalDate, e.SeasonID, e.CityID)
	if err != nil {
		return false, Totals{}, fmt.Errorf("insert award: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return false, t, nil
	}

	kept, days := 0, 0
	switch e.Kind {
	case KindPlaceKept:
		kept = 1
	case KindTripDayCompleted:
		days = 1
	}
	cities := []uuid.UUID{uuid.Nil}
	if e.CityID != nil && *e.CityID != uuid.Nil {
		cities = append(cities, *e.CityID)
	}
	for _, c := range cities {
		if _, err := tx.Exec(ctx, `
			INSERT INTO field_season_scores (user_id, city_id, season_id, score, places_kept, days_finished)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id, city_id, season_id) DO UPDATE SET
				score = field_season_scores.score + EXCLUDED.score,
				places_kept = field_season_scores.places_kept + EXCLUDED.places_kept,
				days_finished = field_season_scores.days_finished + EXCLUDED.days_finished`,
			e.UserID, c, e.SeasonID, e.FieldPoints, kept, days); err != nil {
			return false, Totals{}, fmt.Errorf("update season score: %w", err)
		}
		if c == uuid.Nil {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO field_city_totals (user_id, city_id, score) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, city_id) DO UPDATE SET score = field_city_totals.score + EXCLUDED.score`,
			e.UserID, c, e.FieldPoints); err != nil {
			return false, Totals{}, fmt.Errorf("update city total: %w", err)
		}
	}

	t.TotalPoints += int64(e.FieldPoints)
	if _, err := tx.Exec(ctx, `
		UPDATE user_progress SET total_points = $2, updated_at = NOW() WHERE user_id = $1`,
		e.UserID, t.TotalPoints); err != nil {
		return false, Totals{}, fmt.Errorf("update progress: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, Totals{}, fmt.Errorf("commit award: %w", err)
	}
	return true, t, nil
}

func (r *pgRepository) Counts(ctx context.Context, userID uuid.UUID, longestStreak int) (Counts, error) {
	c := Counts{LongestStreak: longestStreak}
	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE kind = 4),
			COUNT(DISTINCT split_part(ref_key, ':', 2)) FILTER (WHERE kind = 3),
			COUNT(*) FILTER (WHERE kind = 5),
			COUNT(*) FILTER (WHERE kind = 8)
		FROM points_events WHERE user_id = $1`, userID).
		Scan(&c.Cities, &c.Places, &c.ScoutClaims, &c.TripsDone)
	if err != nil {
		return c, fmt.Errorf("count achievements: %w", err)
	}
	return c, nil
}

func (r *pgRepository) Badges(ctx context.Context, userID uuid.UUID) (map[string]time.Time, error) {
	rows, err := r.db.Query(ctx, `SELECT badge_id, awarded_at FROM user_badges WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("list badges: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var (
			id string
			at time.Time
		)
		if err := rows.Scan(&id, &at); err != nil {
			return nil, fmt.Errorf("scan badge: %w", err)
		}
		out[id] = at
	}
	return out, rows.Err()
}

func (r *pgRepository) GrantBadge(ctx context.Context, userID uuid.UUID, badgeID string) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		INSERT INTO user_badges (user_id, badge_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, badgeID)
	if err != nil {
		return false, fmt.Errorf("grant badge: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

func (r *pgRepository) Today(ctx context.Context, userID uuid.UUID, localDate time.Time) (map[Kind]int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT kind, COUNT(*) FROM points_events WHERE user_id = $1 AND local_date = $2 GROUP BY kind`,
		userID, localDate)
	if err != nil {
		return nil, fmt.Errorf("read today: %w", err)
	}
	defer rows.Close()
	out := map[Kind]int{}
	for rows.Next() {
		var (
			k Kind
			n int
		)
		if err := rows.Scan(&k, &n); err != nil {
			return nil, fmt.Errorf("scan today: %w", err)
		}
		out[k] = n
	}
	return out, rows.Err()
}

func (r *pgRepository) Visible(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, COALESCE(ns.leaderboard_visible, TRUE)
		FROM unnest($1::uuid[]) AS u(id)
		LEFT JOIN notification_settings ns ON ns.user_id = u.id`, ids)
	if err != nil {
		return nil, fmt.Errorf("read visibility: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var (
			id      uuid.UUID
			visible bool
		)
		if err := rows.Scan(&id, &visible); err != nil {
			return nil, fmt.Errorf("scan visibility: %w", err)
		}
		out[id] = visible
	}
	return out, rows.Err()
}

func (r *pgRepository) Scores(ctx context.Context, ids []uuid.UUID, metric Metric, period Period) (map[uuid.UUID]int64, error) {
	var value string
	switch metric {
	case MetricCities:
		value = "COUNT(*) FILTER (WHERE e.kind = 4)"
	case MetricPlaces:
		value = "COUNT(DISTINCT split_part(e.ref_key, ':', 2)) FILTER (WHERE e.kind = 3)"
	default:
		value = "COALESCE(SUM(e.field_points), 0)"
	}
	var from, to *time.Time
	if !period.From.IsZero() {
		from, to = &period.From, &period.To
	}
	// value is one of three constants above, never input.
	query := `
		SELECT u.id, ` + value + `
		FROM unnest($1::uuid[]) AS u(id)
		LEFT JOIN points_events e ON e.user_id = u.id
			AND ($2::date IS NULL OR e.local_date BETWEEN $2::date AND $3::date)
		GROUP BY u.id`
	rows, err := r.db.Query(ctx, query, ids, from, to)
	if err != nil {
		return nil, fmt.Errorf("score leaderboard: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]int64{}
	for rows.Next() {
		var (
			id uuid.UUID
			v  int64
		)
		if err := rows.Scan(&id, &v); err != nil {
			return nil, fmt.Errorf("scan score: %w", err)
		}
		out[id] = v
	}
	return out, rows.Err()
}

func (r *pgRepository) Progresses(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Totals, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id, total_points, current_streak, longest_streak, last_active_date, timezone
		FROM user_progress WHERE user_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("read progresses: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]Totals{}
	for rows.Next() {
		var (
			id uuid.UUID
			t  Totals
		)
		if err := rows.Scan(&id, &t.TotalPoints, &t.CurrentStreak, &t.LongestStreak, &t.LastActiveDate, &t.Timezone); err != nil {
			return nil, fmt.Errorf("scan progress: %w", err)
		}
		out[id] = t
	}
	return out, rows.Err()
}

func (r *pgRepository) History(ctx context.Context, userID uuid.UUID, limit int, before time.Time, fieldOnly bool) ([]Event, error) {
	var cursor *time.Time
	if !before.IsZero() {
		cursor = &before
	}
	rows, err := r.db.Query(ctx, `
		SELECT e.id, e.kind, e.ref_key, e.points, e.field_points, e.label, e.local_date, e.season_id,
		       e.city_id, COALESCE(c.name, ''), e.created_at
		FROM points_events e
		LEFT JOIN cities c ON c.id = e.city_id
		WHERE e.user_id = $1 AND ($2::timestamptz IS NULL OR e.created_at < $2)
		  AND (NOT $4 OR e.field_points > 0)
		ORDER BY e.created_at DESC
		LIMIT $3`, userID, cursor, limit, fieldOnly)
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e := Event{UserID: userID}
		if err := rows.Scan(&e.ID, &e.Kind, &e.RefKey, &e.Points, &e.FieldPoints, &e.Label, &e.LocalDate, &e.SeasonID,
			&e.CityID, &e.CityName, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *pgRepository) Place(ctx context.Context, poiID string) (Place, error) {
	var (
		p        Place
		lat, lon *float64
		hood     *string
		checked  *time.Time
	)
	err := r.db.QueryRow(ctx, `
		SELECT p.name, ST_Y(p.location), ST_X(p.location), p.city_id, COALESCE(c.name, ''),
		       p.neighborhood, p.neighborhood_checked_at
		FROM points_of_interest p
		LEFT JOIN cities c ON c.id = p.city_id
		WHERE p.id::text = $1`, poiID).
		Scan(&p.Name, &lat, &lon, &p.CityID, &p.CityName, &hood, &checked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Place{}, nil
	}
	if err != nil {
		return Place{}, fmt.Errorf("read place: %w", err)
	}
	p.Found = true
	if lat != nil && lon != nil {
		p.HasLocation, p.Lat, p.Lon = true, *lat, *lon
	}
	if hood != nil {
		p.Neighborhood = *hood
	}
	p.NeighborhoodChecked = checked != nil
	return p, nil
}

func (r *pgRepository) SetNeighborhood(ctx context.Context, poiID, name string) error {
	var hood *string
	if name != "" {
		hood = &name
	}
	if _, err := r.db.Exec(ctx, `
		UPDATE points_of_interest SET neighborhood = $2, neighborhood_checked_at = NOW()
		WHERE id::text = $1`, poiID, hood); err != nil {
		return fmt.Errorf("set neighborhood: %w", err)
	}
	return nil
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

func (r *pgRepository) Trip(ctx context.Context, userID, tripID uuid.UUID) (*TripState, error) {
	t := &TripState{ID: tripID}
	err := r.db.QueryRow(ctx, `
		SELECT t.title, t.city_id, COALESCE(NULLIF(c.name, ''), t.city_name)
		FROM trips t LEFT JOIN cities c ON c.id = t.city_id
		WHERE t.id = $1 AND t.user_id = $2`, tripID, userID).Scan(&t.Title, &t.CityID, &t.CityName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read trip: %w", err)
	}
	rows, err := r.db.Query(ctx, `
		SELECT d.id, d.day_number, d.city_id, COALESCE(d.city_name, ''), d.completed_at,
		       s.id, COALESCE(s.poi_id, ''), COALESCE(s.name, ''), COALESCE(m.status, 1::smallint)
		FROM trip_days d
		LEFT JOIN trip_stops s ON s.day_id = d.id
		LEFT JOIN trip_stop_marks m ON m.stop_id = s.id
		WHERE d.trip_id = $1
		ORDER BY d.day_number, s.order_index`, tripID)
	if err != nil {
		return nil, fmt.Errorf("read trip days: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			d      DayState
			stopID *uuid.UUID
			s      StopState
		)
		if err := rows.Scan(&d.ID, &d.Number, &d.CityID, &d.CityName, &d.CompletedAt,
			&stopID, &s.POIID, &s.Name, &s.Status); err != nil {
			return nil, fmt.Errorf("scan trip day: %w", err)
		}
		if n := len(t.Days); n == 0 || t.Days[n-1].ID != d.ID {
			t.Days = append(t.Days, d)
		}
		if stopID != nil {
			s.ID = *stopID
			last := &t.Days[len(t.Days)-1]
			last.Stops = append(last.Stops, s)
		}
	}
	return t, rows.Err()
}

func (r *pgRepository) SetMark(ctx context.Context, tripID, stopID uuid.UUID, status StopStatus) error {
	var err error
	if status == StopOpen {
		_, err = r.db.Exec(ctx, `DELETE FROM trip_stop_marks WHERE stop_id = $1 AND trip_id = $2`, stopID, tripID)
	} else {
		_, err = r.db.Exec(ctx, `
			INSERT INTO trip_stop_marks (stop_id, trip_id, status) VALUES ($1, $2, $3)
			ON CONFLICT (stop_id) DO UPDATE SET status = EXCLUDED.status, marked_at = NOW()
			WHERE trip_stop_marks.status <> EXCLUDED.status`, stopID, tripID, status)
	}
	if err != nil {
		return fmt.Errorf("mark stop: %w", err)
	}
	return nil
}

func (r *pgRepository) CompleteDay(ctx context.Context, dayID uuid.UUID) (bool, error) {
	ct, err := r.db.Exec(ctx, `UPDATE trip_days SET completed_at = NOW() WHERE id = $1 AND completed_at IS NULL`, dayID)
	if err != nil {
		return false, fmt.Errorf("complete day: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

func (r *pgRepository) CityByName(ctx context.Context, name string) (*uuid.UUID, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT id FROM cities WHERE lower(name) = lower($1) ORDER BY created_at LIMIT 1`, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find city: %w", err)
	}
	return &id, nil
}

func (r *pgRepository) CityName(ctx context.Context, cityID uuid.UUID) (string, error) {
	var name string
	err := r.db.QueryRow(ctx, `SELECT COALESCE(name, '') FROM cities WHERE id = $1`, cityID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read city: %w", err)
	}
	return name, nil
}

func (r *pgRepository) DefaultCity(ctx context.Context, userID uuid.UUID, today time.Time) (*uuid.UUID, string, error) {
	var (
		id   uuid.UUID
		name string
	)
	err := r.db.QueryRow(ctx, `
		SELECT c.id, COALESCE(c.name, '')
		FROM (
			SELECT t.city_id, 1 AS pri, t.updated_at AS at FROM trips t
			WHERE t.user_id = $1 AND t.city_id IS NOT NULL AND t.start_date <= $2 AND t.end_date >= $2
			UNION ALL
			SELECT t.city_id, 2, t.updated_at FROM trips t
			WHERE t.user_id = $1 AND t.city_id IS NOT NULL
			UNION ALL
			SELECT li.city_id, 3, li.created_at FROM llm_interactions li
			WHERE li.user_id = $1 AND li.city_id IS NOT NULL
		) x
		JOIN cities c ON c.id = x.city_id
		ORDER BY x.pri, x.at DESC
		LIMIT 1`, userID, today).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("default city: %w", err)
	}
	return &id, name, nil
}

func (r *pgRepository) CityBoardVisible(ctx context.Context, userID uuid.UUID) (bool, error) {
	visible := true
	err := r.db.QueryRow(ctx, `SELECT city_board_visible FROM notification_settings WHERE user_id = $1`, userID).Scan(&visible)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, fmt.Errorf("read city board switch: %w", err)
	}
	return visible, nil
}

func (r *pgRepository) Board(ctx context.Context, q BoardQuery) (BoardPage, error) {
	var value string
	switch q.Metric {
	case FieldPlacesKept:
		value = "s.places_kept"
	case FieldDaysFinished:
		value = "s.days_finished"
	default:
		value = "s.score"
	}
	table := "field_season_scores"
	if q.Snapshot {
		table = "field_season_snapshots"
	}
	// value and table are constants above, never input. On a city board the
	// viewer always sees themselves; others see them only while their
	// city_board_visible switch is on. Blocks hide both ways. RANK gives equal
	// values the same position; ROW_NUMBER is the stable order rows are
	// cut by.
	query := `
		WITH board AS (
			SELECT s.user_id, ` + value + ` AS value
			FROM ` + table + ` s
			JOIN users u ON u.id = s.user_id AND u.is_active
			LEFT JOIN notification_settings ns ON ns.user_id = s.user_id
			WHERE s.city_id = $1 AND s.season_id = $2 AND ` + value + ` > 0
			  AND ($4::uuid[] IS NULL OR s.user_id = ANY($4))
			  AND ($4::uuid[] IS NOT NULL OR s.user_id = $3 OR COALESCE(ns.city_board_visible, TRUE))
			  AND NOT EXISTS (
				SELECT 1 FROM user_blocks b
				WHERE (b.blocker_id = $3 AND b.blocked_id = s.user_id)
				   OR (b.blocker_id = s.user_id AND b.blocked_id = $3))
		), ranked AS (
			SELECT user_id, value,
			       RANK() OVER (ORDER BY value DESC) AS position,
			       ROW_NUMBER() OVER (ORDER BY value DESC, user_id) AS rn
			FROM board
		)
		SELECT user_id, value, position, rn, (SELECT COUNT(*) FROM board)
		FROM ranked
		WHERE rn <= 10 OR user_id = $3
		   OR rn = (SELECT rn - 1 FROM ranked WHERE user_id = $3)
		ORDER BY rn`
	var among []uuid.UUID
	if q.Among != nil {
		among = q.Among
	}
	rows, err := r.db.Query(ctx, query, q.CityID, q.SeasonID, q.Viewer, among)
	if err != nil {
		return BoardPage{}, fmt.Errorf("read board: %w", err)
	}
	defer rows.Close()
	type row struct {
		BoardRow
		rn int
	}
	var all []row
	page := BoardPage{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.UserID, &x.Value, &x.Position, &x.rn, &page.Scored); err != nil {
			return BoardPage{}, fmt.Errorf("scan board: %w", err)
		}
		all = append(all, x)
	}
	if err := rows.Err(); err != nil {
		return BoardPage{}, err
	}
	meRN := 0
	for _, x := range all {
		if x.UserID == q.Viewer {
			meRN = x.rn
		}
	}
	for _, x := range all {
		switch {
		case x.rn <= 10:
			page.Top = append(page.Top, x.BoardRow)
		case x.UserID == q.Viewer:
			me := x.BoardRow
			page.Me = &me
		case x.rn == meRN-1:
			above := x.BoardRow
			page.Above = &above
		}
	}
	return page, nil
}

func (r *pgRepository) LifetimeScores(ctx context.Context, cityID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	query := `SELECT user_id, score FROM field_city_totals WHERE city_id = $1 AND user_id = ANY($2)`
	args := []any{cityID, ids}
	if cityID == uuid.Nil {
		query = `SELECT user_id, total_points FROM user_progress WHERE user_id = ANY($1)`
		args = []any{ids}
	}
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read lifetime scores: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]int64{}
	for rows.Next() {
		var (
			id uuid.UUID
			v  int64
		)
		if err := rows.Scan(&id, &v); err != nil {
			return nil, fmt.Errorf("scan lifetime score: %w", err)
		}
		out[id] = v
	}
	return out, rows.Err()
}

func (r *pgRepository) CityTotals(ctx context.Context, userID uuid.UUID) ([]CityTotal, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.city_id, COALESCE(c.name, ''), t.score
		FROM field_city_totals t LEFT JOIN cities c ON c.id = t.city_id
		WHERE t.user_id = $1 AND t.score > 0
		ORDER BY t.score DESC, c.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("read city totals: %w", err)
	}
	defer rows.Close()
	var out []CityTotal
	for rows.Next() {
		var c CityTotal
		if err := rows.Scan(&c.CityID, &c.Name, &c.Score); err != nil {
			return nil, fmt.Errorf("scan city total: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *pgRepository) SeasonScores(ctx context.Context, userID, cityID uuid.UUID, seasons []int) (map[int]SeasonScore, error) {
	rows, err := r.db.Query(ctx, `
		SELECT season_id, score, places_kept, days_finished FROM field_season_scores
		WHERE user_id = $1 AND city_id = $2 AND season_id = ANY($3)`, userID, cityID, seasons)
	if err != nil {
		return nil, fmt.Errorf("read season scores: %w", err)
	}
	defer rows.Close()
	out := map[int]SeasonScore{}
	for rows.Next() {
		var (
			id int
			s  SeasonScore
		)
		if err := rows.Scan(&id, &s.Score, &s.PlacesKept, &s.DaysFinished); err != nil {
			return nil, fmt.Errorf("scan season score: %w", err)
		}
		out[id] = s
	}
	return out, rows.Err()
}

func (r *pgRepository) Lifetime(ctx context.Context, userID uuid.UUID) (SeasonScore, error) {
	var s SeasonScore
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(score), 0), COALESCE(SUM(places_kept), 0), COALESCE(SUM(days_finished), 0)
		FROM field_season_scores WHERE user_id = $1 AND city_id = $2`, userID, uuid.Nil).
		Scan(&s.Score, &s.PlacesKept, &s.DaysFinished)
	if err != nil {
		return s, fmt.Errorf("read lifetime: %w", err)
	}
	return s, nil
}

func (r *pgRepository) SeasonClosed(ctx context.Context, seasonID int) (bool, error) {
	var closed bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM field_season_closures WHERE season_id = $1)`, seasonID).Scan(&closed); err != nil {
		return false, fmt.Errorf("read season closure: %w", err)
	}
	return closed, nil
}

func (r *pgRepository) OpenSeasons(ctx context.Context, before int) ([]int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT s.season_id FROM field_season_scores s
		WHERE s.season_id < $1
		  AND NOT EXISTS (SELECT 1 FROM field_season_closures c WHERE c.season_id = s.season_id)
		ORDER BY s.season_id`, before)
	if err != nil {
		return nil, fmt.Errorf("read open seasons: %w", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan open season: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *pgRepository) CloseSeason(ctx context.Context, seasonID int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin close season: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO field_season_snapshots (season_id, city_id, user_id, score, places_kept, days_finished, position)
		SELECT season_id, city_id, user_id, score, places_kept, days_finished,
		       RANK() OVER (PARTITION BY city_id ORDER BY score DESC)
		FROM field_season_scores
		WHERE season_id = $1 AND (score > 0 OR places_kept > 0 OR days_finished > 0)
		ON CONFLICT (season_id, city_id, user_id) DO NOTHING`, seasonID); err != nil {
		return fmt.Errorf("snapshot season: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO field_season_closures (season_id) VALUES ($1) ON CONFLICT DO NOTHING`, seasonID); err != nil {
		return fmt.Errorf("close season: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit close season: %w", err)
	}
	return nil
}

func (r *pgRepository) DueKeeps(ctx context.Context, limit int) ([]Keep, error) {
	// Only places saved for the field score count as kept, so the first run
	// does not pay every bookmark made before it. A place unsaved and saved
	// again restarts its clock (added_at is the latest save).
	rows, err := r.db.Query(ctx, `
		SELECT f.user_id, f.content_type, f.item_id, COALESCE(f.item_name, ''), saved.city_id
		FROM user_favorites f
		JOIN points_events saved ON saved.user_id = f.user_id AND saved.kind = $2
		     AND saved.ref_key = 'save:' || f.content_type || ':' || f.item_id
		WHERE f.added_at <= NOW() - $3::interval
		  AND NOT EXISTS (
			SELECT 1 FROM points_events k
			WHERE k.user_id = f.user_id AND k.kind = $4
			  AND k.ref_key = 'kept:' || f.content_type || ':' || f.item_id)
		ORDER BY f.added_at
		LIMIT $1`, limit, KindPlaceSaved, fmt.Sprintf("%d seconds", int(KeepAfter.Seconds())), KindPlaceKept)
	if err != nil {
		return nil, fmt.Errorf("read due keeps: %w", err)
	}
	defer rows.Close()
	var out []Keep
	for rows.Next() {
		var k Keep
		if err := rows.Scan(&k.UserID, &k.ContentType, &k.ItemID, &k.Name, &k.CityID); err != nil {
			return nil, fmt.Errorf("scan due keep: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *pgRepository) PlacesWithoutNeighborhood(ctx context.Context, limit int) ([]PlaceRef, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.id::text, ST_Y(p.location), ST_X(p.location)
		FROM points_of_interest p
		WHERE p.neighborhood_checked_at IS NULL AND p.location IS NOT NULL
		  AND (EXISTS (SELECT 1 FROM trip_stops s WHERE s.poi_id = p.id::text)
		    OR EXISTS (SELECT 1 FROM user_visited_pois v WHERE v.poi_id = p.id::text))
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read places without neighborhood: %w", err)
	}
	defer rows.Close()
	var out []PlaceRef
	for rows.Next() {
		var p PlaceRef
		if err := rows.Scan(&p.ID, &p.Lat, &p.Lon); err != nil {
			return nil, fmt.Errorf("scan place: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *pgRepository) NoteSources(ctx context.Context, userID uuid.UUID, contentType, itemID string) ([]string, error) {
	var favDesc, poiDesc string
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(f.description, ''), COALESCE(p.description, '')
		FROM user_favorites f
		LEFT JOIN points_of_interest p ON f.content_type = 'poi' AND p.id::text = f.item_id
		WHERE f.user_id = $1 AND f.content_type = $2 AND f.item_id = $3`, userID, contentType, itemID).
		Scan(&favDesc, &poiDesc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read note sources: %w", err)
	}
	return []string{favDesc, poiDesc}, nil
}
