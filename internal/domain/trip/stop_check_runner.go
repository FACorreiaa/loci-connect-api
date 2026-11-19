package trip

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/placecheck"
	"github.com/FACorreiaa/loci-connect-api/pkg/pglock"
)

// StopCheckRunnerLockKey is the runner's pg advisory lock; it spells
// "locichk\x00".
const StopCheckRunnerLockKey int64 = 0x6c6f636963686b

const (
	// stopCheckTick is how often the runner looks for trips to refresh.
	stopCheckTick = time.Hour
	// stopCheckHorizon is how far ahead a trip day may be for its places to
	// be worth asking about again.
	stopCheckHorizon = 7 * 24 * time.Hour
	// stopCheckBatch is how many trips one tick reads.
	stopCheckBatch = 50
	// stopCheckPause is the gap kept between two visits to the map. The map
	// server is small and shared with people waiting on it.
	stopCheckPause = time.Second
)

// StopRefresher asks the map again about a plan's places; see
// placecheck.Planner.Refresh.
type StopRefresher interface {
	Refresh(ctx context.Context, days []placecheck.Day, allow func() bool) int
}

// TripRef names a trip and its owner.
type TripRef struct {
	ID, UserID uuid.UUID
}

// UpcomingTrips lists trips with a day inside a window, soonest first.
type UpcomingTrips interface {
	Upcoming(ctx context.Context, from, to time.Time, limit int) ([]TripRef, error)
}

// StopCheckLocker elects one runner across replicas (pglock.Locker).
type StopCheckLocker interface {
	TryLock(ctx context.Context) (unlock func(), err error)
}

// StopCheckRunner keeps the map's answers current for trips about to happen.
//
// A place's answer is kept for a month and renewed when somebody reads the
// trip. A trip planned five weeks ago and not opened since would be read on
// its first morning with answers that have aged out, shown stale while they
// are renewed behind the read. The runner renews them in the week before.
//
// It only renews answers. It offers no fixes and tells nobody: both stay on
// the owner's own read of the trip.
type StopCheckRunner struct {
	h        *Handler
	refresh  StopRefresher
	upcoming UpcomingTrips
	locker   StopCheckLocker
	log      *slog.Logger

	interval time.Duration
	pause    time.Duration
	// ceiling is how many visits to the map the runner makes in one UTC day.
	// It is the runner's own, below the budget it shares with readers, so a
	// backlog cannot spend what a person waiting on a stamp needs.
	ceiling int
	now     func() time.Time
	sleep   func(context.Context, time.Duration)

	day   string
	spent int
}

// NewStopCheckRunner builds the runner. ceiling is its visits to the map per
// UTC day; zero or less means it never asks.
func NewStopCheckRunner(h *Handler, refresh StopRefresher, upcoming UpcomingTrips, locker StopCheckLocker, ceiling int, log *slog.Logger) *StopCheckRunner {
	if log == nil {
		log = slog.Default()
	}
	return &StopCheckRunner{
		h: h, refresh: refresh, upcoming: upcoming, locker: locker, log: log,
		interval: stopCheckTick, pause: stopCheckPause, ceiling: ceiling, now: time.Now,
		sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		},
	}
}

// Run ticks until ctx is cancelled. It returns nil on cancellation.
func (r *StopCheckRunner) Run(ctx context.Context) error {
	r.log.Info("stop check runner started", slog.Duration("tick", r.interval), slog.Int("daily_ceiling", r.ceiling))
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			r.Tick(ctx)
		}
	}
}

// spentUp says whether today's visits to the map are used; a new UTC day
// starts the count again.
func (r *StopCheckRunner) spentUp() bool {
	if day := r.now().UTC().Format(time.DateOnly); day != r.day {
		r.day, r.spent = day, 0
	}
	return r.spent >= r.ceiling
}

// allow is asked before every visit to the map: it holds the runner to its
// ceiling for the day and to its pace.
func (r *StopCheckRunner) allow(ctx context.Context) bool {
	if r.spentUp() {
		return false
	}
	if r.spent > 0 {
		r.sleep(ctx, r.pause)
	}
	if ctx.Err() != nil {
		return false
	}
	r.spent++
	return true
}

// Tick runs one round and returns how many times it went to the map.
func (r *StopCheckRunner) Tick(ctx context.Context) int {
	unlock, err := r.locker.TryLock(ctx)
	if errors.Is(err, pglock.ErrHeld) {
		return 0
	}
	if err != nil {
		r.log.WarnContext(ctx, "stop check runner could not take its lock", slog.Any("error", err))
		return 0
	}
	defer unlock()

	now := r.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	refs, err := r.upcoming.Upcoming(ctx, today, today.Add(stopCheckHorizon), stopCheckBatch)
	if err != nil {
		r.log.WarnContext(ctx, "stop check runner could not list trips", slog.Any("error", err))
		return 0
	}

	visits, trips := 0, 0
	for _, ref := range refs {
		if ctx.Err() != nil || r.spentUp() {
			break
		}
		t, err := r.h.repo.GetTrip(ctx, ref.ID, ref.UserID)
		if err != nil {
			continue
		}
		// The trip as a read would answer it: the same names and the same
		// place to search around.
		draft := tripToProto(t)
		r.h.hydrateStops(ctx, draft)
		if n := r.refresh.Refresh(ctx, checkPlan(draft.GetDays()), func() bool { return r.allow(ctx) }); n > 0 {
			visits += n
			trips++
		}
	}
	if visits > 0 {
		// source tells these lookups from the ones a read sets off, which
		// log the same "place check:" lines.
		r.log.InfoContext(ctx, "place check: refreshed ahead of the trip",
			slog.String("source", "runner"), slog.Int("trips", trips), slog.Int("map_visits", visits),
			slog.Int("spent_today", r.spent), slog.Int("daily_ceiling", r.ceiling))
	}
	return visits
}

// upcomingTripStore reads UpcomingTrips from Postgres.
type upcomingTripStore struct{ db *pgxpool.Pool }

// NewUpcomingTrips builds the Postgres-backed UpcomingTrips.
func NewUpcomingTrips(db *pgxpool.Pool) UpcomingTrips { return &upcomingTripStore{db: db} }

// Upcoming lists trips with at least one day dated in [from, to), the trip
// with the soonest such day first.
func (s *upcomingTripStore) Upcoming(ctx context.Context, from, to time.Time, limit int) ([]TripRef, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.user_id
		FROM trips t
		JOIN trip_days d ON d.trip_id = t.id
		WHERE d.date >= $1 AND d.date < $2
		GROUP BY t.id, t.user_id
		ORDER BY MIN(d.date), t.id
		LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TripRef
	for rows.Next() {
		var ref TripRef
		if err := rows.Scan(&ref.ID, &ref.UserID); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
