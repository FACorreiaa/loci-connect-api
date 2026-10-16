package recap

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/gamification"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/travelhistory"
)

// Card ids, stable within a recap; images and shares address cards by them.
const (
	CardIntro      = "intro"
	CardPlaces     = "places"
	CardRoute      = "route"
	CardDays       = "days"
	CardTaste      = "taste"
	CardHighlights = "highlights"
	CardProgress   = "progress"
	CardField      = "field"
	CardFriends    = "friends"
	CardOutro      = "outro"
)

// ReasonNoActivity is shown on a trip recap with nothing to tell.
const ReasonNoActivity = "Mark stops as visited to unlock your recap."

// Builder limits.
const (
	maxStamps     = 6
	maxTraits     = 3
	maxHighlights = 3
	maxFriends    = 5
	// minFallbackConfidence is how sure Loci must be of an overall trait to
	// show it when the trip left no evidence of its own.
	minFallbackConfidence = 0.4
	// minRouteKm is the shortest route worth a card.
	minRouteKm = 1.0
)

// Builder turns facts into a recap.
type Builder struct {
	// CityRanks are the field rank thresholds for one city.
	CityRanks gamification.Thresholds
}

// NewBuilder returns a builder with the default city rank thresholds.
func NewBuilder() *Builder {
	return &Builder{CityRanks: gamification.DefaultCityThresholds}
}

// BuildTrip builds a trip recap. ok is false, with a reason for the
// traveller, when the trip has too little to tell: an intro and an outro with
// nothing between them is not a recap.
func (b *Builder) BuildTrip(f *TripFacts) (rec *recapv1.Recap, reason string, ok bool) {
	if f == nil {
		return nil, ReasonNoActivity, false
	}
	completedDays := 0
	for _, d := range f.Days {
		if d.Completed {
			completedDays++
		}
	}
	if len(f.Places) == 0 && completedDays == 0 && f.StopsDone == 0 {
		return nil, ReasonNoActivity, false
	}

	var stats []*recapv1.RecapCard
	add := func(c *recapv1.RecapCard) {
		if c != nil {
			stats = append(stats, c)
		}
	}
	places := b.places(f)
	route := b.route(f)
	days := b.days(f)
	add(places)
	add(route)
	add(days)
	add(b.taste(f))
	add(b.highlights(f))
	add(b.progress(f))
	add(b.field(f))
	add(b.friends(f))
	if len(stats) == 0 {
		return nil, ReasonNoActivity, false
	}

	cards := make([]*recapv1.RecapCard, 0, len(stats)+2)
	cards = append(cards, b.intro(f))
	cards = append(cards, stats...)
	cards = append(cards, outro(places, route, days))

	return &recapv1.Recap{
		Kind:           recapv1.RecapKind_RECAP_KIND_TRIP,
		TripId:         f.TripID.String(),
		Title:          tripTitle(f),
		Subtitle:       subtitle(f),
		PeriodStart:    f.PeriodStart.Format(time.DateOnly),
		PeriodEnd:      f.PeriodEnd.Format(time.DateOnly),
		PeriodLabel:    PeriodLabel(f.PeriodStart, f.PeriodEnd),
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

func mainCity(f *TripFacts) City {
	if len(f.Cities) > 0 {
		return f.Cities[0]
	}
	return City{}
}

func tripTitle(f *TripFacts) string {
	t := strings.TrimSpace(f.Title)
	if t == "" || strings.EqualFold(t, "Untitled Trip") {
		if c := mainCity(f).Name; c != "" {
			return c
		}
		return "Your trip"
	}
	return t
}

func dayCount(f *TripFacts) int {
	n := int(f.PeriodEnd.Sub(f.PeriodStart).Hours()/24) + 1
	if len(f.Days) > n {
		n = len(f.Days)
	}
	if n < 1 {
		n = 1
	}
	return n
}

func subtitle(f *TripFacts) string {
	return plural(dayCount(f), "day", "days") + " · " + PeriodLabel(f.PeriodStart, f.PeriodEnd)
}

// PeriodLabel names a date range the way a person would: "Oct 2026",
// "Sep–Oct 2026", "Dec 2025 – Jan 2026".
func PeriodLabel(start, end time.Time) string {
	switch {
	case start.Year() != end.Year():
		return start.Format("Jan 2006") + " – " + end.Format("Jan 2006")
	case start.Month() != end.Month():
		return start.Format("Jan") + "–" + end.Format("Jan 2006")
	default:
		return start.Format("Jan 2006")
	}
}

func (b *Builder) intro(f *TripFacts) *recapv1.RecapCard {
	c := mainCity(f)
	names := make([]string, 0, len(f.Cities))
	for _, city := range f.Cities {
		names = append(names, city.Name)
	}
	days := dayCount(f)
	var lede string
	switch {
	case len(f.Cities) > 1:
		lede = fmt.Sprintf("%s cities, %s, one long story.", capitalize(numberWord(len(f.Cities))), plural(days, "day", "days"))
	case days == 1:
		lede = fmt.Sprintf("One day in %s, and every minute of it.", orTrip(c.Name))
	default:
		lede = fmt.Sprintf("%s days in %s, and the shoes to prove it.", capitalize(numberWord(days)), orTrip(c.Name))
	}
	return &recapv1.RecapCard{Id: CardIntro, Card: &recapv1.RecapCard_Intro{Intro: &recapv1.IntroCard{
		Lede:        lede,
		ShortCode:   ShortCode(f.ArrivalIATA, c.Name),
		PrimaryCity: c.Name,
		Country:     c.Country,
		CountryCode: c.CountryCode,
		CityNames:   names,
		DayCount:    int32(days),
	}}}
}

func orTrip(city string) string {
	if city == "" {
		return "town"
	}
	return city
}

// ShortCode is the three letters on the luggage tag: the arrival airport
// when known, else the city's first three letters without accents.
func ShortCode(iata, city string) string {
	iata = strings.ToUpper(strings.TrimSpace(iata))
	if len(iata) == 3 && strings.IndexFunc(iata, func(r rune) bool { return r < 'A' || r > 'Z' }) < 0 {
		return iata
	}
	plain, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), city)
	if err != nil {
		plain = city
	}
	var out []rune
	for _, r := range strings.ToUpper(plain) {
		if r >= 'A' && r <= 'Z' {
			out = append(out, r)
			if len(out) == 3 {
				break
			}
		}
	}
	return string(out)
}

