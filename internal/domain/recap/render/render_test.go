package render

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
)

func lisbonRecap() *recapv1.Recap {
	pts := []*recapv1.RoutePoint{
		{Latitude: 38.7139, Longitude: -9.1335, Label: "Alfama"},
		{Latitude: 38.7110, Longitude: -9.1300, Label: "Sé"},
		{Latitude: 38.7107, Longitude: -9.1366, Label: "Baixa"},
		{Latitude: 38.7166, Longitude: -9.1488, Label: "Príncipe Real"},
		{Latitude: 38.7077, Longitude: -9.1527, Label: "Cais do Sodré"},
		{Latitude: 38.7037, Longitude: -9.1788, Label: "LX Factory"},
		{Latitude: 38.6979, Longitude: -9.2063, Label: "Jerónimos"},
		{Latitude: 38.6916, Longitude: -9.2160, Label: "Belém"},
	}
	days := make([]*recapv1.DayStub, 5)
	for i := range days {
		days[i] = &recapv1.DayStub{
			DayNumber: int32(i + 1), Date: fmt.Sprintf("2026-10-%02d", 3+i), CityName: "Lisbon",
			StopsDone: int32(4 + i%2), StopsTotal: 5, Completed: i < 4,
		}
	}
	card := func(id string, c any) *recapv1.RecapCard {
		rc := &recapv1.RecapCard{Id: id}
		switch v := c.(type) {
		case *recapv1.IntroCard:
			rc.Card = &recapv1.RecapCard_Intro{Intro: v}
		case *recapv1.PlacesCard:
			rc.Card = &recapv1.RecapCard_Places{Places: v}
		case *recapv1.RouteCard:
			rc.Card = &recapv1.RecapCard_Route{Route: v}
		case *recapv1.DaysCard:
			rc.Card = &recapv1.RecapCard_Days{Days: v}
		case *recapv1.TasteCard:
			rc.Card = &recapv1.RecapCard_Taste{Taste: v}
		case *recapv1.HighlightsCard:
			rc.Card = &recapv1.RecapCard_Highlights{Highlights: v}
		case *recapv1.ProgressCard:
			rc.Card = &recapv1.RecapCard_Progress{Progress: v}
		case *recapv1.FieldCard:
			rc.Card = &recapv1.RecapCard_Field{Field: v}
		case *recapv1.FriendsCard:
			rc.Card = &recapv1.RecapCard_Friends{Friends: v}
		case *recapv1.OutroCard:
			rc.Card = &recapv1.RecapCard_Outro{Outro: v}
		}
		return rc
	}
	return &recapv1.Recap{
		Id:          "r1",
		Kind:        recapv1.RecapKind_RECAP_KIND_TRIP,
		Title:       "Five days in Lisbon",
		Subtitle:    "Alfama to Belém",
		PeriodLabel: "Oct 2026",
		Theme:       &recapv1.RecapTheme{AccentHex: "#C63C24"},
		Cards: []*recapv1.RecapCard{
			card("intro", &recapv1.IntroCard{
				Lede:      "Five days of hills, tiles and long lunches by the river.",
				ShortCode: "LIS", PrimaryCity: "Lisbon", Country: "Portugal", CountryCode: "PT",
				CityNames: []string{"Lisbon"}, DayCount: 5,
			}),
			card("places", &recapv1.PlacesCard{
				PlacesCount: 23, CitiesCount: 1, StopsPlanned: 25, StopsDone: 23,
				Headline: "Most of them in Alfama, where you kept getting lost on purpose.",
				Groups: []*recapv1.PlaceGroup{
					{Label: "Alfama", PlacesCount: 9},
					{Label: "Baixa", PlacesCount: 4},
					{Label: "Belém", PlacesCount: 4},
					{Label: "Príncipe Real", PlacesCount: 4},
					{Label: "LX Factory", PlacesCount: 2},
				},
			}),
			card("route", &recapv1.RouteCard{
				DistanceKm: 41, Basis: recapv1.RouteBasis_ROUTE_BASIS_VISITS, Points: pts,
				Headline: "From the castle steps to the river at Belém.",
			}),
			card("days", &recapv1.DaysCard{
				DaysTotal: 5, DaysCompleted: 4, Days: days, BusiestDayNumber: 2,
				Headline: "Four of five days walked end to end.",
			}),
			card("taste", &recapv1.TasteCard{
				Headline: "You came for the wine and stayed for the streets.",
				Traits: []*recapv1.TasteTrait{
					{Label: "Natural wine bars", Strength: 0.92, EvidenceCount: 7, EvidenceKind: recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_SAVES, Trend: recapv1.TasteTrend_TASTE_TREND_STRONG},
					{Label: "Tiled older streets", Strength: 0.7, EvidenceCount: 5, EvidenceKind: recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_VISITS, Trend: recapv1.TasteTrend_TASTE_TREND_GROWING},
					{Label: "Long riverside lunches", Strength: 0.48, EvidenceCount: 3, EvidenceKind: recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_REVIEWS, Trend: recapv1.TasteTrend_TASTE_TREND_NEW},
				},
			}),
			card("highlights", &recapv1.HighlightsCard{
				Headline: "Three you would go back for",
				Highlights: []*recapv1.Highlight{
					{Name: "Miradouro de Santa Luzia", Category: "Viewpoint", Rating: 5, Reason: recapv1.HighlightReason_HIGHLIGHT_REASON_FAVORITED},
					{Name: "Taberna da Rua das Flores", Category: "Restaurant", Rating: 4, Reason: recapv1.HighlightReason_HIGHLIGHT_REASON_REVIEWED},
					{Name: "Mosteiro dos Jerónimos", Category: "Monument", Rating: 5, Reason: recapv1.HighlightReason_HIGHLIGHT_REASON_FIRST_VISIT},
				},
			}),
			card("progress", &recapv1.ProgressCard{
				Points: 1240, StreakDays: 5, Headline: "Your best week on Loci so far.",
				Badges: []*recapv1.EarnedBadge{
					{BadgeId: "b1", Label: "First trip"}, {BadgeId: "b2", Label: "Hill walker"}, {BadgeId: "b3", Label: "Night owl"},
				},
			}),
			card("field", &recapv1.FieldCard{
				CityName: "Lisbon", Score: 860, RankLabel: "Local", PlacesKept: 23, DaysFinished: 4,
				Headline: "You know Lisbon better than most visitors ever will.",
			}),
			card("friends", &recapv1.FriendsCard{
				FriendsBeenCount: 6, Headline: "Ask them where they ate.",
				Friends: []*recapv1.FriendRef{
					{DisplayName: "Ana Sousa"},
					{DisplayName: "Miguel Ferreira"},
					{DisplayName: "Joana"},
					{DisplayName: "Tom Baker"},
					{DisplayName: "Rita Lopes"},
				},
			}),
			card("outro", &recapv1.OutroCard{
				Headline:    "That was Lisbon.",
				SummaryLine: "23 places, 41 km and four full days, mostly on foot.",
			}),
		},
	}
}

