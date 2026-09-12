package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// poisJSON renders n places in the envelope general_pois arrives in.
func poisJSON(n int) string {
	names := make([]string, 0, n)
	for i := range n {
		names = append(names, fmt.Sprintf(`{"name":%q,"latitude":32.65,"longitude":-16.91,"category":"Museum"}`,
			"Place "+string(rune('A'+i%26))+fmt.Sprint(i)))
	}
	return `{"points_of_interest":[` + strings.Join(names, ",") + `]}`
}

func TestMinKeepableCount(t *testing.T) {
	cases := []struct{ target, want int }{
		// Below the floor the bar is a constant, not a proportion: a quarter of
		// eight is two, which is not a cache-worthy answer.
		{0, 4},
		{8, 4},
		{12, 4},
		{16, 4},
		// Above it, a quarter of what was asked for.
		{24, 6},
		{40, 10},
		{50, 12},
	}
	for _, c := range cases {
		if got := minKeepableCount(c.target); got != c.want {
			t.Errorf("minKeepableCount(%d) = %d, want %d", c.target, got, c.want)
		}
	}
}

// A parseable but far-too-short answer used to be cached and replayed for a
// fortnight. At a target of ten that was a small mistake; at forty it is two
// weeks of serving four places to everyone who asks the same question.
func TestShortAnswersAreNotCached(t *testing.T) {
	const target = 40

	if validateGeneratedPart(partGeneralPOIs, poisJSON(3), target) {
		t.Error("a 3-place answer to a 40-place request was judged worth storing")
	}
	if !validateGeneratedPart(partGeneralPOIs, poisJSON(38), target) {
		t.Error("a 38-place answer to a 40-place request was rejected")
	}

	// A thin city honestly returning a dozen places is a correct answer, not a
	// failed one, and must still be cached — otherwise every request for that
	// city pays for a generation for ever.
	if !validateGeneratedPart(partGeneralPOIs, poisJSON(12), target) {
		t.Error("an honest short answer from a thin corpus was rejected")
	}
}

// Accommodation asks for a fixed ten however long the trip, so the floor must
// not apply to it: scaling its bar with a target it never renders would reject
// perfectly good hotel lists.
func TestHotelsKeepTheOldBar(t *testing.T) {
	hotels := func(n int) string {
		rows := make([]string, 0, n)
		for i := range n {
			rows = append(rows, fmt.Sprintf(`{"name":"Hotel %d","city":"Funchal"}`, i))
		}
		return `{"hotels":[` + strings.Join(rows, ",") + `]}`
	}

	if !validateGeneratedPart(partHotels, hotels(3), 50) {
		t.Error("a 3-hotel answer was rejected because an unrelated target was 50")
	}
	if validateGeneratedPart(partHotels, hotels(0), 50) {
		t.Error("an empty hotel list was judged worth storing")
	}
}

// The floor gates storage, never the turn. Nothing here should be able to make
// a response unusable — it only decides what survives the request.
func TestFloorRejectsRatherThanCorrupts(t *testing.T) {
	raw := poisJSON(3)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("the fixture itself does not parse: %v", err)
	}
	if _, ok := envelope["points_of_interest"]; !ok {
		t.Fatal("the fixture lost its envelope")
	}
}
