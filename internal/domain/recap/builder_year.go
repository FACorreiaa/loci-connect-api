package recap

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/gamification"
)

// Year-only card ids.
const (
	CardYearMap       = "year_map"
	CardTripsTimeline = "trips_timeline"
	CardTopCity       = "top_city"
	CardTasteShift    = "taste_shift"
	CardStreak        = "streak"
	CardContributions = "contributions"
	CardPercentile    = "percentile"
)

// ReasonQuietYear is shown on a year with too little to tell.
const ReasonQuietYear = "Your year in review needs two trips, three cities or ten places. There's still time."

// Year builder limits.
const (
	maxYearCountries  = 12
	maxYearCityPoints = 40
	maxYearTrips      = 12
	maxIntroCities    = 30
	maxRunnersUp      = 2
	// minStreakActive is the fewest active days worth a streak card.
	minStreakActive = 3
)

// YearEligible reports whether a year has enough to tell.
func YearEligible(f *YearFacts) bool {
	return f != nil && (len(f.Trips) >= yearMinTrips || len(f.Cities) >= yearMinCities || len(f.Places) >= yearMinVisits)
}

// BuildYear builds a year in review. ok is false, with a reason for the
// traveller, when the year has too little to tell.
func (b *Builder) BuildYear(f *YearFacts) (rec *recapv1.Recap, reason string, ok bool) {
	if !YearEligible(f) {
		return nil, ReasonQuietYear, false
	}
	cities := citiesByDays(f.Cities)
	var top *YearCity
	if len(cities) > 0 {
		top = &cities[0]
	}

	var stats []*recapv1.RecapCard
	add := func(c *recapv1.RecapCard) {
		if c != nil {
			stats = append(stats, c)
		}
	}
	add(yearMap(cities))
	add(tripsTimeline(f.Trips))
	add(topCity(top, len(cities)))
	add(yearPlaces(f, cities))
	add(tasteShift(f.Taste, f.Year))
	add(streak(f.ActiveDates, f.Year))
	add(yearProgress(f))
	add(contributions(f.Contributions))
	add(percentile(f.Percentile))
	if len(stats) == 0 {
		return nil, ReasonQuietYear, false
	}

	start, end := yearPeriod(f.Year)
	cards := make([]*recapv1.RecapCard, 0, len(stats)+2)
	cards = append(cards, yearIntro(f, cities, top))
	cards = append(cards, stats...)
	cards = append(cards, yearOutro(f, len(cities)))

	return &recapv1.Recap{
		Kind:           recapv1.RecapKind_RECAP_KIND_YEAR,
		Year:           int32(f.Year),
		Title:          fmt.Sprintf("Your %d", f.Year),
		Subtitle:       yearSubtitle(f, len(cities)),
		PeriodStart:    start.Format(time.DateOnly),
		PeriodEnd:      end.Format(time.DateOnly),
		PeriodLabel:    strconv.Itoa(f.Year),
		Cards:          cards,
		Theme:          &recapv1.RecapTheme{},
		BuilderVersion: BuilderVersion,
		Owner: &recapv1.RecapOwner{
			UserId:      f.Owner.ID.String(),
			DisplayName: f.Owner.DisplayName,
			AvatarUrl:   f.Owner.AvatarURL,
		},
	}, "", true
}

