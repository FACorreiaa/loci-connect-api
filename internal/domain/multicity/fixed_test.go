package multicity

import "testing"

func TestPlanFixed_KeepsOrderAndNights(t *testing.T) {
	got := PlanFixed(Input{MultiModal: true}, []FixedStop{{City: lisbon, Nights: 3}, {City: porto, Nights: 2}})

	if !got.Feasible || len(got.Cities) != 2 || got.Cities[0].Name != "Lisbon" {
		t.Fatalf("expected Lisbon then Porto, got %v", cityNames(got.Cities))
	}
	if len(got.Days) != 5 {
		t.Fatalf("expected 5 days, got %d", len(got.Days))
	}
	want := []string{"Lisbon", "Lisbon", "Lisbon", "Porto", "Porto"}
	for i, d := range got.Days {
		if d.CityName != want[i] || d.DayNumber != i+1 {
			t.Errorf("day %d = %s/%d, want %s/%d", i, d.CityName, d.DayNumber, want[i], i+1)
		}
	}
	if !got.Days[3].TravelDay || got.Days[1].TravelDay {
		t.Errorf("day 4 (arrive Porto) should be the only travel day after day 1")
	}
	if len(got.Legs) != 1 || got.Legs[0].AfterDay != 3 || got.Legs[0].Mode != "train" {
		t.Fatalf("expected one train leg after day 3, got %+v", got.Legs)
	}
}

func TestPlanFixed_DuplicatesCollapse(t *testing.T) {
	got := PlanFixed(Input{MultiModal: true}, []FixedStop{
		{City: lisbon, Nights: 2}, {City: porto, Nights: 1}, {City: lisbon, Nights: 1},
	})
	if len(got.Cities) != 2 {
		t.Fatalf("expected duplicates collapsed, got %v", cityNames(got.Cities))
	}
	if n := countDays(got.Days, "Lisbon"); n != 3 {
		t.Errorf("Lisbon should keep the combined 3 nights, got %d", n)
	}
}

func TestPlanFixed_ZeroNightsIsOne(t *testing.T) {
	got := PlanFixed(Input{MultiModal: true}, []FixedStop{{City: lisbon}, {City: porto, Nights: -2}})
	if len(got.Days) != 2 {
		t.Fatalf("expected one day per city, got %d", len(got.Days))
	}
}

func countDays(days []DayPlan, city string) int {
	n := 0
	for _, d := range days {
		if d.CityName == city {
			n++
		}
	}
	return n
}
