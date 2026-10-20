package recap

import (
	"context"
	"testing"
	"time"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestYearAccessUnlocked(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	prod := YearAccess{}
	assert.False(t, prod.Unlocked(me, 2026, day("2026-11-30")), "the day before")
	assert.True(t, prod.Unlocked(me, 2026, day("2026-12-01")), "1 December")
	assert.True(t, prod.Unlocked(me, 2025, day("2026-03-01")), "a past year")
	assert.False(t, prod.Unlocked(me, 2027, day("2026-12-31")), "a year not started")

	early := YearAccess{UnlockOverride: day("2026-10-20"), Allow: map[uuid.UUID]bool{me: true}}
	assert.False(t, early.Unlocked(me, 2026, day("2026-10-19")))
	assert.True(t, early.Unlocked(me, 2026, day("2026-10-20")), "allowlisted, from the override")
	assert.False(t, early.Unlocked(other, 2026, day("2026-10-20")), "everyone else waits for December")
	assert.True(t, early.Unlocked(other, 2026, day("2026-12-01")))

	everyone := YearAccess{UnlockOverride: day("2026-10-20")}
	assert.True(t, everyone.Unlocked(other, 2026, day("2026-10-20")), "no allowlist: the override is for all")
}

func TestYearAccessRunnerYear(t *testing.T) {
	prod := YearAccess{}
	for _, tc := range []struct {
		today string
		year  int
		ok    bool
	}{
		{"2026-10-19", 0, false},
		{"2026-11-30", 0, false},
		{"2026-12-01", 2026, true},
		{"2027-01-31", 2026, true},
		{"2027-02-01", 0, false},
	} {
		y, ok := prod.RunnerYear(day(tc.today))
		assert.Equal(t, tc.ok, ok, tc.today)
		assert.Equal(t, tc.year, y, tc.today)
	}
	early := YearAccess{UnlockOverride: day("2026-10-20")}
	y, ok := early.RunnerYear(day("2026-10-19"))
	assert.True(t, ok, "a day early, for zones ahead of UTC")
	assert.Equal(t, 2026, y)
	_, ok = early.RunnerYear(day("2026-10-10"))
	assert.False(t, ok)
}

func TestParseYearAccess(t *testing.T) {
	a, err := ParseYearAccess("", nil)
	require.NoError(t, err)
	assert.True(t, a.UnlockOverride.IsZero())
	assert.Empty(t, a.Allow)

	id := uuid.New()
	a, err = ParseYearAccess(" 2026-10-20 ", []string{" " + id.String()})
	require.NoError(t, err)
	assert.Equal(t, day("2026-10-20"), a.UnlockOverride)
	assert.True(t, a.Allow[id])

	_, err = ParseYearAccess("20/10/2026", nil)
	require.Error(t, err)
	_, err = ParseYearAccess("", []string{"not-a-uuid"})
	require.Error(t, err)
}

func TestPercentileTopPercent(t *testing.T) {
	assert.Equal(t, 1, Percentile{Rank: 1, Active: 1000}.TopPercent())
	assert.Equal(t, 5, Percentile{Rank: 50, Active: 1000}.TopPercent())
	assert.Equal(t, 6, Percentile{Rank: 51, Active: 1000}.TopPercent(), "rounds up: never flatters")
	assert.Equal(t, 100, Percentile{Rank: 200, Active: 200}.TopPercent())
	assert.Equal(t, 0, Percentile{}.TopPercent())
}

// richYear is an eligible year with something for every card.
func richYear() *YearFacts {
	lisbonID := uuid.New()
	return &YearFacts{
		UserID: uuid.New(),
		Year:   2026,
		Trips: []YearTrip{
			{ID: uuid.New(), Start: day("2026-06-10"), Days: 5, CityName: "Madrid", CountryCode: "es"},
			{ID: uuid.New(), Start: day("2026-03-02"), Days: 4, CityName: "Porto", CountryCode: "pt"},
			{ID: uuid.New(), Start: day("2026-10-11"), Days: 5, CityName: "Lisbon", CountryCode: "pt"},
		},
		Cities: []YearCity{
			{Name: "Porto", Country: "Portugal", CountryCode: "PT", Days: 4, Places: 12, Trips: 1, Lat: 41.15, Lon: -8.61, HasCoords: true},
			{ID: &lisbonID, Name: "Lisbon", Country: "Portugal", CountryCode: "PT", Days: 9, Places: 41, Trips: 2, Lat: 38.72, Lon: -9.14, HasCoords: true},
			{Name: "Madrid", Country: "Spain", CountryCode: "ES", Days: 5, Places: 8, Trips: 1, FirstCountryVisit: true},
		},
		Places: make([]Place, 61),
		Taste: []TraitShift{
			{Key: "wine", Label: "Natural wine bars", Before: 1, Added: 4, Evidence: 9},
			{Key: "tiles", Label: "Tiled older streets", Before: 3, Added: 1, Evidence: 2},
			{Key: "clubs", Label: "Clubs", Before: 2, Added: -1},
		},
		Points:      2140,
		ActiveDates: []time.Time{day("2026-06-10"), day("2026-06-11"), day("2026-06-12"), day("2026-06-20"), day("2025-12-31"), day("2026-06-11")},
		Badges:      []string{"first_trip"},
		Contributions: Contributions{
			Reviews: 12, Lists: 3, Submissions: 2,
		},
		Percentile: &Percentile{Places: 61, Rank: 10, Active: 400},
		Owner:      Owner{DisplayName: "Ana Sousa"},
	}
}

func TestBuildYearEveryCard(t *testing.T) {
	rec, reason, ok := NewBuilder().BuildYear(richYear())
	require.True(t, ok, reason)
	assert.Equal(t, []string{
		CardIntro, CardYearMap, CardTripsTimeline, CardTopCity, CardPlaces, CardTasteShift,
		CardStreak, CardProgress, CardContributions, CardPercentile, CardOutro,
	}, cardIDs(rec))
	assert.Equal(t, recapv1.RecapKind_RECAP_KIND_YEAR, rec.GetKind())
	assert.Equal(t, "Your 2026", rec.GetTitle())
	assert.Equal(t, "2026", rec.GetPeriodLabel())
	assert.Equal(t, "3 trips · 3 cities", rec.GetSubtitle())

	cards := map[string]*recapv1.RecapCard{}
	for _, c := range rec.GetCards() {
		cards[c.GetId()] = c
	}
	in := cards[CardIntro].GetIntro()
	assert.Equal(t, "Lisbon", in.GetPrimaryCity(), "most days")
	assert.Equal(t, "'26", in.GetShortCode())
	assert.Equal(t, []string{"Lisbon", "Madrid", "Porto"}, in.GetCityNames())
	assert.EqualValues(t, 14, in.GetDayCount())

	m := cards[CardYearMap].GetYearMap()
	assert.EqualValues(t, 2, m.GetCountriesCount())
	assert.EqualValues(t, 1, m.GetNewCountriesCount())
	assert.Equal(t, "Portugal", m.GetCountries()[0].GetCountry(), "most days first")
	assert.Len(t, m.GetCities(), 2, "only cities with coordinates")
	assert.Equal(t, "Three cities in two countries, one of them new to you.", m.GetHeadline())

	tl := cards[CardTripsTimeline].GetTripsTimeline()
	assert.Len(t, tl.GetMonths(), 12)
	assert.EqualValues(t, 14, tl.GetDaysAway())
	assert.EqualValues(t, 6, tl.GetBusiestMonth())
	assert.Equal(t, "Porto", tl.GetTrips()[0].GetCityName(), "date order")
	assert.Equal(t, "3 trips, 14 days away. June was the busiest.", tl.GetHeadline())

	tc := cards[CardTopCity].GetTopCity()
	assert.Equal(t, "Lisbon", tc.GetCityName())
	assert.NotEmpty(t, tc.GetCityId())
	assert.Equal(t, "Lisbon was your city this year: 9 days and 41 places.", tc.GetHeadline())

	ts := cards[CardTasteShift].GetTasteShift()
	assert.Equal(t, "Natural wine bars", ts.GetTrait().GetLabel(), "grew most")
	assert.InDelta(t, 0.2, ts.GetStrengthStart(), 0.001)
	assert.InDelta(t, 1.0, ts.GetStrengthEnd(), 0.001)
	require.Len(t, ts.GetRunnersUp(), 1, "a trait that shrank is not on the rise")
	assert.Equal(t, "2026 was the year you got into natural wine bars.", ts.GetHeadline())

	st := cards[CardStreak].GetStreak()
	assert.EqualValues(t, 3, st.GetLongestStreakDays())
	assert.Equal(t, "2026-06-10", st.GetLongestStreakStart())
	assert.Equal(t, "2026-06-12", st.GetLongestStreakEnd())
	assert.EqualValues(t, 4, st.GetActiveDays(), "deduped, and last year's day left out")
	assert.EqualValues(t, 6, st.GetBestMonth())

	assert.Zero(t, cards[CardProgress].GetProgress().GetStreakDays(), "the streak has its own card")
	assert.Equal(t, "You gave back: 12 reviews, 3 lists and 2 places added.", cards[CardContributions].GetContributions().GetHeadline())
	p := cards[CardPercentile].GetPercentile()
	assert.EqualValues(t, 3, p.GetTopPercent())
	assert.EqualValues(t, 61, p.GetValue())
	assert.Equal(t, "See you in 2027.", cards[CardOutro].GetOutro().GetHeadline())
	assert.Equal(t, "3 cities · 3 trips · 61 places", cards[CardOutro].GetOutro().GetSummaryLine())
}

func TestBuildYearEligibility(t *testing.T) {
	b := NewBuilder()
	_, reason, ok := b.BuildYear(&YearFacts{Year: 2026, Trips: []YearTrip{{Start: day("2026-03-01"), Days: 2, CityName: "Porto"}}})
	assert.False(t, ok, "one trip is a quiet year")
	assert.Equal(t, ReasonQuietYear, reason)

	_, _, ok = b.BuildYear(nil)
	assert.False(t, ok)

	ten := &YearFacts{Year: 2026, Places: make([]Place, 10), Cities: []YearCity{{Name: "Porto", Places: 10, Days: 2}}}
	rec, _, ok := b.BuildYear(ten)
	require.True(t, ok, "ten places is enough")
	assert.Contains(t, cardIDs(rec), CardPlaces)
	assert.NotContains(t, cardIDs(rec), CardTripsTimeline, "no trips, no timeline")
	assert.NotContains(t, cardIDs(rec), CardYearMap, "a city without a country has no map")
}

func TestBuildYearPercentileThresholds(t *testing.T) {
	b := NewBuilder()
	f := richYear()
	f.Percentile = &Percentile{Places: 61, Rank: 10, Active: minPercentileActive - 1}
	rec, _, _ := b.BuildYear(f)
	assert.NotContains(t, cardIDs(rec), CardPercentile, "too few travellers to rank against")

	f.Percentile = &Percentile{Places: 61, Rank: 300, Active: 400}
	rec, _, _ = b.BuildYear(f)
	assert.NotContains(t, cardIDs(rec), CardPercentile, "top 75% is not worth a card")
}

func TestBuildYearNewTrait(t *testing.T) {
	f := richYear()
	f.Taste = []TraitShift{{Key: "jazz", Label: "Jazz bars", Added: 2, Evidence: 3}}
	rec, _, ok := NewBuilder().BuildYear(f)
	require.True(t, ok)
	for _, c := range rec.GetCards() {
		if ts := c.GetTasteShift(); ts != nil {
			assert.Equal(t, recapv1.TasteTrend_TASTE_TREND_NEW, ts.GetTrait().GetTrend())
			assert.Zero(t, ts.GetStrengthStart())
			assert.Equal(t, "2026 was the year you found jazz bars.", ts.GetHeadline())
			return
		}
	}
	t.Fatal("no taste shift card")
}

func TestYearPublicView(t *testing.T) {
	rec, _, ok := NewBuilder().BuildYear(richYear())
	require.True(t, ok)
	pub := PublicView(rec)
	assert.Equal(t, cardIDs(rec), cardIDs(pub), "every year card is safe to show in some form")
	for _, c := range pub.GetCards() {
		switch {
		case c.GetTripsTimeline() != nil:
			for _, tr := range c.GetTripsTimeline().GetTrips() {
				assert.Empty(t, tr.GetTripId())
				assert.NotEmpty(t, tr.GetCityName(), "cities stay")
			}
		case c.GetTopCity() != nil:
			assert.Empty(t, c.GetTopCity().GetCityId())
		case c.GetStreak() != nil:
			assert.Empty(t, c.GetStreak().GetLongestStreakStart())
			assert.Empty(t, c.GetStreak().GetLongestStreakEnd())
			assert.NotZero(t, c.GetStreak().GetLongestStreakDays())
		case c.GetTasteShift() != nil:
			assert.Zero(t, c.GetTasteShift().GetTrait().GetEvidenceCount())
		case c.GetYearMap() != nil:
			assert.Len(t, c.GetYearMap().GetCities(), 2)
		}
	}
	assert.Equal(t, "Ana", pub.GetOwner().GetDisplayName())
}

func TestYearInReviewLockedThenBuilt(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, "https://lociai.fyi", &fakeNotifier{}, nil)
	f := richYear()
	repo.yearFacts[f.UserID] = f
	ctx := context.Background()

	svc.now = func() time.Time { return time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC) }
	rec, err := svc.YearInReview(ctx, f.UserID, 2026)
	require.NoError(t, err)
	assert.Equal(t, recapv1.RecapStatus_RECAP_STATUS_INELIGIBLE, rec.GetStatus())
	assert.Equal(t, ReasonYearLocked, rec.GetIneligibleReason())
	assert.Zero(t, repo.yearLoads, "nothing built while locked")
	assert.Nil(t, repo.byYear(f.UserID, 2026))

	svc.now = func() time.Time { return time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC) }
	rec, err = svc.YearInReview(ctx, f.UserID, 2026)
	require.NoError(t, err)
	assert.Equal(t, recapv1.RecapStatus_RECAP_STATUS_READY, rec.GetStatus())
	assert.True(t, rec.GetIsOwner())
	assert.EqualValues(t, 2026, rec.GetYear())
	assert.NotEmpty(t, rec.GetId())
	row := repo.byYear(f.UserID, 2026)
	require.NotNil(t, row)
	assert.NotNil(t, row.NotifiedAt, "someone who asked needs no push")

	_, err = svc.YearInReview(ctx, f.UserID, 2026)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.yearLoads, "a ready year is served, not rebuilt")
}

