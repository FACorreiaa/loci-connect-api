package boards

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// dailyRepo records editorial inserts per UTC day, the way the unique index
// on publication_date does.
type dailyRepo struct {
	Repository
	posted map[string]*Post
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
