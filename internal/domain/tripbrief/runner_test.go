package tripbrief

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
)

type fakeStore struct {
	mu       sync.Mutex
	due      map[string][]Candidate
	stops    []Stop
	claimOK  bool
	claims   []string
	precip   map[uuid.UUID]float32
	precipIn []*float32
}

func (f *fakeStore) Due(_ context.Context, _ time.Time, kind string, _, _, _ int) ([]Candidate, error) {
	return f.due[kind], nil
}

func (f *fakeStore) Claim(_ context.Context, _ Candidate, kind string, precip *float32) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = append(f.claims, kind)
	f.precipIn = append(f.precipIn, precip)
	return f.claimOK, nil
}

func (f *fakeStore) MorningPrecip(_ context.Context, user uuid.UUID, _ time.Time) (float32, bool, error) {
	p, ok := f.precip[user]
	return p, ok, nil
}

func (f *fakeStore) Stops(context.Context, uuid.UUID) ([]Stop, error) { return f.stops, nil }

type fakeWeather struct {
	day   *localcontext.WeatherDay
	err   error
	calls int
}

func (f *fakeWeather) Forecast(context.Context, float64, float64, int) ([]localcontext.WeatherDay, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.day == nil {
		return nil, nil
	}
	return []localcontext.WeatherDay{*f.day}, nil
}

type sent struct{ title, body string }

type fakeSender struct {
	mu   sync.Mutex
	sent []sent
}

func (f *fakeSender) TripBrief(_ context.Context, _, _ uuid.UUID, title, body, _ string) {
	f.mu.Lock()
	f.sent = append(f.sent, sent{title, body})
	f.mu.Unlock()
}

type openLock struct{}

func (openLock) TryLock(context.Context) (func(), error) { return func() {}, nil }

func candidate(withDay, withCoords bool) Candidate {
	c := Candidate{UserID: uuid.New(), TripID: uuid.New(), City: "Lisbon", DayNumber: 1, LocalDate: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	if withDay {
		id := uuid.New()
		c.DayID = &id
	}
	if withCoords {
		c.Lat, c.Lon = ptr(38.7), ptr(-9.1)
	}
	return c
}

func newTestRunner(store *fakeStore, wx *fakeWeather, s *fakeSender) *Runner {
	return NewRunner(store, wx, nil, s, openLock{}, nil)
}

func TestRunner_ClaimsBeforeSend(t *testing.T) {
	store := &fakeStore{due: map[string][]Candidate{KindMorning: {candidate(true, true)}}, stops: []Stop{{Name: "A"}}, claimOK: false}
	s := &fakeSender{}
	newTestRunner(store, &fakeWeather{day: &localcontext.WeatherDay{HighC: 20, Condition: "Clear", PrecipProb: 0.2}}, s).Tick(context.Background())
	require.Equal(t, []string{KindMorning}, store.claims)
	require.Empty(t, s.sent, "a lost claim sends nothing")
}

func TestRunner_MorningRecordsThePrecip(t *testing.T) {
	store := &fakeStore{due: map[string][]Candidate{KindMorning: {candidate(true, true)}}, stops: []Stop{{Name: "A"}}, claimOK: true}
	s := &fakeSender{}
	newTestRunner(store, &fakeWeather{day: &localcontext.WeatherDay{HighC: 20, Condition: "Clear", PrecipProb: 0.2}}, s).Tick(context.Background())
	require.Len(t, s.sent, 1)
	require.Equal(t, "20°C, clear · 1 stop, first A", s.sent[0].body)
	require.NotNil(t, store.precipIn[0])
	require.InDelta(t, 0.2, *store.precipIn[0], 1e-6)
}

func TestBuild_PartialData(t *testing.T) {
	t.Run("no coordinates: stops only, no forecast call", func(t *testing.T) {
		store := &fakeStore{due: map[string][]Candidate{KindMorning: {candidate(true, false)}}, stops: []Stop{{Name: "A"}}, claimOK: true}
		wx, s := &fakeWeather{}, &fakeSender{}
		newTestRunner(store, wx, s).Tick(context.Background())
		require.Zero(t, wx.calls)
		require.Equal(t, []sent{{"Day 1 in Lisbon", "1 stop, first A"}}, s.sent)
	})
	t.Run("no planned day: weather only", func(t *testing.T) {
		store := &fakeStore{due: map[string][]Candidate{KindMorning: {candidate(false, true)}}, stops: []Stop{{Name: "never read"}}, claimOK: true}
		s := &fakeSender{}
		newTestRunner(store, &fakeWeather{day: &localcontext.WeatherDay{HighC: 15, Condition: "Rain"}}, s).Tick(context.Background())
		require.Equal(t, []sent{{"Day 1 in Lisbon", "15°C, rain"}}, s.sent)
	})
	t.Run("forecast fails: stops only", func(t *testing.T) {
		store := &fakeStore{due: map[string][]Candidate{KindMorning: {candidate(true, true)}}, stops: []Stop{{Name: "A"}}, claimOK: true}
		s := &fakeSender{}
		newTestRunner(store, &fakeWeather{err: errors.New("down")}, s).Tick(context.Background())
		require.Equal(t, []sent{{"Day 1 in Lisbon", "1 stop, first A"}}, s.sent)
	})
	t.Run("nothing to say: no claim, so a later tick can try again", func(t *testing.T) {
		store := &fakeStore{due: map[string][]Candidate{KindMorning: {candidate(false, false)}}, claimOK: true}
		s := &fakeSender{}
		newTestRunner(store, &fakeWeather{}, s).Tick(context.Background())
		require.Empty(t, store.claims)
		require.Empty(t, s.sent)
	})
}

func TestRunner_WeatherTurn(t *testing.T) {
	c := candidate(true, true)
	store := &fakeStore{due: map[string][]Candidate{KindWeather: {c}}, precip: map[uuid.UUID]float32{c.UserID: 0.1}, claimOK: true}
	s := &fakeSender{}
	newTestRunner(store, &fakeWeather{day: &localcontext.WeatherDay{PrecipProb: 0.8}}, s).Tick(context.Background())
	require.Equal(t, []string{KindWeather}, store.claims)
	require.Len(t, s.sent, 1)
	require.Equal(t, "Rain likely in Lisbon", s.sent[0].title)
}

func TestRunner_WeatherStillDryClaimsNothing(t *testing.T) {
	c := candidate(true, true)
	store := &fakeStore{due: map[string][]Candidate{KindWeather: {c}}, precip: map[uuid.UUID]float32{c.UserID: 0.1}, claimOK: true}
	s := &fakeSender{}
	newTestRunner(store, &fakeWeather{day: &localcontext.WeatherDay{PrecipProb: 0.2}}, s).Tick(context.Background())
	require.Empty(t, store.claims, "checked again next tick")
	require.Empty(t, s.sent)
}

func TestRunner_OneForecastPerPlacePerTick(t *testing.T) {
	a, b := candidate(true, true), candidate(true, true)
	store := &fakeStore{due: map[string][]Candidate{KindMorning: {a, b}}, stops: []Stop{{Name: "A"}}, claimOK: true}
	wx := &fakeWeather{day: &localcontext.WeatherDay{HighC: 20}}
	newTestRunner(store, wx, &fakeSender{}).Tick(context.Background())
	require.Equal(t, 1, wx.calls)
}
