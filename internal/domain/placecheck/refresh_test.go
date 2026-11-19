package placecheck

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func yes() bool { return true }

func TestRefreshAsksOnlyForPlacesNeverLookedUpOrFoundLongAgo(t *testing.T) {
	old := *matched(always)
	old.POIID = "aged"
	old.ExpiresAt = tuesday.Add(-1)
	fresh := *matched(always)
	fresh.POIID = "fresh"
	store := &fakeStore{rows: map[string]Verification{
		"aged":  old,
		"fresh": fresh,
		// Not on the map, and that answer aged out long ago.
		"gone": {POIID: "gone", ExpiresAt: tuesday.AddDate(0, -1, 0)},
	}}
	finder := &fakeFinder{}
	days := planFrom(wednesday, []Stop{visit("aged", 600, 60), visit("fresh", 700, 60), visit("gone", 800, 60), visit("new", 900, 60)})

	visits, err := newPlanner(store, finder, nil).Refresh(context.Background(), days, yes)

	require.NoError(t, err)
	require.Equal(t, 1, visits)
	require.Equal(t, [][]string{{"aged", "new"}}, finder.asked)
}

func TestRefreshLeavesAPlanTheMapDoesNotCoverAlone(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{
		"a": {POIID: "a", ExpiresAt: tuesday.Add(-1)},
		"b": {POIID: "b", ExpiresAt: tuesday.Add(-1)},
	}}
	finder := &fakeFinder{}
	days := planFrom(wednesday, []Stop{visit("a", 600, 60)}, []Stop{visit("b", 600, 60)})

	visits, err := newPlanner(store, finder, nil).Refresh(context.Background(), days, yes)
	require.NoError(t, err)
	require.Zero(t, visits)
	require.Empty(t, finder.asked)
}

func TestRefreshSkipsDaysPastAndDaysWithNowhereToSearch(t *testing.T) {
	finder := &fakeFinder{}
	// Monday is past (today is Tuesday); the last day has no centre.
	days := planFrom(tuesday.AddDate(0, 0, -1), []Stop{visit("past", 600, 60)}, []Stop{visit("today", 600, 60)}, []Stop{visit("nowhere", 600, 60)})
	days[2].HasCentre = false

	visits, err := newPlanner(&fakeStore{rows: map[string]Verification{}}, finder, nil).Refresh(context.Background(), days, yes)

	require.NoError(t, err)
	require.Equal(t, 1, visits)
	require.Equal(t, [][]string{{"today"}}, finder.asked)
}

func TestRefreshAsksOncePerPlaceAndStopsWhenToldTo(t *testing.T) {
	finder := &fakeFinder{}
	days := planFrom(wednesday,
		[]Stop{visit("a", 600, 60)},
		[]Stop{visit("a", 600, 60)}, // the same place again: nothing left to ask
		[]Stop{visit("b", 600, 60)},
		[]Stop{visit("c", 600, 60)},
	)
	left := 2
	allow := func() bool { left--; return left >= 0 }

	visits, err := newPlanner(&fakeStore{rows: map[string]Verification{}}, finder, nil).Refresh(context.Background(), days, allow)

	require.NoError(t, err)
	require.Equal(t, 2, visits)
	require.Equal(t, [][]string{{"a"}, {"b"}}, finder.asked)
}

func TestRefreshEndsAtTheFirstFailedLookup(t *testing.T) {
	finder := &fakeFinder{err: errors.New("map is down")}
	days := planFrom(wednesday, []Stop{visit("a", 600, 60)}, []Stop{visit("b", 600, 60)})
	p := newPlanner(&fakeStore{rows: map[string]Verification{}}, finder, nil)

	visits, err := p.Refresh(context.Background(), days, yes)

	require.Error(t, err)
	require.Equal(t, 1, visits, "the failed visit is counted, and the next is not made")
	require.Equal(t, [][]string{{"a"}}, finder.asked)
}

func TestRefreshStopsWhenCancelledAndSaysWhenItCannotReadWhatIsHeld(t *testing.T) {
	days := planFrom(wednesday, []Stop{visit("a", 600, 60)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	visits, err := newPlanner(&fakeStore{rows: map[string]Verification{}}, &fakeFinder{}, nil).Refresh(ctx, days, yes)
	require.NoError(t, err)
	require.Zero(t, visits)

	_, err = newPlanner(&fakeStore{getErr: errors.New("database is away")}, &fakeFinder{}, nil).Refresh(context.Background(), days, yes)
	require.Error(t, err)
}