func (b *Builder) places(f *TripFacts) *recapv1.RecapCard {
	if len(f.Places) == 0 && f.StopsDone == 0 {
		return nil
	}
	cities := map[string]bool{}
	for _, p := range f.Places {
		if p.CityName != "" {
			cities[strings.ToLower(p.CityName)] = true
		}
	}
	citiesCount := len(cities)
	if citiesCount == 0 && len(f.Places) > 0 {
		citiesCount = 1
	}

	groups, byCity := placeGroups(f)
	card := &recapv1.PlacesCard{
		PlacesCount:  int32(len(f.Places)),
		CitiesCount:  int32(citiesCount),
		StopsPlanned: int32(f.StopsPlanned),
		StopsDone:    int32(f.StopsDone),
		Groups:       groups,
	}
	noun := "neighbourhoods"
	if byCity {
		noun = "cities"
	}
	switch {
	case len(f.Places) == 0:
		card.Headline = fmt.Sprintf("%d of %s on your plan, done.", f.StopsDone, plural(f.StopsPlanned, "stop", "stops"))
	case len(groups) > 1:
		card.Headline = fmt.Sprintf("Kept across %s %s. %s took the most stamps.", numberWord(len(groups)), noun, groups[0].GetLabel())
	case len(groups) == 1:
		card.Headline = fmt.Sprintf("Every one of them in %s.", groups[0].GetLabel())
	}
	return &recapv1.RecapCard{Id: CardPlaces, Card: &recapv1.RecapCard_Places{Places: card}}
}

// placeGroups stamps the passport: one stamp per city on a multi-city trip,
// otherwise one per neighbourhood. Largest first.
func placeGroups(f *TripFacts) ([]*recapv1.PlaceGroup, bool) {
	byCity := len(f.Cities) > 1
	type agg struct {
		label string
		count int
		first time.Time
	}
	order := []string{}
	m := map[string]*agg{}
	for _, p := range f.Places {
		label := p.Neighborhood
		if byCity {
			label = p.CityName
		}
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		a, ok := m[key]
		if !ok {
			a = &agg{label: label, first: p.At}
			m[key] = a
			order = append(order, key)
		}
		a.count++
		if !p.At.IsZero() && (a.first.IsZero() || p.At.Before(a.first)) {
			a.first = p.At
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return m[order[i]].count > m[order[j]].count })
	if len(order) > maxStamps {
		order = order[:maxStamps]
	}
	loc := Location(f.Timezone)
	out := make([]*recapv1.PlaceGroup, 0, len(order))
	for _, k := range order {
		a := m[k]
		g := &recapv1.PlaceGroup{Label: a.label, PlacesCount: int32(a.count)}
		if !a.first.IsZero() {
			g.FirstDate = LocalDate(a.first, loc).Format(time.DateOnly)
		}
		out = append(out, g)
	}
	return out, byCity
}