// citiesByDays orders cities by days, then places, then name, so the top
// city and every list agree.
func citiesByDays(in []YearCity) []YearCity {
	out := append([]YearCity(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Days != out[j].Days {
			return out[i].Days > out[j].Days
		}
		if out[i].Places != out[j].Places {
			return out[i].Places > out[j].Places
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func daysAway(f *YearFacts) int {
	n := 0
	for _, t := range f.Trips {
		n += t.Days
	}
	return n
}

func yearSubtitle(f *YearFacts, cities int) string {
	var parts []string
	if len(f.Trips) > 0 {
		parts = append(parts, plural(len(f.Trips), "trip", "trips"))
	}
	if cities > 0 {
		parts = append(parts, plural(cities, "city", "cities"))
	}
	if len(parts) == 0 {
		parts = append(parts, plural(len(f.Places), "place", "places"))
	}
	return strings.Join(parts, " · ")
}

func yearIntro(f *YearFacts, cities []YearCity, top *YearCity) *recapv1.RecapCard {
	names := make([]string, 0, min(len(cities), maxIntroCities))
	for i, c := range cities {
		if i == maxIntroCities {
			break
		}
		names = append(names, c.Name)
	}
	days := daysAway(f)
	var lede string
	switch {
	case len(f.Trips) > 1 && len(cities) > 1:
		lede = fmt.Sprintf("%s trips, %s cities, one year.", capitalize(numberWord(len(f.Trips))), numberWord(len(cities)))
	case len(cities) > 1:
		lede = fmt.Sprintf("%s cities, one year.", capitalize(numberWord(len(cities))))
	default:
		lede = fmt.Sprintf("%d, place by place.", f.Year)
	}
	in := &recapv1.IntroCard{
		Lede:      lede,
		ShortCode: fmt.Sprintf("'%02d", f.Year%100),
		CityNames: names,
		DayCount:  int32(days),
	}
	if top != nil {
		in.PrimaryCity, in.Country, in.CountryCode = top.Name, top.Country, top.CountryCode
	}
	return &recapv1.RecapCard{Id: CardIntro, Card: &recapv1.RecapCard_Intro{Intro: in}}
}

func yearMap(cities []YearCity) *recapv1.RecapCard {
	if len(cities) == 0 {
		return nil
	}
	type agg struct {
		c     *recapv1.YearCountry
		order int
	}
	byCountry := map[string]*agg{}
	for _, c := range cities {
		key := strings.ToUpper(c.CountryCode)
		if key == "" {
			key = strings.ToLower(c.Country)
		}
		if key == "" {
			continue
		}
		a, ok := byCountry[key]
		if !ok {
			a = &agg{c: &recapv1.YearCountry{Country: c.Country, CountryCode: strings.ToUpper(c.CountryCode)}, order: len(byCountry)}
			byCountry[key] = a
		}
		a.c.CitiesCount++
		a.c.Days += int32(c.Days)
		a.c.FirstTime = a.c.FirstTime || c.FirstCountryVisit
		if a.c.Country == "" {
			a.c.Country = c.Country
		}
	}
	if len(byCountry) == 0 {
		// The map leads with a country count; "0 countries" is not a card.
		return nil
	}
	countries := make([]*agg, 0, len(byCountry))
	newCount := 0
	for _, a := range byCountry {
		countries = append(countries, a)
		if a.c.FirstTime {
			newCount++
		}
	}
	sort.SliceStable(countries, func(i, j int) bool {
		if countries[i].c.Days != countries[j].c.Days {
			return countries[i].c.Days > countries[j].c.Days
		}
		return countries[i].order < countries[j].order
	})
	card := &recapv1.YearMapCard{
		CountriesCount:    int32(len(countries)),
		CitiesCount:       int32(len(cities)),
		NewCountriesCount: int32(newCount),
	}
	for i, a := range countries {
		if i == maxYearCountries {
			break
		}
		card.Countries = append(card.Countries, a.c)
	}
	for _, c := range cities {
		if c.HasCoords && len(card.Cities) < maxYearCityPoints {
			card.Cities = append(card.Cities, &recapv1.RoutePoint{Latitude: c.Lat, Longitude: c.Lon, Label: c.Name})
		}
	}
	nCities := capitalize(numberWord(len(cities)))
	cityWord := "cities"
	if len(cities) == 1 {
		cityWord = "city"
	}
	switch {
	case len(countries) == 1 && card.Countries[0].GetCountry() != "":
		card.Headline = fmt.Sprintf("%s %s, all in %s.", nCities, cityWord, card.Countries[0].GetCountry())
		if newCount == 1 {
			card.Headline = fmt.Sprintf("%s %s in %s, your first time there.", nCities, cityWord, card.Countries[0].GetCountry())
		}
	case len(countries) > 1 && newCount == 1:
		card.Headline = fmt.Sprintf("%s %s in %s countries, one of them new to you.", nCities, cityWord, numberWord(len(countries)))
	case len(countries) > 1 && newCount > 1:
		card.Headline = fmt.Sprintf("%s %s in %s countries, %s of them new to you.", nCities, cityWord, numberWord(len(countries)), numberWord(newCount))
	case len(countries) > 1:
		card.Headline = fmt.Sprintf("%s %s across %s countries.", nCities, cityWord, numberWord(len(countries)))
	default:
		card.Headline = fmt.Sprintf("%s %s.", nCities, cityWord)
	}
	return &recapv1.RecapCard{Id: CardYearMap, Card: &recapv1.RecapCard_YearMap{YearMap: card}}
}

func tripsTimeline(trips []YearTrip) *recapv1.RecapCard {
	if len(trips) == 0 {
		return nil
	}
	sorted := append([]YearTrip(nil), trips...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	card := &recapv1.TripsTimelineCard{TripsCount: int32(len(sorted))}
	months := make([]*recapv1.TimelineMonth, 12)
	for i := range months {
		months[i] = &recapv1.TimelineMonth{Month: int32(i + 1)}
	}
	for _, t := range sorted {
		m := months[t.Start.Month()-1]
		m.TripsCount++
		m.Days += int32(t.Days)
		card.DaysAway += int32(t.Days)
		if len(card.Trips) < maxYearTrips {
			card.Trips = append(card.Trips, &recapv1.TimelineTrip{
				TripId:      t.ID.String(),
				Month:       int32(t.Start.Month()),
				CityName:    t.CityName,
				CountryCode: strings.ToUpper(t.CountryCode),
				DayCount:    int32(t.Days),
			})
		}
	}
	card.Months = months
	best := int32(0)
	for _, m := range months {
		if m.GetDays() > best {
			best = m.GetDays()
			card.BusiestMonth = m.GetMonth()
		}
	}
	lead := fmt.Sprintf("%s, %s away.", capitalize(plural(len(sorted), "trip", "trips")), plural(int(card.DaysAway), "day", "days"))
	if len(sorted) > 1 && card.BusiestMonth > 0 {
		lead += fmt.Sprintf(" %s was the busiest.", time.Month(card.BusiestMonth))
	}
	card.Headline = lead
	return &recapv1.RecapCard{Id: CardTripsTimeline, Card: &recapv1.RecapCard_TripsTimeline{TripsTimeline: card}}
}

func topCity(c *YearCity, cities int) *recapv1.RecapCard {
	if c == nil || (c.Days == 0 && c.Places == 0) {
		return nil
	}
	card := &recapv1.TopCityCard{
		CityName:    c.Name,
		Country:     c.Country,
		CountryCode: strings.ToUpper(c.CountryCode),
		Days:        int32(c.Days),
		PlacesCount: int32(c.Places),
		TripsCount:  int32(c.Trips),
		ImageUrl:    c.ImageURL,
	}
	if c.ID != nil {
		card.CityId = c.ID.String()
	}
	var parts []string
	if c.Days > 0 {
		parts = append(parts, plural(c.Days, "day", "days"))
	}
	if c.Places > 0 {
		parts = append(parts, plural(c.Places, "place", "places"))
	}
	what := strings.Join(parts, " and ")
	if cities > 1 {
		card.Headline = fmt.Sprintf("%s was your city this year: %s.", c.Name, what)
	} else {
		card.Headline = fmt.Sprintf("%s, all year: %s.", c.Name, what)
	}
	return &recapv1.RecapCard{Id: CardTopCity, Card: &recapv1.RecapCard_TopCity{TopCity: card}}
}

// yearPlaces is the passport page with one stamp per city.
func yearPlaces(f *YearFacts, cities []YearCity) *recapv1.RecapCard {
	if len(f.Places) == 0 {
		return nil
	}
	byPlaces := append([]YearCity(nil), cities...)
	sort.SliceStable(byPlaces, func(i, j int) bool { return byPlaces[i].Places > byPlaces[j].Places })
	card := &recapv1.PlacesCard{PlacesCount: int32(len(f.Places)), CitiesCount: int32(max(len(cities), 1))}
	for _, c := range byPlaces {
		if c.Places == 0 || len(card.Groups) == maxStamps {
			continue
		}
		card.Groups = append(card.Groups, &recapv1.PlaceGroup{Label: c.Name, PlacesCount: int32(c.Places)})
	}
	switch {
	case len(card.Groups) > 1:
		card.Headline = fmt.Sprintf("Kept across %s cities. %s took the most stamps.", numberWord(len(cities)), card.Groups[0].GetLabel())
	case len(card.Groups) == 1:
		card.Headline = fmt.Sprintf("Every one of them in %s.", card.Groups[0].GetLabel())
	}
	return &recapv1.RecapCard{Id: CardPlaces, Card: &recapv1.RecapCard_Places{Places: card}}
}

func tasteShift(traits []TraitShift, year int) *recapv1.RecapCard {
	grew := make([]TraitShift, 0, len(traits))
	maxEnd := 0.0
	for _, t := range traits {
		end := math.Max(0, t.Before) + t.Added
		maxEnd = math.Max(maxEnd, end)
		if t.Added > 0 && strings.TrimSpace(t.Label) != "" {
			grew = append(grew, t)
		}
	}
	if len(grew) == 0 || maxEnd <= 0 {
		return nil
	}
	sort.SliceStable(grew, func(i, j int) bool { return grew[i].Added > grew[j].Added })
	toTrait := func(t TraitShift) *recapv1.TasteTrait {
		end := (math.Max(0, t.Before) + t.Added) / maxEnd
		tr := recapv1.TasteTrend_TASTE_TREND_GROWING
		switch {
		case t.Before <= 0:
			tr = recapv1.TasteTrend_TASTE_TREND_NEW
		case end >= 0.6:
			tr = recapv1.TasteTrend_TASTE_TREND_STRONG
		}
		return &recapv1.TasteTrait{
			TraitKey:      t.Key,
			Label:         t.Label,
			Strength:      round2(math.Max(0.15, end)),
			EvidenceCount: int32(t.Evidence),
			Trend:         tr,
		}
	}
	top := grew[0]
	card := &recapv1.TasteShiftCard{
		Trait:         toTrait(top),
		StrengthStart: round2(math.Max(0, top.Before) / maxEnd),
		StrengthEnd:   round2((math.Max(0, top.Before) + top.Added) / maxEnd),
	}
	for _, t := range grew[1:] {
		if len(card.RunnersUp) == maxRunnersUp {
			break
		}
		card.RunnersUp = append(card.RunnersUp, toTrait(t))
	}
	if top.Before <= 0 {
		card.Headline = fmt.Sprintf("%d was the year you found %s.", year, lowerFirst(top.Label))
	} else {
		card.Headline = fmt.Sprintf("%d was the year you got into %s.", year, lowerFirst(top.Label))
	}
	return &recapv1.RecapCard{Id: CardTasteShift, Card: &recapv1.RecapCard_TasteShift{TasteShift: card}}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// streak finds the longest run of active days, and the month with the most.
func streak(dates []time.Time, year int) *recapv1.RecapCard {
	seen := map[int64]bool{}
	days := make([]int64, 0, len(dates))
	perMonth := [13]int{}
	for _, d := range dates {
		d = dateOnly(d)
		if d.Year() != year {
			continue
		}
		k := d.Unix() / 86400
		if seen[k] {
			continue
		}
		seen[k] = true
		days = append(days, k)
		perMonth[d.Month()]++
	}
	if len(days) < minStreakActive {
		return nil
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	bestLen, bestStart := 1, days[0]
	run, runStart := 1, days[0]
	for i := 1; i < len(days); i++ {
		if days[i] == days[i-1]+1 {
			run++
		} else {
			run, runStart = 1, days[i]
		}
		if run > bestLen {
			bestLen, bestStart = run, runStart
		}
	}
	day := func(k int64) time.Time { return time.Unix(k*86400, 0).UTC() }
	start, end := day(bestStart), day(bestStart+int64(bestLen-1))
	card := &recapv1.StreakCard{
		LongestStreakDays:  int32(bestLen),
		LongestStreakStart: start.Format(time.DateOnly),
		LongestStreakEnd:   end.Format(time.DateOnly),
		ActiveDays:         int32(len(days)),
	}
	best := 0
	for m := 1; m <= 12; m++ {
		if perMonth[m] > best {
			best = perMonth[m]
			card.BestMonth = int32(m)
		}
	}
	if bestLen > 1 {
		card.Headline = fmt.Sprintf("%d days in a row in %s, and %s in all.", bestLen, start.Month(), plural(len(days), "active day", "active days"))
	} else {
		card.Headline = fmt.Sprintf("%s out with Loci this year.", plural(len(days), "day", "days"))
	}
	return &recapv1.RecapCard{Id: CardStreak, Card: &recapv1.RecapCard_Streak{Streak: card}}
}

func yearProgress(f *YearFacts) *recapv1.RecapCard {
	if f.Points <= 0 && len(f.Badges) == 0 {
		return nil
	}
	card := &recapv1.ProgressCard{Points: f.Points}
	for _, id := range f.Badges {
		label := id
		if b, ok := gamification.BadgeByID(id); ok {
			label = b.Title
		}
		card.Badges = append(card.Badges, &recapv1.EarnedBadge{BadgeId: id, Label: label})
	}
	switch {
	case f.Points > 0 && len(card.Badges) > 0:
		card.Headline = fmt.Sprintf("%s and %s this year.", plural(int(f.Points), "point", "points"), plural(len(card.Badges), "badge", "badges"))
	case f.Points > 0:
		card.Headline = fmt.Sprintf("%s this year.", plural(int(f.Points), "point", "points"))
	default:
		card.Headline = fmt.Sprintf("%s earned this year.", plural(len(card.Badges), "badge", "badges"))
	}
	return &recapv1.RecapCard{Id: CardProgress, Card: &recapv1.RecapCard_Progress{Progress: card}}
}

func contributions(c Contributions) *recapv1.RecapCard {
	if c.Total() == 0 {
		return nil
	}
	var parts []string
	if c.Reviews > 0 {
		parts = append(parts, plural(c.Reviews, "review", "reviews"))
	}
	if c.Lists > 0 {
		parts = append(parts, plural(c.Lists, "list", "lists"))
	}
	if c.Posts > 0 {
		parts = append(parts, plural(c.Posts, "post", "posts"))
	}
	if c.Submissions > 0 {
		parts = append(parts, plural(c.Submissions, "place added", "places added"))
	}
	return &recapv1.RecapCard{Id: CardContributions, Card: &recapv1.RecapCard_Contributions{Contributions: &recapv1.ContributionsCard{
		ReviewsCount:     int32(c.Reviews),
		ListsCount:       int32(c.Lists),
		PostsCount:       int32(c.Posts),
		SubmissionsCount: int32(c.Submissions),
		Headline:         "You gave back: " + joinAnd(parts) + ".",
	}}}
}

// joinAnd joins "a", "a and b", "a, b and c".
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func percentile(p *Percentile) *recapv1.RecapCard {
	if p == nil || p.Places <= 0 || p.Active < minPercentileActive {
		return nil
	}
	top := p.TopPercent()
	if top <= 0 || top > maxPercentileShown {
		return nil
	}
	return &recapv1.RecapCard{Id: CardPercentile, Card: &recapv1.RecapCard_Percentile{Percentile: &recapv1.PercentileCard{
		TopPercent: int32(top),
		Metric:     recapv1.PercentileMetric_PERCENTILE_METRIC_PLACES,
		Value:      int32(p.Places),
		Headline:   fmt.Sprintf("Top %d%% of Loci travellers for places kept.", top),
	}}}
}

func yearOutro(f *YearFacts, cities int) *recapv1.RecapCard {
	var parts []string
	if cities > 0 {
		parts = append(parts, plural(cities, "city", "cities"))
	}
	if len(f.Trips) > 0 {
		parts = append(parts, plural(len(f.Trips), "trip", "trips"))
	}
	if len(f.Places) > 0 {
		parts = append(parts, plural(len(f.Places), "place", "places"))
	}
	return &recapv1.RecapCard{Id: CardOutro, Card: &recapv1.RecapCard_Outro{Outro: &recapv1.OutroCard{
		Headline:    fmt.Sprintf("See you in %d.", f.Year+1),
		SummaryLine: strings.Join(parts, " · "),
	}}}
}
