package recap

import (
	"fmt"
	"strings"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
)

// AnonymousName stands in for an owner with no display name.
const AnonymousName = "A Loci traveller"

// PublicView is what a stranger with the share link sees: cities and counts.
// No place names, no exact dates, no friend identities, no evidence behind
// the taste traits, no highlights. It works on a copy; r is not changed.
//
// The rule is "keep only what is listed", so a field added to a card later
// stays private until someone decides it is safe to show.
func PublicView(r *recapv1.Recap) *recapv1.Recap {
	if r == nil {
		return nil
	}
	out := &recapv1.Recap{
		Id:          r.GetId(),
		Kind:        r.GetKind(),
		Status:      r.GetStatus(),
		Year:        r.GetYear(),
		Title:       r.GetTitle(),
		Subtitle:    r.GetSubtitle(),
		PeriodLabel: r.GetPeriodLabel(),
		Theme: &recapv1.RecapTheme{
			AccentHex:    r.GetTheme().GetAccentHex(),
			HeroImageUrl: r.GetTheme().GetHeroImageUrl(),
		},
		Owner:          &recapv1.RecapOwner{DisplayName: FirstName(r.GetOwner().GetDisplayName())},
		ShareUrl:       r.GetShareUrl(),
		ReadyAt:        r.GetReadyAt(),
		BuilderVersion: r.GetBuilderVersion(),
	}
	cities := cityNames(r)
	primary := ""
	for _, c := range r.GetCards() {
		if in := c.GetIntro(); in != nil {
			primary = in.GetPrimaryCity()
		}
	}
	for _, c := range r.GetCards() {
		if pc := publicCard(c, cities, primary); pc != nil {
			out.Cards = append(out.Cards, pc)
		}
	}
	return out
}

// FirstName is the first word of a display name, or AnonymousName.
func FirstName(display string) string {
	if f := strings.Fields(display); len(f) > 0 {
		return f[0]
	}
	return AnonymousName
}

func cityNames(r *recapv1.Recap) map[string]bool {
	m := map[string]bool{}
	for _, c := range r.GetCards() {
		if in := c.GetIntro(); in != nil {
			for _, n := range in.GetCityNames() {
				m[strings.ToLower(n)] = true
			}
			if n := in.GetPrimaryCity(); n != "" {
				m[strings.ToLower(n)] = true
			}
		}
	}
	return m
}