func (b *Builder) route(f *TripFacts) *recapv1.RecapCard {
	var cityPts []*recapv1.RoutePoint
	for _, c := range f.Cities {
		if c.HasCoords {
			cityPts = append(cityPts, &recapv1.RoutePoint{Latitude: c.Lat, Longitude: c.Lon, Label: c.Name})
		}
	}
	visits := make([]Place, 0, len(f.Places))
	for _, p := range f.Places {
		if p.HasCoords && !p.At.IsZero() {
			visits = append(visits, p)
		}
	}
	sort.SliceStable(visits, func(i, j int) bool { return visits[i].At.Before(visits[j].At) })
	visitPts := make([]*recapv1.RoutePoint, 0, len(visits))
	for _, p := range visits {
		visitPts = append(visitPts, &recapv1.RoutePoint{Latitude: p.Lat, Longitude: p.Lon, Label: p.Name})
	}

	card := &recapv1.RouteCard{}
	switch {
	case f.LegsKm >= minRouteKm:
		card.Basis = recapv1.RouteBasis_ROUTE_BASIS_LEGS
		card.DistanceKm = f.LegsKm
		card.Points = cityPts
	case len(cityPts) >= 2:
		card.Basis = recapv1.RouteBasis_ROUTE_BASIS_CITIES
		card.DistanceKm = pathKm(cityPts)
		card.Points = cityPts
	case len(visitPts) >= 2:
		card.Basis = recapv1.RouteBasis_ROUTE_BASIS_VISITS
		card.DistanceKm = pathKm(visitPts)
		card.Points = visitPts
	default:
		return nil
	}
	if card.DistanceKm < minRouteKm {
		return nil
	}
	card.DistanceKm = math.Round(card.DistanceKm*10) / 10
	if n := len(card.Points); n >= 2 {
		card.Headline = fmt.Sprintf("From %s to %s.", card.Points[0].GetLabel(), card.Points[n-1].GetLabel())
	}
	return &recapv1.RecapCard{Id: CardRoute, Card: &recapv1.RecapCard_Route{Route: card}}
}

func pathKm(pts []*recapv1.RoutePoint) float64 {
	total := 0.0
	for i := 1; i < len(pts); i++ {
		total += travelhistory.HaversineKm(pts[i-1].GetLatitude(), pts[i-1].GetLongitude(), pts[i].GetLatitude(), pts[i].GetLongitude())
	}
	return total
}

func (b *Builder) days(f *TripFacts) *recapv1.RecapCard {
	if len(f.Days) == 0 {
		return nil
	}
	card := &recapv1.DaysCard{DaysTotal: int32(len(f.Days))}
	loc := Location(f.Timezone)
	best, stopsDone := 0, 0
	for _, d := range f.Days {
		stub := &recapv1.DayStub{
			DayNumber:  int32(d.Number),
			CityName:   d.CityName,
			StopsDone:  int32(d.StopsDone),
			StopsTotal: int32(d.StopsTotal),
			Completed:  d.Completed,
		}
		if d.Date != nil {
			stub.Date = LocalDate(*d.Date, loc).Format(time.DateOnly)
		}
		if d.Completed {
			card.DaysCompleted++
		}
		if d.StopsDone > best {
			best = d.StopsDone
			card.BusiestDayNumber = int32(d.Number)
		}
		stopsDone += d.StopsDone
		card.Days = append(card.Days, stub)
	}
	if card.DaysCompleted == 0 && stopsDone == 0 {
		return nil
	}
	if card.DaysCompleted == card.DaysTotal {
		card.Headline = "Every day walked, start to finish."
	} else {
		card.Headline = fmt.Sprintf("%d of %s walked to the end.", card.DaysCompleted, plural(int(card.DaysTotal), "day", "days"))
	}
	return &recapv1.RecapCard{Id: CardDays, Card: &recapv1.RecapCard_Days{Days: card}}
}

