package forge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/bundle"
)

func TestLoadSeeds_EveryThemeIsInTheVocabulary(t *testing.T) {
	seeds, err := LoadSeeds()
	require.NoError(t, err)
	assert.NotEmpty(t, seeds)
	for _, s := range seeds {
		assert.True(t, ValidTheme(s.Theme), "%s has theme %q", s.City, s.Theme)
		assert.NotEmpty(t, s.Months)
		assert.Positive(t, s.Days)
	}
}

func TestSeedSlug_IsUrlSafeAndUnique(t *testing.T) {
	seeds, err := LoadSeeds()
	require.NoError(t, err)

	seen := map[string]string{}
	for _, s := range seeds {
		slug := s.Slug()
		assert.NotContains(t, slug, " ", "%s", s.City)
		assert.Equal(t, strings.ToLower(slug), slug)
		// Tromsø and the like must not leave non-ascii in a URL.
		for _, r := range slug {
			assert.Less(t, r, rune(128), "slug %q keeps a non-ascii rune", slug)
		}
		if prev, dup := seen[slug]; dup {
			t.Fatalf("slug %q collides: %s and %s", slug, prev, s.City)
		}
		seen[slug] = s.City
	}
}

func TestParsePlan_SurvivesAFencedCodeBlock(t *testing.T) {
	raw := "Sure, here you go:\n```json\n" +
		`{"summary":"s","days":[{"day_number":1,"title":"t","stops":[{"name":"n"}]}]}` +
		"\n```\n"
	p, err := ParsePlan(raw)
	require.NoError(t, err)
	assert.Equal(t, "s", p.Summary)
	require.Len(t, p.Days, 1)
	assert.Equal(t, "n", p.Days[0].Stops[0].Name)
}

func TestParsePlan_RejectsAnEmptyPlan(t *testing.T) {
	_, err := ParsePlan(`{"summary":"s","days":[]}`)
	assert.Error(t, err)
}

// Models skip, repeat and zero-index day numbers. A pack numbered "Day 0" or
// "Day 2 to Day 5" reads as broken even when the content is fine.
func TestToDraft_RenumbersDaysByPosition(t *testing.T) {
	s := Seed{City: "Lisbon", CountryCode: "PT", Months: []int{9}, Hook: "the light", Days: 3, Theme: "food"}
	p := Plan{
		Summary: "x",
		Days: []PlanDay{
			{DayNumber: 0, Title: "a", Stops: []PlanStop{{Name: "one", Latitude: 38.7, Longitude: -9.1}}},
			{DayNumber: 7, Title: "b", Stops: []PlanStop{{Name: "two", Latitude: 38.7, Longitude: -9.1}}},
		},
	}

	d := ToDraft(s, p, "model-x", nil, true)
	require.Len(t, d.Days, 2)
	assert.Equal(t, 1, d.Days[0].DayNumber)
	assert.Equal(t, 2, d.Days[1].DayNumber)
	assert.Equal(t, "lisbon-light-3day", d.Slug)
	assert.True(t, d.IsPaid)
	assert.Equal(t, []int16{9}, d.Months)
}

func TestToDraft_LeavesMissingCoordinatesNilRatherThanZero(t *testing.T) {
	s := Seed{City: "Lisbon", Months: []int{9}, Days: 1, Theme: "food"}
	p := Plan{Days: []PlanDay{{Stops: []PlanStop{{Name: "nowhere"}}}}}

	d := ToDraft(s, p, "m", nil, false)
	stop := d.Days[0].Stops[0]
	// 0,0 is the Atlantic. Storing it would put a stop in the ocean and pass a
	// null check; nil is what lets Validate catch it.
	assert.Nil(t, stop.Latitude)
	assert.Nil(t, stop.Longitude)
}

func TestValidate_RefusesWhatWouldBeABrokenProduct(t *testing.T) {
	lat, lon := 38.7, -9.1
	good := bundle.Stop{Name: "ok", Latitude: &lat, Longitude: &lon, Notes: "why"}

	cases := []struct {
		name  string
		b     *bundle.Bundle
		days  []bundle.Day
		wants string
	}{
		{
			name:  "a stop with no coordinates",
			b:     &bundle.Bundle{Summary: "s", DayCount: 1},
			days:  []bundle.Day{{DayNumber: 1, Stops: []bundle.Stop{{Name: "ghost", Notes: "why"}}}},
			wants: "has no coordinates",
		},
		{
			name:  "an empty day",
			b:     &bundle.Bundle{Summary: "s", DayCount: 1},
			days:  []bundle.Day{{DayNumber: 1}},
			wants: "has no stops",
		},
		{
			name:  "a stop with no notes, which is the thing being sold",
			b:     &bundle.Bundle{Summary: "s", DayCount: 1},
			days:  []bundle.Day{{DayNumber: 1, Stops: []bundle.Stop{{Name: "x", Latitude: &lat, Longitude: &lon}}}},
			wants: "has no notes",
		},
		{
			name:  "a blank summary leaves the catalog card empty",
			b:     &bundle.Bundle{DayCount: 1},
			days:  []bundle.Day{{DayNumber: 1, Stops: []bundle.Stop{good}}},
			wants: "no summary",
		},
		{
			name:  "a day count that disagrees with the rows",
			b:     &bundle.Bundle{Summary: "s", DayCount: 4},
			days:  []bundle.Day{{DayNumber: 1, Stops: []bundle.Stop{good}}},
			wants: "day_count is 4",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := Validate(tc.b, tc.days)
			require.NotEmpty(t, issues)
			assert.Contains(t, strings.Join(issues, "\n"), tc.wants)
		})
	}
}

func TestValidate_PassesAGoodPack(t *testing.T) {
	lat, lon := 38.7, -9.1
	b := &bundle.Bundle{Summary: "a real summary", DayCount: 2}
	days := []bundle.Day{
		{DayNumber: 1, Stops: []bundle.Stop{{Name: "a", Latitude: &lat, Longitude: &lon, Notes: "why a"}}},
		{DayNumber: 2, Stops: []bundle.Stop{{Name: "b", Latitude: &lat, Longitude: &lon, Notes: "why b"}}},
	}
	assert.Empty(t, Validate(b, days))
}

func TestRender_FlagsAProblemWhereTheReaderIsLooking(t *testing.T) {
	b := &bundle.Bundle{Title: "T", Slug: "s", Summary: "x", DayCount: 1}
	days := []bundle.Day{{DayNumber: 1, Title: "d", Stops: []bundle.Stop{{Name: "ghost", Notes: "n"}}}}

	out := Render(b, days)
	assert.Contains(t, out, "NO COORDINATES")
	assert.Contains(t, out, "Blocking issues")
}