func decode(t *testing.T, b []byte, w, h int) {
	t.Helper()
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if got := im.Bounds(); got.Dx() != w || got.Dy() != h {
		t.Fatalf("bounds = %v, want %dx%d", got, w, h)
	}
}

func TestEveryCardRenders(t *testing.T) {
	r := lisbonRecap()
	if len(r.GetCards()) != 10 {
		t.Fatalf("fixture has %d cards, want 10", len(r.GetCards()))
	}
	for _, c := range r.GetCards() {
		t.Run(c.GetId(), func(t *testing.T) {
			t.Parallel()
			b, err := Card(r, c)
			if err != nil {
				t.Fatal(err)
			}
			decode(t, b, StoryW, StoryH)
		})
	}
}

func TestOG(t *testing.T) {
	b, err := OG(lisbonRecap())
	if err != nil {
		t.Fatal(err)
	}
	decode(t, b, OGW, OGH)

	for name, r := range map[string]*recapv1.Recap{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			b, err := OG(r)
			if err != nil {
				t.Fatal(err)
			}
			decode(t, b, OGW, OGH)
		})
	}
}

func TestUnsupportedCard(t *testing.T) {
	for name, c := range map[string]*recapv1.RecapCard{"nil": nil, "empty": {Id: "x"}} {
		if _, err := Card(lisbonRecap(), c); !errors.Is(err, ErrUnsupportedCard) {
			t.Errorf("%s: err = %v, want ErrUnsupportedCard", name, err)
		}
	}
}

