package preference

import (
	"context"
	"errors"
	"testing"

	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	"github.com/google/uuid"
)

type fakeScorer struct {
	matches map[string]Match
	err     error
	calls   int
	asked   []string
}

func (f *fakeScorer) ScorePOIs(_ context.Context, _ uuid.UUID, ids []string) (map[string]Match, error) {
	f.calls++
	f.asked = append(f.asked, ids...)
	return f.matches, f.err
}

func pois(ids ...string) []*poiv1.POIDetailedInfo {
	out := make([]*poiv1.POIDetailedInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, &poiv1.POIDetailedInfo{Id: id})
	}
	return out
}

func TestAnnotatePOIs_SetsMatchesAcrossGroups(t *testing.T) {
	s := &fakeScorer{matches: map[string]Match{
		"a": {Score: 0.9, Reasons: []string{"You tend to like cafe"}},
		"c": {Score: 0.2},
	}}
	first, second := pois("a", "b"), pois("c")
	AnnotatePOIs(context.Background(), s, uuid.New(), first, second)

	if got := first[0].GetTasteMatch(); got == nil || got.GetScore() != 0.9 || len(got.GetReasons()) != 1 || got.GetReasons()[0] != "You tend to like cafe" {
		t.Fatalf("a taste match = %+v", got)
	}
	if first[1].TasteMatch != nil {
		t.Fatalf("b should be unset, got %+v", first[1].TasteMatch)
	}
	if got := second[0].GetTasteMatch(); got == nil || got.GetScore() != 0.2 {
		t.Fatalf("c taste match = %+v", got)
	}
}

func TestAnnotatePOIs_NilScorerOrAnonymousIsNoop(t *testing.T) {
	list := pois("a")
	AnnotatePOIs(context.Background(), nil, uuid.New(), list)
	s := &fakeScorer{matches: map[string]Match{"a": {Score: 1}}}
	AnnotatePOIs(context.Background(), s, uuid.Nil, list)
	if list[0].TasteMatch != nil || s.calls != 0 {
		t.Fatalf("taste match = %+v, calls = %d; want none", list[0].TasteMatch, s.calls)
	}
}

func TestAnnotatePOIs_ScorerErrorLeavesUnset(t *testing.T) {
	list := pois("a")
	s := &fakeScorer{matches: map[string]Match{"a": {Score: 1}}, err: errors.New("db down")}
	AnnotatePOIs(context.Background(), s, uuid.New(), list)
	if list[0].TasteMatch != nil {
		t.Fatalf("taste match = %+v; want unset on error", list[0].TasteMatch)
	}
}

func TestAnnotatePOIs_OneCallForAllGroups(t *testing.T) {
	s := &fakeScorer{}
	AnnotatePOIs(context.Background(), s, uuid.New(), pois("a", "b"), pois("b", "c"), nil, []*poiv1.POIDetailedInfo{nil, {}})
	if s.calls != 1 {
		t.Fatalf("calls = %d, want 1", s.calls)
	}
	if len(s.asked) != 3 {
		t.Fatalf("asked = %v, want a, b, c once each", s.asked)
	}
}