func TestYearInReviewUsesTravellersZone(t *testing.T) {
	repo := newFakeRepo()
	repo.tz = "Pacific/Kiritimati" // UTC+14
	svc := NewService(repo, "https://lociai.fyi", &fakeNotifier{}, nil)
	f := richYear()
	repo.yearFacts[f.UserID] = f
	svc.now = func() time.Time { return time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC) }
	rec, err := svc.YearInReview(context.Background(), f.UserID, 2026)
	require.NoError(t, err)
	assert.Equal(t, recapv1.RecapStatus_RECAP_STATUS_READY, rec.GetStatus(), "already 1 December there")
}

func TestYearInReviewQuietYear(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, "https://lociai.fyi", &fakeNotifier{}, nil)
	svc.now = func() time.Time { return time.Date(2026, 12, 2, 12, 0, 0, 0, time.UTC) }
	rec, err := svc.YearInReview(context.Background(), uuid.New(), 2026)
	require.NoError(t, err)
	assert.Equal(t, recapv1.RecapStatus_RECAP_STATUS_INELIGIBLE, rec.GetStatus())
	assert.Equal(t, ReasonQuietYear, rec.GetIneligibleReason())
}

func TestTickQueuesBuildsAndPushesYear(t *testing.T) {
	repo := newFakeRepo()
	n := &fakeNotifier{}
	svc := NewService(repo, "https://lociai.fyi", n, nil)
	f := richYear()
	quiet := uuid.New()
	repo.yearFacts[f.UserID] = f
	repo.yearCands = []YearCandidate{{UserID: f.UserID, Timezone: "UTC"}, {UserID: quiet, Timezone: "UTC"}}
	ctx := context.Background()

	svc.now = func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) }
	res, err := svc.Tick(ctx)
	require.NoError(t, err)
	assert.Equal(t, TickResult{}, res, "October: no years")

	svc.now = func() time.Time { return time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC) }
	res, err = svc.Tick(ctx)
	require.NoError(t, err)
	assert.Equal(t, TickResult{Queued: 2, Built: 1, Ineligible: 1, Notified: 1}, res)
	require.Len(t, n.sent, 1)
	assert.Equal(t, f.UserID, n.sent[0].to)
	assert.Equal(t, 2026, n.sent[0].year)
	assert.Equal(t, "Your 2026 in Loci is ready", n.sent[0].title)
	assert.Equal(t, "3 cities · 3 trips · 61 places. Tap to look back on the year.", n.sent[0].body)

	res, err = svc.Tick(ctx)
	require.NoError(t, err)
	assert.Equal(t, TickResult{}, res, "nothing twice")
}

