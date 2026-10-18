package boards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/pglock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DailyBoardSlug           = "daily-destinations"
	EditorialUserID          = "00000000-0000-0000-0000-000000000001"
	DailyRunnerLockKey int64 = 0x6c6f63696461696c
)

type DailyTextGenerator interface {
	GenerateText(context.Context, string) (string, error)
}

type SeasonalCity struct {
	Name string
	Hook string
}

type DailyPopularity func(context.Context, []SeasonalCity) ([]string, error)

type dailyOutput struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type DailyRunner struct {
	repo       Repository
	locker     *pglock.Locker
	generator  DailyTextGenerator
	popularity DailyPopularity
	logger     *slog.Logger
	now        func() time.Time
	interval   time.Duration
}

func NewDailyRunner(repo Repository, locker *pglock.Locker, generator DailyTextGenerator, popularity DailyPopularity, logger *slog.Logger) *DailyRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &DailyRunner{
		repo: repo, locker: locker, generator: generator, popularity: popularity,
		logger: logger.With(slog.String("component", "daily-ai-post-runner")),
		now:    time.Now, interval: 15 * time.Minute,
	}
}

func (r *DailyRunner) Run(ctx context.Context) error {
	r.logger.Info("daily AI post runner started", slog.Duration("tick", r.interval))
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.Tick(ctx)
		}
	}
}

func (r *DailyRunner) Tick(ctx context.Context) bool {
	unlock, err := r.locker.TryLock(ctx)
	if errors.Is(err, pglock.ErrHeld) {
		return false
	}
	if err != nil {
		r.logger.WarnContext(ctx, "daily AI post runner could not take its lock", slog.Any("error", err))
		return false
	}
	defer unlock()

	if err := r.publish(ctx, r.now().UTC()); err != nil {
		r.logger.WarnContext(ctx, "daily AI post failed", slog.Any("error", err))
		return false
	}
	return true
}

func (r *DailyRunner) publish(ctx context.Context, now time.Time) error {
	cities := seasonalCities(now.Month())
	if len(cities) == 0 {
		return fmt.Errorf("no seasonal cities for month %d", now.Month())
	}
	if r.popularity != nil {
		popular, err := r.popularity(ctx, cities)
		if err != nil {
			return fmt.Errorf("find seasonal popularity: %w", err)
		}
		for _, name := range popular {
			for _, city := range cities {
				if strings.EqualFold(strings.TrimSpace(name), city.Name) {
					return r.publishWithCity(ctx, now, city)
				}
			}
		}
	}
	selected := cities[now.YearDay()%len(cities)]
	return r.publishWithCity(ctx, now, selected)
}

func (r *DailyRunner) publishWithCity(ctx context.Context, now time.Time, selected SeasonalCity) error {
	prompt := fmt.Sprintf(`Write today's Loci editorial travel post about %s, which is especially appealing for %s.
Return JSON only with exactly two string fields: "title" (short and inviting) and "body" (80-140 words).
Do not claim live prices, opening hours, or events unless they are timeless. Do not mention that you are an AI.`, selected.Name, selected.Hook)
	raw, err := r.generator.GenerateText(ctx, prompt)
	if err != nil {
		return fmt.Errorf("generate post: %w", err)
	}
	var out dailyOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return fmt.Errorf("decode generated post: %w", err)
	}
	out.Title, out.Body = strings.TrimSpace(out.Title), strings.TrimSpace(out.Body)
	if len([]rune(out.Title)) < 3 || len([]rune(out.Title)) > 200 || len([]rune(out.Body)) > 10000 {
		return fmt.Errorf("generated post is outside Boards limits")
	}
	board, err := r.repo.GetBoard(ctx, DailyBoardSlug)
	if err != nil {
		return fmt.Errorf("load daily board: %w", err)
	}
	authorID, _ := uuid.Parse(EditorialUserID)
	return r.repo.CreateAIPost(ctx, &Post{
		BoardID: board.ID, BoardSlug: board.Slug, BoardName: board.Name,
		AuthorID: authorID, Title: out.Title, Body: out.Body,
	}, now)
}

func seasonalCities(month time.Month) []SeasonalCity {
	all := map[time.Month][]SeasonalCity{
		1:  {{"Tromsø", "the northern lights"}, {"Madeira", "a mild winter escape"}, {"Seville", "the orange harvest"}},
		2:  {{"Venice", "carnival"}, {"Valencia", "Las Fallas"}, {"Marrakech", "the mild season"}},
		3:  {{"Kyoto", "early blossom"}, {"Lisbon", "the first spring light"}, {"Rome", "shoulder season"}},
		4:  {{"Amsterdam", "tulip season"}, {"Paris", "spring in the parks"}, {"Istanbul", "the tulip festival"}},
		5:  {{"Lisbon", "the jacarandas"}, {"Porto", "São João"}, {"Amalfi", "the coast before the crowds"}},
		6:  {{"Reykjavik", "the midnight sun"}, {"Stockholm", "Midsummer"}, {"Sintra", "cool hills above Lisbon"}},
		7:  {{"Dubrovnik", "the summer festival"}, {"Bergen", "long fjord days"}, {"Azores", "hydrangeas and whales"}},
		8:  {{"Edinburgh", "the Fringe"}, {"Salzburg", "the summer festival"}, {"San Sebastián", "Semana Grande and pintxos"}},
		9:  {{"Porto", "the harvest season"}, {"Munich", "Oktoberfest"}, {"Barcelona", "La Mercè"}},
		10: {{"Boston", "New England foliage"}, {"Alsace", "wine villages in autumn"}, {"Rome", "shoulder season"}},
		11: {{"Madrid", "the museum season"}, {"Bruges", "misty canals"}, {"Malta", "late sun"}},
		12: {{"Vienna", "the Christmas markets"}, {"Strasbourg", "the Christmas market"}, {"Copenhagen", "hygge season"}},
	}
	return all[month]
}

func NewDailyPopularity(pool *pgxpool.Pool) DailyPopularity {
	return func(ctx context.Context, cities []SeasonalCity) ([]string, error) {
		names := make([]string, len(cities))
		for i, city := range cities {
			names[i] = city.Name
		}
		rows, err := pool.Query(ctx, `
			SELECT city_name
			FROM chat_sessions
			WHERE created_at >= NOW() - INTERVAL '7 days'
			  AND city_name = ANY($1)
			GROUP BY city_name
			ORDER BY COUNT(*) DESC, MAX(created_at) DESC`, names)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			out = append(out, name)
		}
		return out, rows.Err()
	}
}
