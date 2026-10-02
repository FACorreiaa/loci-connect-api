package tripaction

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scripted returns replies in order and records the prompts it was given.
type scripted struct {
	replies []string
	err     error
	prompts []string
}

func (s *scripted) GenerateText(_ context.Context, prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	if s.err != nil {
		return "", s.err
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	return r, nil
}

var snap = Snapshot{Cities: []string{"Lisbon"}, Days: 3, Today: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}

func TestExtract_ParsesFencedJSONAndDropsInvalid(t *testing.T) {
	gen := &scripted{replies: []string{"Sure!\n```json\n" + `{"actions":[
		{"kind":"set_dates","start_date":"2026-11-12","end_date":"2026-11-17"},
		{"kind":"search_hotels","city":"Lisbon","min_stars":4,"max_stars":4},
		{"kind":"search_hotels","city":"Paris","min_stars":4},
		{"kind":"teleport"}]}` + "\n```"}}
	got, err := Extract(context.Background(), gen, "4-star hotels, 12 to 17 Nov", snap)
	require.NoError(t, err)
	require.Len(t, got, 2, "Paris is not on the trip; teleport is not an action")
	require.Equal(t, KindSetDates, got[0].Kind)
	require.Equal(t, KindSearchHotels, got[1].Kind)
}

func TestExtract_TellsTheModelAboutTheTrip(t *testing.T) {
	gen := &scripted{replies: []string{`{"actions":[]}`}}
	_, err := Extract(context.Background(), gen, "make it longer", snap)
	require.NoError(t, err)
	p := gen.prompts[0]
	require.Contains(t, p, "Lisbon")
	require.Contains(t, p, "2026-10-01")
	require.Contains(t, p, "make it longer")
}

func TestExtract_RetriesOnceThenGivesUp(t *testing.T) {
	gen := &scripted{replies: []string{"I can't do JSON today", `{"actions":[{"kind":"regenerate_days","days":6}]}`}}
	got, err := Extract(context.Background(), gen, "make it 6 days", snap)
	require.NoError(t, err)
	require.Len(t, got, 1)

	gen = &scripted{replies: []string{"nope", "still nope"}}
	_, err = Extract(context.Background(), gen, "make it 6 days", snap)
	require.Error(t, err)
	require.Len(t, gen.prompts, 2, "one retry, not more")
}

func TestExtract_ModelDown(t *testing.T) {
	_, err := Extract(context.Background(), &scripted{err: errors.New("402 out of credits")}, "x", snap)
	require.Error(t, err)
}

func TestExtract_CapsActions(t *testing.T) {
	one := `{"kind":"regenerate_days","days":2}`
	gen := &scripted{replies: []string{`{"actions":[` + strings.Repeat(one+",", 6) + one + `]}`}}
	got, err := Extract(context.Background(), gen, "x", snap)
	require.NoError(t, err)
	require.Len(t, got, maxActions)
}

// "4 days in Rome" after a Lisbon trip is a new trip, not "re-plan Lisbon as
// 4 days". The model names the other city; extraction then proposes nothing,
// so the turn is answered as usual and the chat's new-trip hand-off runs.
func TestExtract_AnotherCityIsANewTripNotAChange(t *testing.T) {
	gen := &scripted{replies: []string{`{"other_city":"Rome","actions":[{"kind":"regenerate_days","days":4}]}`}}
	got, err := Extract(context.Background(), gen, "4 days in Rome", snap)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Contains(t, gen.prompts[0], `"other_city"`)

	for _, same := range []string{"", "lisbon", " Lisbon "} {
		gen = &scripted{replies: []string{`{"other_city":"` + same + `","actions":[{"kind":"regenerate_days","days":4}]}`}}
		got, err = Extract(context.Background(), gen, "make it 4 days", snap)
		require.NoError(t, err)
		require.Len(t, got, 1, "other_city %q is the trip's own", same)
	}
}