func TestTickYearEarlyOnlyForAllowlist(t *testing.T) {
	repo := newFakeRepo()
	n := &fakeNotifier{}
	f := richYear()
	other := richYear()
	svc := NewService(repo, "https://lociai.fyi", n, nil).
		WithYearAccess(YearAccess{UnlockOverride: day("2026-10-20"), Allow: map[uuid.UUID]bool{f.UserID: true}})
	repo.yearFacts[f.UserID] = f
	repo.yearFacts[other.UserID] = other
	repo.yearCands = []YearCandidate{{UserID: f.UserID, Timezone: "UTC"}, {UserID: other.UserID, Timezone: "UTC"}}
	svc.now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }

	res, err := svc.Tick(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Queued)
	assert.Equal(t, []uuid.UUID{f.UserID}, repo.yearOnlyIn[0], "candidates limited to the allowlist")
	require.Len(t, n.sent, 1)
	assert.Equal(t, f.UserID, n.sent[0].to)
	assert.Nil(t, repo.byYear(other.UserID, 2026))
}

func TestPushTextYear(t *testing.T) {
	rec := &recapv1.Recap{Kind: recapv1.RecapKind_RECAP_KIND_YEAR, Year: 2026, Title: "Your 2026"}
	title, body := PushText(rec)
	assert.Equal(t, "Your 2026 in Loci is ready", title)
	assert.Equal(t, "Tap to look back on the year.", body)
}

func TestYearPayloadRoundTrips(t *testing.T) {
	rec, _, ok := NewBuilder().BuildYear(richYear())
	require.True(t, ok)
	b, err := proto.Marshal(rec)
	require.NoError(t, err)
	back, err := decode(b)
	require.NoError(t, err)
	assert.True(t, proto.Equal(rec, back))
}
