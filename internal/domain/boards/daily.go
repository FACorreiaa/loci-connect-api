package boards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
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
	// Country tells two cities of one name apart when looking up its page.
	Country string
	Hook    string
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
	// Every tick after the first of the day would otherwise pay for a model
	// call whose insert is then discarded by the unique index.
	done, err := r.repo.HasAIPost(ctx, now)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	cities := seasonalCities(now.Month())
	if len(cities) == 0 {
		return fmt.Errorf("no seasonal cities for month %d", now.Month())
	}
	recent, err := r.repo.RecentAIPosts(ctx, now.Add(-dailyNoRepeat))
	if err != nil {
		return fmt.Errorf("load recent daily posts: %w", err)
	}
	// Once a month's list is used up, repeating beats posting nothing.
	if unused := unusedCities(cities, recent); len(unused) > 0 {
		cities = unused
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
	// Seeded by the date, so a retried tick picks the same city.
	pick := rand.New(rand.NewPCG(uint64(now.Year()), uint64(now.YearDay()))).IntN(len(cities))
	return r.publishWithCity(ctx, now, cities[pick])
}

// dailyNoRepeat is how long a city rests after its post.
const dailyNoRepeat = 30 * 24 * time.Hour

// unusedCities drops the cities a recent post was about. The attached city
// is the record when there is one; regions like Alsace have no cities row,
// so their name in the post's text stands in.
func unusedCities(cities []SeasonalCity, recent []*Post) []SeasonalCity {
	var out []SeasonalCity
	for _, city := range cities {
		used := false
		for _, p := range recent {
			if p.Attachment != nil && strings.EqualFold(p.Attachment.Snapshot.City, city.Name) {
				used = true
				break
			}
			text := strings.ToLower(p.Title + " " + p.Body)
			if strings.Contains(text, strings.ToLower(city.Name)) {
				used = true
				break
			}
		}
		if !used {
			out = append(out, city)
		}
	}
	return out
}

// cityAttachment links the post to the city's page when Loci knows the city.
func (r *DailyRunner) cityAttachment(ctx context.Context, city SeasonalCity) (*Attachment, error) {
	id, ok, err := r.repo.CityIDByName(ctx, city.Name, city.Country)
	if err != nil || !ok {
		return nil, err
	}
	snap, err := r.repo.Snapshot(ctx, KindCity, id, uuid.Nil)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Attachment{Kind: KindCity, Ref: id.String(), Snapshot: snap}, nil
}

func (r *DailyRunner) publishWithCity(ctx context.Context, now time.Time, selected SeasonalCity) error {
	prompt := fmt.Sprintf(`Write today's Loci editorial travel post about %s, %s, which is especially appealing in %s for %s.
Return JSON only with exactly two string fields: "title" (short and inviting) and "body" (80-140 words).
Do not claim live prices, opening hours, or events unless they are timeless. Do not mention that you are an AI.`,
		selected.Name, selected.Country, now.Month(), selected.Hook)
	raw, err := r.generator.GenerateText(ctx, prompt)
	if err != nil {
		return fmt.Errorf("generate post: %w", err)
	}
	// Read the first JSON object, fenced or not: not every model in the
	// chain honours "JSON only".
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return errors.New("decode generated post: no JSON object in the reply")
	}
	var out dailyOutput
	if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err != nil {
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
	attachment, err := r.cityAttachment(ctx, selected)
	if err != nil {
		return fmt.Errorf("attach %s: %w", selected.Name, err)
	}
	authorID, _ := uuid.Parse(EditorialUserID)
	return r.repo.CreateAIPost(ctx, &Post{
		BoardID: board.ID, BoardSlug: board.Slug, BoardName: board.Name,
		AuthorID: authorID, Title: out.Title, Body: out.Body, Attachment: attachment,
	}, now)
}

