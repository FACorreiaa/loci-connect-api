package trip

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/placecheck"
)

type savingRepo struct {
	Repository
	err error
}

func (r savingRepo) SaveTrip(_ context.Context, t *Trip, _ int64) (*Trip, error) {
	if r.err != nil {
		return nil, r.err
	}
	saved := *t
	saved.Version++
	return &saved, nil
}

func TestSaveNotifierTellsTheListenerAboutTheStoredTrip(t *testing.T) {
	n := NotifyOnSave(savingRepo{})
	heard := make(chan *Trip, 1)
	n.OnSave(func(t *Trip) { heard <- t })

	saved, err := n.SaveTrip(context.Background(), &Trip{ID: uuid.New(), Title: "Lisbon"}, 0)
	if err != nil || saved.Version != 1 {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	select {
	case got := <-heard:
		if got.Version != 1 || got.Title != "Lisbon" {
			t.Errorf("listener heard %+v, want the trip as stored", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listener was not told")
	}
}

func TestSaveNotifierIsSilentOnAFailedSaveAndWithNoListener(t *testing.T) {
	failing := NotifyOnSave(savingRepo{err: errors.New("conflict")})
	called := make(chan struct{}, 1)
	failing.OnSave(func(*Trip) { called <- struct{}{} })
	if _, err := failing.SaveTrip(context.Background(), &Trip{}, 0); err == nil {
		t.Fatal("want the save's error")
	}
	select {
	case <-called:
		t.Error("listener told about a save that failed")
	case <-time.After(50 * time.Millisecond):
	}

	// No listener: the save just goes through.
	if _, err := NotifyOnSave(savingRepo{}).SaveTrip(context.Background(), &Trip{}, 0); err != nil {
		t.Errorf("save with no listener: %v", err)
	}
}

func TestWarmStopChecksAsksAboutTheTripsPlaces(t *testing.T) {
	lat, lon := 38.72, -9.14
	checker := &fakeStopChecker{answers: [][]placecheck.Check{{{}}}}
	h := (&Handler{}).WithStopChecks(checker)

	h.WarmStopChecks(&Trip{ID: uuid.New(), Days: []TripDay{{
		DayNumber: 1, CityLat: &lat, CityLon: &lon,
		Stops: []TripStop{{POIID: "a", Name: "Torre de Belém"}},
	}}})

	if len(checker.got) != 1 || len(checker.got[0].Stops) != 1 || checker.got[0].Stops[0].Name != "Torre de Belém" || !checker.got[0].HasCentre {
		t.Errorf("checker asked %+v, want the day's one stop around the day's city", checker.got)
	}

	// With no checker, or no trip, it does nothing.
	(&Handler{}).WarmStopChecks(&Trip{})
	h.WarmStopChecks(nil)
}