// stressRecaps holds pathological inputs: very long strings, unbreakable
// words, huge and negative numbers, NaN coordinates, 50 route points, 30
// days, an invalid accent, and a recap of zero values.
func stressRecaps() map[string]*recapv1.Recap {
	long := strings.Repeat("Llanfairpwllgwyngyllgogerychwyrndrobwllllantysiliogogogoch ", 6)
	word := "Llanfairpwllgwyngyllgogerychwyrndrobwllllantysiliogogogoch"
	pts := make([]*recapv1.RoutePoint, 50)
	for i := range pts {
		pts[i] = &recapv1.RoutePoint{Latitude: 38.7 + float64(i%7)*0.01, Longitude: -9.1 - float64(i)*0.003, Label: long}
	}
	pts[3].Latitude = math.NaN()
	days := make([]*recapv1.DayStub, 30)
	for i := range days {
		days[i] = &recapv1.DayStub{DayNumber: int32(i + 1), CityName: word, StopsDone: 3, StopsTotal: 9, Completed: i%3 == 0}
	}
	groups := make([]*recapv1.PlaceGroup, 12)
	for i := range groups {
		groups[i] = &recapv1.PlaceGroup{Label: long, PlacesCount: int32(i), FirstDate: long}
	}
	big := &recapv1.Recap{
		Title: long, Subtitle: long, PeriodLabel: long,
		Theme: &recapv1.RecapTheme{AccentHex: "not-a-colour"},
		Cards: []*recapv1.RecapCard{
			{Card: &recapv1.RecapCard_Intro{Intro: &recapv1.IntroCard{Lede: long, ShortCode: word, PrimaryCity: word, Country: long, CountryCode: word, DayCount: 99999}}},
			{Card: &recapv1.RecapCard_Places{Places: &recapv1.PlacesCard{PlacesCount: 2147483647, CitiesCount: 400, Groups: groups, Headline: long}}},
			{Card: &recapv1.RecapCard_Route{Route: &recapv1.RouteCard{DistanceKm: 123456789.5, Points: pts, Headline: long}}},
			{Card: &recapv1.RecapCard_Days{Days: &recapv1.DaysCard{DaysTotal: 30, DaysCompleted: 10, Days: days, Headline: long}}},
			{Card: &recapv1.RecapCard_Taste{Taste: &recapv1.TasteCard{Headline: long, Traits: []*recapv1.TasteTrait{
				{Label: long, Strength: 7, EvidenceCount: 1 << 30, Trend: 99},
				{Label: word, Strength: -1, EvidenceCount: 1},
				{Label: "", Strength: math.NaN()},
				{Label: "fourth is dropped"},
			}}}},
			{Card: &recapv1.RecapCard_Highlights{Highlights: &recapv1.HighlightsCard{Headline: long, Highlights: []*recapv1.Highlight{
				{Name: long, Category: long, Rating: 99, Reason: 42},
				{Name: word, Rating: -3},
				{},
				{Name: "dropped"},
			}}}},
			{Card: &recapv1.RecapCard_Progress{Progress: &recapv1.ProgressCard{Points: math.MaxInt64, StreakDays: 9999, Headline: long, Badges: []*recapv1.EarnedBadge{
				{Label: long}, {Label: word}, {}, {Label: "a"}, {Label: "b"}, {Label: "c"}, {Label: "d"}, {Label: "e"},
			}}}},
			{Card: &recapv1.RecapCard_Field{Field: &recapv1.FieldCard{CityName: long, Score: math.MaxInt64, RankLabel: long, PlacesKept: -4, DaysFinished: 1 << 30, Headline: long}}},
			{Card: &recapv1.RecapCard_Friends{Friends: &recapv1.FriendsCard{FriendsBeenCount: 1 << 30, Headline: long, Friends: []*recapv1.FriendRef{
				{DisplayName: long}, {DisplayName: ""}, {DisplayName: "  "}, {DisplayName: "李 小龍"}, {DisplayName: "!!! ???"}, {DisplayName: "dropped"},
			}}}},
			{Card: &recapv1.RecapCard_Outro{Outro: &recapv1.OutroCard{Headline: long, SummaryLine: long}}},
		},
	}
	zero := &recapv1.Recap{Cards: []*recapv1.RecapCard{
		{Card: &recapv1.RecapCard_Intro{Intro: &recapv1.IntroCard{}}},
		{Card: &recapv1.RecapCard_Places{Places: &recapv1.PlacesCard{}}},
		{Card: &recapv1.RecapCard_Places{Places: &recapv1.PlacesCard{StopsDone: 3, StopsPlanned: 8}}},
		{Card: &recapv1.RecapCard_Route{Route: &recapv1.RouteCard{}}},
		{Card: &recapv1.RecapCard_Route{Route: &recapv1.RouteCard{Points: []*recapv1.RoutePoint{{}, {}}}}},
		{Card: &recapv1.RecapCard_Days{Days: &recapv1.DaysCard{}}},
		{Card: &recapv1.RecapCard_Days{Days: &recapv1.DaysCard{DaysTotal: 7, DaysCompleted: 3}}},
		{Card: &recapv1.RecapCard_Taste{Taste: &recapv1.TasteCard{}}},
		{Card: &recapv1.RecapCard_Highlights{Highlights: &recapv1.HighlightsCard{}}},
		{Card: &recapv1.RecapCard_Progress{Progress: &recapv1.ProgressCard{}}},
		{Card: &recapv1.RecapCard_Field{Field: &recapv1.FieldCard{}}},
		{Card: &recapv1.RecapCard_Friends{Friends: &recapv1.FriendsCard{}}},
		{Card: &recapv1.RecapCard_Outro{Outro: &recapv1.OutroCard{}}},
	}}
	return map[string]*recapv1.Recap{"long": big, "zero": zero}
}

