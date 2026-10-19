package boards

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// dailyRepo records editorial inserts per UTC day, the way the unique index
// on publication_date does. Cities in known have a page; the rest do not.
type dailyRepo struct {
	Repository
	posted map[string]*Post
	known  map[string]uuid.UUID
}

func (d *dailyRepo) RecentAIPosts(_ context.Context, since time.Time) ([]*Post, error) {
	var out []*Post
	for day, p := range d.posted {
		if day >= since.UTC().Format("2006-01-02") {
			out = append(out, p)
		}
	}
	return out, nil
}

func (d *dailyRepo) CityIDByName(_ context.Context, name, _ string) (uuid.UUID, bool, error) {
	id, ok := d.known[name]
	return id, ok, nil
}

func (d *dailyRepo) Snapshot(_ context.Context, _ string, ref, _ uuid.UUID) (Snapshot, error) {
	for name, id := range d.known {
		if id == ref {
			return Snapshot{Title: name, City: name}, nil
		}
	}
	return Snapshot{}, ErrNotFound
}

// knowAll gives every city in month a page.
func (d *dailyRepo) knowAll(month time.Month) {
	d.known = map[string]uuid.UUID{}
	for _, c := range seasonalCities(month) {
		d.known[c.Name] = uuid.New()
	}
}

func (d *dailyRepo) cityOn(day time.Time) string {
	p := d.posted[day.UTC().Format("2006-01-02")]
	if p == nil || p.Attachment == nil {
		return ""
	}
	return p.Attachment.Snapshot.City
}

func (d *dailyRepo) GetBoard(context.Context, string) (*Board, error) {
	return &Board{ID: uuid.New(), Slug: DailyBoardSlug, Name: "Daily destinations"}, nil
}

func (d *dailyRepo) HasAIPost(_ context.Context, date time.Time) (bool, error) {
	_, ok := d.posted[date.UTC().Format("2006-01-02")]
	return ok, nil
}

func (d *dailyRepo) CreateAIPost(_ context.Context, p *Post, date time.Time) error {
	key := date.UTC().Format("2006-01-02")
	if _, ok := d.posted[key]; !ok {
		d.posted[key] = p
	}
	return nil
}

type countingGen struct {
	reply string
	calls int
}

func (g *countingGen) GenerateText(context.Context, string) (string, error) {
	g.calls++
	return g.reply, nil
}

func TestDailyRunnerCallsTheModelOncePerDay(t *testing.T) {
	repo := &dailyRepo{posted: map[string]*Post{}}
	gen := &countingGen{reply: `{"title":"Autumn in Alsace","body":"Wine villages."}`}
	r := &DailyRunner{repo: repo, generator: gen}
	day := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)

	for i := range 4 {
		if err := r.publish(context.Background(), day.Add(time.Duration(i)*15*time.Minute)); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if gen.calls != 1 {
		t.Fatalf("model calls on one day = %d, want 1", gen.calls)
	}

	if err := r.publish(context.Background(), day.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if gen.calls != 2 || len(repo.posted) != 2 {
		t.Fatalf("next day: calls %d, posts %d; want 2 and 2", gen.calls, len(repo.posted))
	}
}

func TestDailyRunnerReadsAFencedReply(t *testing.T) {
	repo := &dailyRepo{posted: map[string]*Post{}}
	gen := &countingGen{reply: "```json\n{\"title\":\"Autumn in Alsace\",\"body\":\"Wine villages.\"}\n```"}
	r := &DailyRunner{repo: repo, generator: gen}

	if err := r.publish(context.Background(), time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	p := repo.posted["2026-10-19"]
	if p == nil || p.Title != "Autumn in Alsace" || p.Body != "Wine villages." {
		t.Fatalf("posted %+v", p)
	}
}

func TestDailyRunnerDoesNotRepeatACityWithinTheWindow(t *testing.T) {
	repo := &dailyRepo{posted: map[string]*Post{}}
	repo.knowAll(time.October)
	r := &DailyRunner{repo: repo, generator: &countingGen{reply: `{"title":"Today","body":"A city."}`}}
	day := time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	n := len(seasonalCities(time.October))

	seen := map[string]bool{}
	for i := range n {
		d := day.AddDate(0, 0, i)
		if err := r.publish(context.Background(), d); err != nil {
			t.Fatalf("day %d: %v", i, err)
		}
		city := repo.cityOn(d)
		if city == "" || seen[city] {
			t.Fatalf("day %d posted %q; seen %v", i, city, seen)
		}
		seen[city] = true
	}

	// With the month used up, the runner repeats rather than going quiet.
	last := day.AddDate(0, 0, n)
	if err := r.publish(context.Background(), last); err != nil {
		t.Fatal(err)
	}
	if repo.cityOn(last) == "" {
		t.Fatal("no post once every city was used")
	}
}

func TestDailyRunnerPrefersPopularCitiesItHasNotUsed(t *testing.T) {
	repo := &dailyRepo{posted: map[string]*Post{}}
	repo.knowAll(time.October)
	r := &DailyRunner{
		repo:      repo,
		generator: &countingGen{reply: `{"title":"Today","body":"A city."}`},
		popularity: func(_ context.Context, cities []SeasonalCity) ([]string, error) {
			var out []string
			for _, want := range []string{"Rome", "Boston"} {
				for _, c := range cities {
					if c.Name == want {
						out = append(out, want)
					}
				}
			}
			return out, nil
		},
	}
	day := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)

	for i, want := range []string{"Rome", "Boston"} {
		d := day.AddDate(0, 0, i)
		if err := r.publish(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		if got := repo.cityOn(d); got != want {
			t.Fatalf("day %d posted %q, want %q", i, got, want)
		}
	}
}

func TestDailyRunnerPostsWithoutAPageAndStillRemembersTheCity(t *testing.T) {
	repo := &dailyRepo{posted: map[string]*Post{}, known: map[string]uuid.UUID{}}
	gen := &echoGen{}
	r := &DailyRunner{repo: repo, generator: gen}
	day := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)

	if err := r.publish(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	first := repo.posted["2026-10-19"]
	if first == nil || first.Attachment != nil {
		t.Fatalf("posted %+v; want a post with no attachment", first)
	}
	if err := r.publish(context.Background(), day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if gen.cities[0] == gen.cities[1] {
		t.Fatalf("wrote about %s two days running", gen.cities[0])
	}
}

// echoGen writes a post naming the city its prompt asked about.
type echoGen struct{ cities []string }

func (g *echoGen) GenerateText(_ context.Context, prompt string) (string, error) {
	city, _, _ := strings.Cut(strings.TrimPrefix(prompt, "Write today's Loci editorial travel post about "), ",")
	g.cities = append(g.cities, city)
	return `{"title":"Autumn in ` + city + `","body":"Go to ` + city + `."}`, nil
}
