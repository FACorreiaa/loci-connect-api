// Package achievements is what a traveller has done in Loci, counted: tiered
// tracks (cities, stops walked, reviews...), one-off moments and a few
// secrets. They replace the old badges and the contributor badges.
//
// Nothing here records an action. Every counter is read from tables the
// actions already write, so evaluating a user is always safe to repeat, and a
// user's past can be backfilled with the same code that grants live.
package achievements

import (
	"fmt"
	"strings"
)

// Kind matches AchievementKind in the proto.
type Kind int32

const (
	KindTrack  Kind = 1
	KindMoment Kind = 2
	KindSecret Kind = 3
)

// Tier matches AchievementTier in the proto. 0 is "none": the tier stored for
// a moment or a secret.
type Tier int16

const (
	TierNone     Tier = 0
	TierBronze   Tier = 1
	TierSilver   Tier = 2
	TierGold     Tier = 3
	TierPlatinum Tier = 4
)

// TierNames are the tiers as the app writes them.
var TierNames = map[Tier]string{
	TierBronze: "Bronze", TierSilver: "Silver", TierGold: "Gold", TierPlatinum: "Platinum",
}

// Counter is one number an achievement is judged on. Each is one query over
// the user's own rows (see counterSQL).
type Counter string

const (
	CountCities         Counter = "cities"
	CountCountries      Counter = "countries"
	CountPlaces         Counter = "places"
	CountVerifiedVisits Counter = "verified_visits"
	CountNeighborhoods  Counter = "neighborhoods"
	CountDays           Counter = "days"
	CountStops          Counter = "stops"
	CountTrips          Counter = "trips"
	CountDiaries        Counter = "diaries"
	CountPhotos         Counter = "photos"
	CountReviews        Counter = "reviews"
	CountHelpfuls       Counter = "helpfuls"
	CountClaims         Counter = "claims"
	CountSaves          Counter = "saves"
	CountPosts          Counter = "posts"
	CountWeekStreak     Counter = "week_streak"
	CountGrandTours     Counter = "grand_tours"
	CountSubmissions    Counter = "submissions"
	CountPacks          Counter = "packs"
	CountRankings       Counter = "rankings"
	CountCuratedLists   Counter = "curated_lists"
	CountCopied         Counter = "copied"
	CountCopiedByOthers Counter = "copied_by_others"
	CountInvited        Counter = "invited"
	CountFriends        Counter = "friends"
	CountNightVisits    Counter = "night_visits"
	CountFullDays       Counter = "full_days"
	CountHomecomings    Counter = "homecomings"
	CountKeeperCities   Counter = "keeper_cities"
)

// Def is one achievement.
type Def struct {
	ID       string
	Title    string
	Kind     Kind
	Category string
	// Icon is a semantic key the apps map to their own symbols.
	Icon    string
	Counter Counter
	// Blurb describes it. A track's has one %d, the count its next tier asks.
	Blurb string
	// Tiers are a track's bronze to platinum thresholds.
	Tiers [4]int64
	// Target is what a moment or a secret asks of its counter (1 when zero).
	Target int64
}

// Threshold is the count tier t of a track asks, or the target of a moment
// or a secret.
func (d Def) Threshold(t Tier) int64 {
	if d.Kind == KindTrack {
		return d.Tiers[t-1]
	}
	if d.Target == 0 {
		return 1
	}
	return d.Target
}

// Reached is the highest tier n reaches on a track; for a moment or a secret
// it is whether n reaches the target.
func (d Def) Reached(n int64) (Tier, bool) {
	if d.Kind != KindTrack {
		return TierNone, n >= d.Threshold(TierNone)
	}
	reached := TierNone
	for i, need := range d.Tiers {
		if n >= need {
			reached = Tier(i + 1)
		}
	}
	return reached, reached > TierNone
}

// Describe is the description at target: the next tier's count for a track
// still climbing, its top count once complete.
func (d Def) Describe(target int64) string {
	if d.Kind != KindTrack {
		return d.Blurb
	}
	return fmt.Sprintf(d.Blurb, target)
}

// Units counts what can be earned from d: four for a track, one otherwise.
func (d Def) Units() int {
	if d.Kind == KindTrack {
		return len(d.Tiers)
	}
	return 1
}

// Categories group the catalogue for display.
const (
	CatExplore   = "explore"
	CatWalk      = "walk"
	CatRecord    = "record"
	CatCommunity = "community"
	CatSocial    = "social"
)