// seasonalCities is the month's shortlist: places at their best that month,
// from both hemispheres, so the board is not a year of Europe.
func seasonalCities(month time.Month) []SeasonalCity {
	all := map[time.Month][]SeasonalCity{
		1: {
			{"Tromsø", "Norway", "the northern lights"},
			{"Funchal", "Portugal", "a mild winter escape"},
			{"Seville", "Spain", "the orange harvest"},
			{"Cape Town", "South Africa", "high summer"},
			{"Buenos Aires", "Argentina", "long summer evenings"},
			{"Queenstown", "New Zealand", "alpine summer"},
			{"Hanoi", "Vietnam", "the cool dry season"},
			{"Bangkok", "Thailand", "the cool season"},
		},
		2: {
			{"Venice", "Italy", "carnival"},
			{"Rio de Janeiro", "Brazil", "carnival"},
			{"Marrakech", "Morocco", "the mild season"},
			{"Sapporo", "Japan", "the snow festival"},
			{"Chiang Mai", "Thailand", "the cool dry season"},
			{"Melbourne", "Australia", "late summer"},
			{"Oaxaca", "Mexico", "the dry season"},
			{"Lisbon", "Portugal", "almond blossom and quiet streets"},
		},
		3: {
			{"Valencia", "Spain", "Las Fallas"},
			{"Kyoto", "Japan", "early blossom"},
			{"Rome", "Italy", "shoulder season"},
			{"Dublin", "Ireland", "St Patrick's Day"},
			{"Washington", "United States", "the cherry blossoms"},
			{"Mexico City", "Mexico", "the jacarandas"},
			{"Jaipur", "India", "the weeks before the heat"},
			{"Porto", "Portugal", "the first spring light"},
		},
		4: {
			{"Amsterdam", "Netherlands", "tulip season"},
			{"Paris", "France", "spring in the parks"},
			{"Istanbul", "Turkey", "the tulip festival"},
			{"Seoul", "South Korea", "cherry blossoms"},
			{"Seville", "Spain", "the April fair"},
			{"Tokyo", "Japan", "late blossom"},
			{"Granada", "Spain", "spring before the heat"},
			{"Budapest", "Hungary", "the thermal baths in spring"},
		},
		5: {
			{"Lisbon", "Portugal", "the jacarandas"},
			{"Amalfi", "Italy", "the coast before the crowds"},
			{"Prague", "Czechia", "spring evenings"},
			{"Kraków", "Poland", "the old town in bloom"},
			{"Florence", "Italy", "the galleries before summer"},
			{"Athens", "Greece", "the ruins before the heat"},
			{"Vancouver", "Canada", "mountains and sea in spring"},
			{"Palma", "Spain", "the island before summer"},
		},
		6: {
			{"Reykjavik", "Iceland", "the midnight sun"},
			{"Stockholm", "Sweden", "Midsummer"},
			{"Porto", "Portugal", "São João"},
			{"Cusco", "Peru", "Inti Raymi"},
			{"Sintra", "Portugal", "cool hills above Lisbon"},
			{"Montreal", "Canada", "the jazz festival"},
			{"Tallinn", "Estonia", "white nights"},
			{"Helsinki", "Finland", "white nights"},
		},
		7: {
			{"Dubrovnik", "Croatia", "the summer festival"},
			{"Bergen", "Norway", "long fjord days"},
			{"Azores", "Portugal", "hydrangeas and whales"},
			{"Avignon", "France", "the theatre festival"},
			{"Quebec City", "Canada", "the summer festival"},
			{"Pamplona", "Spain", "San Fermín"},
			{"Ljubljana", "Slovenia", "summer by the river"},
			{"Queenstown", "New Zealand", "the ski season"},
		},
		8: {
			{"Edinburgh", "United Kingdom", "the Fringe"},
			{"Salzburg", "Austria", "the summer festival"},
			{"San Sebastián", "Spain", "Semana Grande and pintxos"},
			{"Split", "Croatia", "late summer by the sea"},
			{"Galway", "Ireland", "the races and long evenings"},
			{"Nairobi", "Kenya", "the great migration"},
			{"Bordeaux", "France", "the vineyards before harvest"},
			{"Copenhagen", "Denmark", "summer swims in the harbour"},
		},
		9: {
			{"Porto", "Portugal", "the Douro harvest"},
			{"Munich", "Germany", "Oktoberfest"},
			{"Barcelona", "Spain", "La Mercè"},
			{"Berlin", "Germany", "late-summer nights"},
			{"Lyon", "France", "the start of the food season"},
			{"New York", "United States", "early autumn"},
			{"Florence", "Italy", "the Chianti harvest"},
			{"Tbilisi", "Georgia", "the wine harvest"},
		},
		10: {
			{"Boston", "United States", "New England foliage"},
			{"Alsace", "France", "wine villages in autumn"},
			{"Rome", "Italy", "shoulder season"},
			{"Seoul", "South Korea", "autumn colours"},
			{"Cape Town", "South Africa", "spring"},
			{"Lisbon", "Portugal", "a warm autumn"},
			{"Albuquerque", "United States", "the balloon fiesta"},
			{"Istanbul", "Turkey", "mild autumn days"},
		},
		11: {
			{"Madrid", "Spain", "the museum season"},
			{"Bruges", "Belgium", "misty canals"},
			{"Valletta", "Malta", "late sun"},
			{"Kyoto", "Japan", "autumn colours"},
			{"Buenos Aires", "Argentina", "the jacarandas"},
			{"Mexico City", "Mexico", "Día de Muertos"},
			{"Marrakech", "Morocco", "the mild season"},
			{"Sydney", "Australia", "late spring"},
		},
		12: {
			{"Vienna", "Austria", "the Christmas markets"},
			{"Strasbourg", "France", "the Christmas market"},
			{"Copenhagen", "Denmark", "hygge season"},
			{"Nuremberg", "Germany", "the Christkindlesmarkt"},
			{"Prague", "Czechia", "the Christmas markets"},
			{"Cape Town", "South Africa", "early summer"},
			{"Sydney", "Australia", "summer by the harbour"},
			{"Rovaniemi", "Finland", "the polar night"},
		},
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
