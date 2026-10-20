package tripbrief

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	"github.com/FACorreiaa/loci-connect-api/pkg/pglock"
)

// Local-hour windows: the morning brief goes from 08:00, the weather turn is
// watched through the afternoon. Nothing is sent outside 08:00–18:00.
const (
	morningFrom, morningTo = 8, 11
	weatherFrom, weatherTo = 11, 18
)

// DefaultTick is how often the runner looks for due briefs.
const DefaultTick = 5 * time.Minute

const defaultBatch = 50

// RunnerLockKey is the runner's pg advisory lock; it spells "locibrf\x00".
const RunnerLockKey int64 = 0x6c6f6369627266

// store is what the runner needs from Repository.
type store interface {
	Due(ctx context.Context, now time.Time, kind string, fromHour, toHour, limit int) ([]Candidate, error)
	Claim(ctx context.Context, c Candidate, kind string, precip *float32) (bool, error)
	MorningPrecip(ctx context.Context, userID uuid.UUID, localDate time.Time) (float32, bool, error)
	Stops(ctx context.Context, dayID uuid.UUID) ([]Stop, error)
}

// AlertSource is the local-alerts gatherer (localcontext.Gatherer).
type AlertSource interface {
	Enabled() bool
	Gather(ctx context.Context, lat, lon float64, start, end time.Time) []localcontext.Alert
}

// Sender delivers a brief (push.Notifier.TripBrief).
type Sender interface {
	TripBrief(ctx context.Context, to, tripID uuid.UUID, title, body, url string)
}

// Locker elects one runner across replicas (pglock.Locker).
type Locker interface {
	TryLock(ctx context.Context) (unlock func(), err error)
}

// Runner sends due briefs every tick. Every replica starts one; the advisory
// lock lets one of them work per tick, and the log claim keeps each brief to
// once a day even if two ever did.
type Runner struct {
	store    store
	weather  localcontext.WeatherAdapter
	alerts   AlertSource
	sender   Sender
	locker   Locker
	logger   *slog.Logger
	interval time.Duration
	batch    int
	now      func() time.Time
}

// NewRunner builds the runner. alerts may be nil.
func NewRunner(s store, weather localcontext.WeatherAdapter, alerts AlertSource, sender Sender, locker Locker, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		store: s, weather: weather, alerts: alerts, sender: sender, locker: locker, logger: logger,
		interval: DefaultTick, batch: defaultBatch, now: time.Now,
	}
}

// Run ticks until ctx is cancelled. It returns nil on cancellation.
func (r *Runner) Run(ctx context.Context) error {
	r.logger.Info("tripbrief runner started", slog.Duration("tick", r.interval))
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

// Tick runs one round and returns how many briefs it sent.
func (r *Runner) Tick(ctx context.Context) int {
	unlock, err := r.locker.TryLock(ctx)
	if errors.Is(err, pglock.ErrHeld) {
		return 0
	}
	if err != nil {
		r.logger.WarnContext(ctx, "tripbrief runner could not take its lock", slog.Any("error", err))
		return 0
	}
	defer unlock()

	now := r.now()
	forecasts := map[[2]float64]*localcontext.WeatherDay{}
	sent := r.mornings(ctx, now, forecasts) + r.weatherTurns(ctx, now, forecasts)
	if sent > 0 {
		r.logger.InfoContext(ctx, "tripbrief sent", slog.Int("count", sent))
	}
	return sent
}

func (r *Runner) mornings(ctx context.Context, now time.Time, forecasts map[[2]float64]*localcontext.WeatherDay) int {
	due, err := r.store.Due(ctx, now, KindMorning, morningFrom, morningTo, r.batch)
	if err != nil {
		r.logger.WarnContext(ctx, "tripbrief: due mornings", slog.Any("error", err))
		return 0
	}
	sent := 0
	for _, c := range due {
		wx := r.forecast(ctx, c, forecasts)
		var stops []Stop
		if c.DayID != nil {
			if stops, err = r.store.Stops(ctx, *c.DayID); err != nil {
				r.logger.WarnContext(ctx, "tripbrief: stops", slog.Any("error", err))
			}
		}
		var alerts []localcontext.Alert
		if r.alerts != nil && r.alerts.Enabled() && c.Lat != nil && c.Lon != nil {
			start := c.LocalDate
			alerts = r.alerts.Gather(ctx, *c.Lat, *c.Lon, start, start.AddDate(0, 0, 1))
		}
		b, ok := BuildMorning(c, stops, wx, alerts)
		if !ok {
			continue
		}
		var precip *float32
		if wx != nil {
			p := float32(wx.PrecipProb)
			precip = &p
		}
		if r.claimAndSend(ctx, c, KindMorning, precip, b) {
			sent++
		}
	}
	return sent
}

func (r *Runner) weatherTurns(ctx context.Context, now time.Time, forecasts map[[2]float64]*localcontext.WeatherDay) int {
	due, err := r.store.Due(ctx, now, KindWeather, weatherFrom, weatherTo, r.batch)
	if err != nil {
		r.logger.WarnContext(ctx, "tripbrief: due weather checks", slog.Any("error", err))
		return 0
	}
	sent := 0
	for _, c := range due {
		morning, found, err := r.store.MorningPrecip(ctx, c.UserID, c.LocalDate)
		if err != nil || !found {
			continue
		}
		wx := r.forecast(ctx, c, forecasts)
		if wx == nil {
			continue
		}
		b, ok := BuildWeatherTurn(c, morning, float32(wx.PrecipProb))
		if !ok {
			continue // still dry: look again next tick
		}
		p := float32(wx.PrecipProb)
		if r.claimAndSend(ctx, c, KindWeather, &p, b) {
			sent++
		}
	}
	return sent
}

func (r *Runner) claimAndSend(ctx context.Context, c Candidate, kind string, precip *float32, b Brief) bool {
	won, err := r.store.Claim(ctx, c, kind, precip)
	if err != nil {
		r.logger.WarnContext(ctx, "tripbrief: claim", slog.String("kind", kind), slog.Any("error", err))
		return false
	}
	if !won {
		return false
	}
	r.sender.TripBrief(ctx, c.UserID, c.TripID, b.Title, b.Body, b.URL)
	return true
}

// forecast is today's forecast at c's city, fetched once per place per tick.
func (r *Runner) forecast(ctx context.Context, c Candidate, cache map[[2]float64]*localcontext.WeatherDay) *localcontext.WeatherDay {
	if r.weather == nil || c.Lat == nil || c.Lon == nil {
		return nil
	}
	key := [2]float64{math.Round(*c.Lat*100) / 100, math.Round(*c.Lon*100) / 100}
	if wx, ok := cache[key]; ok {
		return wx
	}
	var wx *localcontext.WeatherDay
	days, err := r.weather.Forecast(ctx, key[0], key[1], 1)
	if err != nil {
		r.logger.WarnContext(ctx, "tripbrief: forecast", slog.String("error", localcontext.RedactErr(err)))
	} else if len(days) > 0 {
		wx = &days[0]
	}
	cache[key] = wx
	return wx
}