func (b *Builder) taste(f *TripFacts) *recapv1.RecapCard {
	fromPeriod := true
	traits := positive(f.TripTaste, func(t Trait) float64 { return t.Weight })
	if len(traits) == 0 {
		fromPeriod = false
		for _, t := range f.OverallTaste {
			if t.Score > 0 && t.Confidence >= minFallbackConfidence {
				traits = append(traits, t)
			}
		}
		sort.SliceStable(traits, func(i, j int) bool {
			return traits[i].Score*traits[i].Confidence > traits[j].Score*traits[j].Confidence
		})
	}
	if len(traits) == 0 {
		return nil
	}
	if len(traits) > maxTraits {
		traits = traits[:maxTraits]
	}
	measure := func(t Trait) float64 {
		if fromPeriod {
			return t.Weight
		}
		return t.Score * t.Confidence
	}
	top := measure(traits[0])
	card := &recapv1.TasteCard{FromPeriod: fromPeriod}
	for _, t := range traits {
		strength := 1.0
		if top > 0 {
			strength = math.Max(0.15, math.Min(1, measure(t)/top))
		}
		tt := &recapv1.TasteTrait{
			TraitKey: t.Key,
			Label:    t.Label,
			Strength: math.Round(strength*100) / 100,
			Trend:    trend(t, fromPeriod),
		}
		if fromPeriod {
			tt.EvidenceCount = int32(t.Evidence)
			tt.EvidenceKind = evidenceKind(t)
		}
		card.Traits = append(card.Traits, tt)
	}
	if fromPeriod {
		card.Headline = fmt.Sprintf("You kept going back to %s.", lowerFirst(traits[0].Label))
	} else {
		card.Headline = "Your taste, as Loci sees it."
	}
	return &recapv1.RecapCard{Id: CardTaste, Card: &recapv1.RecapCard_Taste{Taste: card}}
}

func positive(ts []Trait, by func(Trait) float64) []Trait {
	out := make([]Trait, 0, len(ts))
	for _, t := range ts {
		if by(t) > 0 && strings.TrimSpace(t.Label) != "" {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return by(out[i]) > by(out[j]) })
	return out
}

func trend(t Trait, fromPeriod bool) recapv1.TasteTrend {
	switch {
	case fromPeriod && t.TotalEvidence > 0 && t.Evidence >= t.TotalEvidence:
		return recapv1.TasteTrend_TASTE_TREND_NEW
	case t.Score >= 0.6:
		return recapv1.TasteTrend_TASTE_TREND_STRONG
	default:
		return recapv1.TasteTrend_TASTE_TREND_GROWING
	}
}

func evidenceKind(t Trait) recapv1.TasteEvidenceKind {
	switch {
	case t.Saves > 0 && t.Visits == 0:
		return recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_SAVES
	case t.Visits > 0 && t.Saves == 0:
		return recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_VISITS
	case t.Visits > 0 && t.Saves > 0:
		return recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_MIXED
	default:
		return recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_UNSPECIFIED
	}
}

func (b *Builder) highlights(f *TripFacts) *recapv1.RecapCard {
	seen := map[string]bool{}
	card := &recapv1.HighlightsCard{Headline: "The ones you kept."}
	// Favourites and reviews first; then the first places visited, one per
	// category so three cafés do not crowd out everything else.
	candidates := append([]HighlightFact(nil), f.Highlights...)
	firsts := append([]Place(nil), f.Places...)
	sort.SliceStable(firsts, func(i, j int) bool { return firsts[i].At.Before(firsts[j].At) })
	usedCategory := map[string]bool{}
	for _, p := range firsts {
		cat := strings.ToLower(p.Category)
		if cat != "" && usedCategory[cat] {
			continue
		}
		usedCategory[cat] = true
		candidates = append(candidates, HighlightFact{POIID: p.POIID, Name: p.Name, Category: p.Category, Reason: HighlightFirstVisit})
	}
	for _, h := range candidates {
		key := h.POIID
		if key == "" {
			key = strings.ToLower(h.Name)
		}
		if seen[key] || strings.TrimSpace(h.Name) == "" {
			continue
		}
		seen[key] = true
		card.Highlights = append(card.Highlights, &recapv1.Highlight{
			PoiId:    h.POIID,
			Name:     h.Name,
			Category: h.Category,
			Rating:   int32(h.Rating),
			Reason:   recapv1.HighlightReason(h.Reason),
		})
		if len(card.Highlights) == maxHighlights {
			break
		}
	}
	if len(card.Highlights) == 0 {
		return nil
	}
	return &recapv1.RecapCard{Id: CardHighlights, Card: &recapv1.RecapCard_Highlights{Highlights: card}}
}