func publicCard(c *recapv1.RecapCard, cities map[string]bool, primary string) *recapv1.RecapCard {
	id := c.GetId()
	switch {
	case c.GetIntro() != nil:
		in := c.GetIntro()
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Intro{Intro: &recapv1.IntroCard{
			Lede:        in.GetLede(),
			ShortCode:   in.GetShortCode(),
			PrimaryCity: in.GetPrimaryCity(),
			Country:     in.GetCountry(),
			CountryCode: in.GetCountryCode(),
			CityNames:   append([]string(nil), in.GetCityNames()...),
			DayCount:    in.GetDayCount(),
		}}}

	case c.GetPlaces() != nil:
		p := c.GetPlaces()
		card := &recapv1.PlacesCard{
			PlacesCount:  p.GetPlacesCount(),
			CitiesCount:  p.GetCitiesCount(),
			StopsPlanned: p.GetStopsPlanned(),
			StopsDone:    p.GetStopsDone(),
		}
		// Stamps name neighbourhoods, which say where someone was more
		// precisely than a city does. Only city stamps stay, with counts.
		for _, g := range p.GetGroups() {
			if cities[strings.ToLower(g.GetLabel())] {
				card.Groups = append(card.Groups, &recapv1.PlaceGroup{Label: g.GetLabel(), PlacesCount: g.GetPlacesCount()})
			}
		}
		// A one-city trip's stamps were all neighbourhoods: stamp the city
		// instead, and say how many neighbourhoods without naming them.
		if len(card.Groups) == 0 && card.GetPlacesCount() > 0 && primary != "" {
			card.Groups = []*recapv1.PlaceGroup{{Label: primary, PlacesCount: card.GetPlacesCount()}}
			if n := len(p.GetGroups()); n > 1 {
				card.Headline = fmt.Sprintf("Kept across %s neighbourhoods of %s.", numberWord(n), primary)
			}
		}
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Places{Places: card}}

	case c.GetRoute() != nil:
		rt := c.GetRoute()
		card := &recapv1.RouteCard{DistanceKm: rt.GetDistanceKm(), Basis: rt.GetBasis()}
		// A route through places is a trail of exactly where someone went.
		// A route between cities is only the cities.
		if rt.GetBasis() != recapv1.RouteBasis_ROUTE_BASIS_VISITS {
			for _, pt := range rt.GetPoints() {
				if cities[strings.ToLower(pt.GetLabel())] {
					card.Points = append(card.Points, &recapv1.RoutePoint{
						Latitude: pt.GetLatitude(), Longitude: pt.GetLongitude(), Label: pt.GetLabel(),
					})
				}
			}
			if len(card.Points) >= 2 {
				card.Headline = rt.GetHeadline()
			}
		}
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Route{Route: card}}

	case c.GetDays() != nil:
		d := c.GetDays()
		card := &recapv1.DaysCard{
			DaysTotal:        d.GetDaysTotal(),
			DaysCompleted:    d.GetDaysCompleted(),
			BusiestDayNumber: d.GetBusiestDayNumber(),
			Headline:         d.GetHeadline(),
		}
		for _, s := range d.GetDays() {
			card.Days = append(card.Days, &recapv1.DayStub{
				DayNumber:  s.GetDayNumber(),
				CityName:   s.GetCityName(),
				StopsDone:  s.GetStopsDone(),
				StopsTotal: s.GetStopsTotal(),
				Completed:  s.GetCompleted(),
			})
		}
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Days{Days: card}}

	case c.GetTaste() != nil:
		t := c.GetTaste()
		card := &recapv1.TasteCard{Headline: t.GetHeadline(), FromPeriod: t.GetFromPeriod()}
		for _, tr := range t.GetTraits() {
			if tr.GetStrength() <= 0 {
				continue
			}
			card.Traits = append(card.Traits, &recapv1.TasteTrait{
				TraitKey: tr.GetTraitKey(),
				Label:    tr.GetLabel(),
				Strength: tr.GetStrength(),
				Trend:    tr.GetTrend(),
			})
		}
		if len(card.Traits) == 0 {
			return nil
		}
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Taste{Taste: card}}

	case c.GetProgress() != nil:
		p := c.GetProgress()
		card := &recapv1.ProgressCard{Points: p.GetPoints(), StreakDays: p.GetStreakDays(), Headline: p.GetHeadline()}
		for _, b := range p.GetBadges() {
			card.Badges = append(card.Badges, &recapv1.EarnedBadge{BadgeId: b.GetBadgeId(), Label: b.GetLabel()})
		}
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Progress{Progress: card}}

	case c.GetField() != nil:
		f := c.GetField()
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Field{Field: &recapv1.FieldCard{
			CityName:     f.GetCityName(),
			Score:        f.GetScore(),
			RankLabel:    f.GetRankLabel(),
			PlacesKept:   f.GetPlacesKept(),
			DaysFinished: f.GetDaysFinished(),
			Headline:     f.GetHeadline(),
		}}}

	case c.GetFriends() != nil:
		// Friends did not agree to appear on someone else's public page:
		// keep the count, drop who they are.
		f := c.GetFriends()
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Friends{Friends: &recapv1.FriendsCard{
			FriendsBeenCount: f.GetFriendsBeenCount(),
		}}}

	case c.GetOutro() != nil:
		o := c.GetOutro()
		return &recapv1.RecapCard{Id: id, Card: &recapv1.RecapCard_Outro{Outro: &recapv1.OutroCard{
			Headline: o.GetHeadline(), SummaryLine: o.GetSummaryLine(),
		}}}
	}
	// Highlights name places; unknown cards are not known to be safe.
	return nil
}
