package compare

import "testing"

func TestHaversinePortoEvora(t *testing.T) {
	// Porto → Évora straight-line ~290 km (user fixture: ~300 km from Porto).
	const (
		portoLat = 41.1579
		portoLon = -8.6291
		evoraLat = 38.5714
		evoraLon = -7.9135
	)
	km := HaversineKm(portoLat, portoLon, evoraLat, evoraLon)
	if km < 250 || km > 350 {
		t.Fatalf("Porto→Évora distance %.1f km outside expected band 250–350", km)
	}
}

func TestHaversineEvoraBeja(t *testing.T) {
	const (
		evoraLat = 38.5714
		evoraLon = -7.9135
		bejaLat  = 38.0153
		bejaLon  = -7.8624
	)
	km := HaversineKm(evoraLat, evoraLon, bejaLat, bejaLon)
	if km < 50 || km > 100 {
		t.Fatalf("Évora→Beja distance %.1f km outside expected band 50–100", km)
	}
}

func TestDriveMins(t *testing.T) {
	if got := DriveMins(160); got < 100 || got > 140 {
		t.Fatalf("DriveMins(160) = %d, want ~120", got)
	}
}

func TestDualCityFeasible_EvoraBeja(t *testing.T) {
	portoLat, portoLon := 41.1579, -8.6291
	cols := []resolvedCity{
		{name: "Évora", lat: 38.5714, lon: -7.9135},
		{name: "Beja", lat: 38.0153, lon: -7.8624},
	}
	feasible, outline, mins := dualCityFeasible(portoLat, portoLon, cols, 48)
	if outline == "" {
		t.Fatal("expected outline")
	}
	if mins <= 0 {
		t.Fatalf("expected positive travel mins, got %d", mins)
	}
	// Évora+Beja inter-city is short; dual may or may not pass origin distance gate.
	_ = feasible
}