func (b *Builder) progress(f *TripFacts) *recapv1.RecapCard {
	if f.Points <= 0 && len(f.Badges) == 0 {
		return nil
	}
	card := &recapv1.ProgressCard{Points: f.Points, StreakDays: int32(LongestRun(f.ActiveDates))}
	for _, id := range f.Badges {
		label := id
		if b, ok := gamification.BadgeByID(id); ok {
			label = b.Title
		}
		card.Badges = append(card.Badges, &recapv1.EarnedBadge{BadgeId: id, Label: label})
	}
	switch {
	case f.Points > 0 && len(card.Badges) > 0:
		card.Headline = fmt.Sprintf("%s and %s on this trip.", plural(int(f.Points), "point", "points"), plural(len(card.Badges), "badge", "badges"))
	case f.Points > 0:
		card.Headline = fmt.Sprintf("%s on this trip.", plural(int(f.Points), "point", "points"))
	default:
		card.Headline = fmt.Sprintf("%s earned on this trip.", plural(len(card.Badges), "badge", "badges"))
	}
	return &recapv1.RecapCard{Id: CardProgress, Card: &recapv1.RecapCard_Progress{Progress: card}}
}

// LongestRun is the longest run of consecutive dates.
func LongestRun(dates []time.Time) int {
	if len(dates) == 0 {
		return 0
	}
	days := make([]int64, 0, len(dates))
	seen := map[int64]bool{}
	for _, d := range dates {
		k := dateOnly(d).Unix() / 86400
		if !seen[k] {
			seen[k] = true
			days = append(days, k)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	best, run := 1, 1
	for i := 1; i < len(days); i++ {
		if days[i] == days[i-1]+1 {
			run++
			best = max(best, run)
		} else {
			run = 1
		}
	}
	return best
}

func (b *Builder) field(f *TripFacts) *recapv1.RecapCard {
	if f.Field == nil || f.Field.Score <= 0 {
		return nil
	}
	rank, _ := gamification.RankFor(f.Field.Score, b.CityRanks)
	name := gamification.RankNames[rank]
	card := &recapv1.FieldCard{
		CityId:       f.Field.CityID.String(),
		CityName:     f.Field.CityName,
		Score:        f.Field.Score,
		RankLabel:    name,
		PlacesKept:   int32(f.Field.PlacesKept),
		DaysFinished: int32(f.Field.DaysFinished),
	}
	if f.Field.CityName != "" {
		card.Headline = fmt.Sprintf("%s of %s.", name, f.Field.CityName)
	} else {
		card.Headline = name + "."
	}
	return &recapv1.RecapCard{Id: CardField, Card: &recapv1.RecapCard_Field{Field: card}}
}

func (b *Builder) friends(f *TripFacts) *recapv1.RecapCard {
	if len(f.Friends) == 0 {
		return nil
	}
	city := orTrip(mainCity(f).Name)
	card := &recapv1.FriendsCard{FriendsBeenCount: int32(len(f.Friends))}
	for i, fr := range f.Friends {
		if i == maxFriends {
			break
		}
		card.Friends = append(card.Friends, &recapv1.FriendRef{
			UserId: fr.ID.String(), DisplayName: fr.DisplayName, AvatarUrl: fr.AvatarURL,
		})
	}
	if len(f.Friends) == 1 && f.Friends[0].DisplayName != "" {
		card.Headline = fmt.Sprintf("%s has %s on a trip too.", f.Friends[0].DisplayName, city)
	} else {
		card.Headline = fmt.Sprintf("%s of your friends have %s on a trip too.", capitalize(numberWord(len(f.Friends))), city)
	}
	return &recapv1.RecapCard{Id: CardFriends, Card: &recapv1.RecapCard_Friends{Friends: card}}
}

func outro(places, route, days *recapv1.RecapCard) *recapv1.RecapCard {
	var parts []string
	if p := places.GetPlaces(); p != nil && p.GetPlacesCount() > 0 {
		parts = append(parts, plural(int(p.GetPlacesCount()), "place", "places"))
	}
	if r := route.GetRoute(); r != nil {
		parts = append(parts, fmt.Sprintf("%.0f km", r.GetDistanceKm()))
	}
	if d := days.GetDays(); d != nil {
		parts = append(parts, plural(int(d.GetDaysTotal()), "day", "days"))
	}
	return &recapv1.RecapCard{Id: CardOutro, Card: &recapv1.RecapCard_Outro{Outro: &recapv1.OutroCard{
		Headline:    "Where to next?",
		SummaryLine: strings.Join(parts, " · "),
	}}}
}

var numberWords = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}

func numberWord(n int) string {
	if n >= 0 && n < len(numberWords) {
		return numberWords[n]
	}
	return fmt.Sprint(n)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// lowerFirst lowercases a label's first letter for use mid-sentence, but
// keeps proper nouns and acronyms ("LX Factory", "IPA") as written.
func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	if next, _ := utf8.DecodeRuneInString(s[n:]); unicode.IsUpper(next) {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}