func TestStress(t *testing.T) {
	for name, r := range stressRecaps() {
		for i, c := range r.GetCards() {
			t.Run(fmt.Sprintf("%s/%02d", name, i), func(t *testing.T) {
				t.Parallel()
				b, err := Card(r, c)
				if err != nil {
					t.Fatal(err)
				}
				decode(t, b, StoryW, StoryH)
				if name != "zero" {
					return
				}
				// A nil recap: no theme, title or period.
				if b, err = Card(nil, c); err != nil {
					t.Fatalf("nil recap: %v", err)
				}
				decode(t, b, StoryW, StoryH)
			})
		}
		t.Run(name+"/og", func(t *testing.T) {
			t.Parallel()
			b, err := OG(r)
			if err != nil {
				t.Fatal(err)
			}
			decode(t, b, OGW, OGH)
		})
	}
}

func TestConcurrent(t *testing.T) {
	r := lisbonRecap()
	cards := r.GetCards()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			b, err := Card(r, cards[i%len(cards)])
			if err == nil {
				_, err = png.Decode(bytes.NewReader(b))
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestWriteSamples writes one PNG per card plus the OG image for eyeballing.
// It only runs when RECAP_SAMPLES_DIR is set, so CI never writes files.
func TestWriteSamples(t *testing.T) {
	dir := os.Getenv("RECAP_SAMPLES_DIR")
	if dir == "" {
		t.Skip("set RECAP_SAMPLES_DIR to write sample PNGs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := lisbonRecap()
	for i, c := range r.GetCards() {
		b, err := Card(r, c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d-%s.png", i+1, c.GetId())), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := OG(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "11-og.png"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	long := stressRecaps()["long"]
	for i, c := range long.GetCards() {
		b, err := Card(long, c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("stress-%02d.png", i+1)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if b, err = OG(long); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stress-og.png"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