// Catalogue is every achievement, in the order the app lists them: tracks,
// then moments, then secrets. IDs are stable: they are stored.
var Catalogue = []Def{
	// Tracks.
	{ID: "cartographer", Title: "Cartographer", Kind: KindTrack, Category: CatExplore, Icon: "map", Counter: CountCities, Blurb: "Visit %d cities.", Tiers: [4]int64{5, 15, 30, 50}},
	{ID: "border-crosser", Title: "Border Crosser", Kind: KindTrack, Category: CatExplore, Icon: "globe", Counter: CountCountries, Blurb: "Visit %d countries.", Tiers: [4]int64{2, 5, 10, 20}},
	{ID: "footfall", Title: "Footfall", Kind: KindTrack, Category: CatExplore, Icon: "footprints", Counter: CountPlaces, Blurb: "Visit %d places.", Tiers: [4]int64{25, 100, 300, 750}},
	{ID: "street-by-street", Title: "Street by Street", Kind: KindTrack, Category: CatExplore, Icon: "signpost", Counter: CountNeighborhoods, Blurb: "Explore %d neighborhoods.", Tiers: [4]int64{5, 20, 50, 120}},
	{ID: "day-walker", Title: "Day Walker", Kind: KindTrack, Category: CatWalk, Icon: "sun", Counter: CountDays, Blurb: "Finish %d trip days.", Tiers: [4]int64{3, 10, 30, 75}},
	{ID: "trailhead", Title: "Trailhead", Kind: KindTrack, Category: CatWalk, Icon: "flag", Counter: CountStops, Blurb: "Walk %d stops.", Tiers: [4]int64{20, 75, 200, 500}},
	{ID: "itinerant", Title: "Itinerant", Kind: KindTrack, Category: CatWalk, Icon: "route", Counter: CountTrips, Blurb: "Finish %d trips.", Tiers: [4]int64{2, 5, 12, 25}},
	{ID: "steady-boots", Title: "Steady Boots", Kind: KindTrack, Category: CatWalk, Icon: "flame", Counter: CountWeekStreak, Blurb: "Explore %d weeks in a row.", Tiers: [4]int64{4, 12, 26, 52}},
	{ID: "field-notes", Title: "Field Notes", Kind: KindTrack, Category: CatRecord, Icon: "notebook-pen", Counter: CountDiaries, Blurb: "Write %d trip diary entries.", Tiers: [4]int64{3, 15, 40, 100}},
	{ID: "darkroom", Title: "Darkroom", Kind: KindTrack, Category: CatRecord, Icon: "camera", Counter: CountPhotos, Blurb: "Add %d trip photos.", Tiers: [4]int64{10, 50, 150, 400}},
	{ID: "cabinet-of-curiosities", Title: "Cabinet of Curiosities", Kind: KindTrack, Category: CatRecord, Icon: "bookmark", Counter: CountSaves, Blurb: "Save %d places.", Tiers: [4]int64{10, 50, 150, 400}},
	{ID: "critics-hand", Title: "Critic's Hand", Kind: KindTrack, Category: CatCommunity, Icon: "pen-line", Counter: CountReviews, Blurb: "Publish %d reviews.", Tiers: [4]int64{3, 10, 25, 60}},
	{ID: "good-word", Title: "Good Word", Kind: KindTrack, Category: CatCommunity, Icon: "thumbs-up", Counter: CountHelpfuls, Blurb: "Get %d helpful votes on your reviews.", Tiers: [4]int64{5, 25, 75, 200}},
	{ID: "scout", Title: "Scout", Kind: KindTrack, Category: CatCommunity, Icon: "binoculars", Counter: CountClaims, Blurb: "Have %d field reports confirmed.", Tiers: [4]int64{3, 10, 25, 60}},
	{ID: "town-crier", Title: "Town Crier", Kind: KindTrack, Category: CatCommunity, Icon: "megaphone", Counter: CountPosts, Blurb: "Post or reply %d times on city boards.", Tiers: [4]int64{5, 25, 75, 200}},

	// Moments.
	{ID: "first-pin", Title: "First Pin", Kind: KindMoment, Category: CatExplore, Icon: "map-pin", Counter: CountCities, Blurb: "Visit your first city."},
	{ID: "first-footstep", Title: "First Footstep", Kind: KindMoment, Category: CatExplore, Icon: "locate", Counter: CountVerifiedVisits, Blurb: "Check in at a place on the spot."},
	{ID: "pack-mule", Title: "Pack Mule", Kind: KindMoment, Category: CatExplore, Icon: "package", Counter: CountPacks, Blurb: "Claim your first City Pack."},
	{ID: "day-one-done", Title: "Day One Done", Kind: KindMoment, Category: CatWalk, Icon: "sunrise", Counter: CountDays, Blurb: "Finish your first trip day."},
	{ID: "round-trip", Title: "Round Trip", Kind: KindMoment, Category: CatWalk, Icon: "repeat", Counter: CountTrips, Blurb: "Finish your first trip."},
	{ID: "grand-tour", Title: "Grand Tour", Kind: KindMoment, Category: CatWalk, Icon: "milestone", Counter: CountGrandTours, Blurb: "Finish a trip through three or more cities."},
	{ID: "ink-on-paper", Title: "Ink on Paper", Kind: KindMoment, Category: CatRecord, Icon: "feather", Counter: CountDiaries, Blurb: "Write your first trip diary entry."},
	{ID: "first-light", Title: "First Light", Kind: KindMoment, Category: CatRecord, Icon: "aperture", Counter: CountPhotos, Blurb: "Add your first trip photo."},
	{ID: "taste-ledger", Title: "Taste Ledger", Kind: KindMoment, Category: CatRecord, Icon: "list-ordered", Counter: CountRankings, Target: 10, Blurb: "Rank 10 places."},
	{ID: "curator", Title: "Curator", Kind: KindMoment, Category: CatRecord, Icon: "library", Counter: CountCuratedLists, Blurb: "Make a list of five or more places."},
	{ID: "on-the-map", Title: "On the Map", Kind: KindMoment, Category: CatCommunity, Icon: "map-pin-plus", Counter: CountSubmissions, Blurb: "Have a place you added accepted."},
	{ID: "hand-me-down", Title: "Hand-Me-Down", Kind: KindMoment, Category: CatSocial, Icon: "copy", Counter: CountCopied, Blurb: "Copy someone else's trip."},
	{ID: "well-trodden", Title: "Well-Trodden", Kind: KindMoment, Category: CatSocial, Icon: "users", Counter: CountCopiedByOthers, Blurb: "Have someone copy your trip."},
	{ID: "plus-one", Title: "Plus One", Kind: KindMoment, Category: CatSocial, Icon: "user-plus", Counter: CountInvited, Blurb: "Have someone join Loci from your invite."},
	{ID: "kindred", Title: "Kindred", Kind: KindMoment, Category: CatSocial, Icon: "handshake", Counter: CountFriends, Blurb: "Follow someone who follows you back."},

	// Secrets: hidden until earned.
	{ID: "night-owl", Title: "Night Owl", Kind: KindSecret, Category: CatExplore, Icon: "moon", Counter: CountNightVisits, Blurb: "Visit a place between midnight and 4 a.m."},
	{ID: "homecoming", Title: "Homecoming", Kind: KindSecret, Category: CatExplore, Icon: "house", Counter: CountHomecomings, Blurb: "Return to the same city in three different years."},
	{ID: "full-house", Title: "Full House", Kind: KindSecret, Category: CatWalk, Icon: "list-checks", Counter: CountFullDays, Blurb: "Walk every stop of a day with six or more."},
	{ID: "keeper", Title: "Keeper of the City", Kind: KindSecret, Category: CatWalk, Icon: "crown", Counter: CountKeeperCities, Blurb: "Reach Keeper rank in a city."},
}

// ByID finds an achievement.
func ByID(id string) (Def, bool) {
	for _, d := range Catalogue {
		if d.ID == id {
			return d, true
		}
	}
	return Def{}, false
}

// TotalUnits is everything that can be earned, each track tier once.
func TotalUnits() int {
	n := 0
	for _, d := range Catalogue {
		n += d.Units()
	}
	return n
}

// Key names a unit in one string: the achievement's id, then its tier for a
// track ("cartographer-gold"). Older apps get it as a badge id.
func (u Unit) Key() string {
	if u.Tier == TierNone {
		return u.ID
	}
	return u.ID + "-" + strings.ToLower(TierNames[u.Tier])
}

// Label is the display name of a Key ("Cartographer · Gold").
func Label(key string) (string, bool) {
	if d, ok := ByID(key); ok {
		return d.Title, true
	}
	for t, name := range TierNames {
		id, ok := strings.CutSuffix(key, "-"+strings.ToLower(name))
		if !ok {
			continue
		}
		if d, ok := ByID(id); ok && d.Kind == KindTrack {
			return d.Title + " · " + TierNames[t], true
		}
	}
	return "", false
}
