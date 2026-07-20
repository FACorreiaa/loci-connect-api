package preference

import (
	"math"
	"testing"
)

func TestWeightedAverage(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	got, err := WeightedAverage([][]float32{a, b}, []float32{2, 1})
	if err != nil {
		t.Fatal(err)
	}
	// abs weights 3 → (2*a + 1*b)/3
	want := []float32{2.0 / 3, 1.0 / 3, 0}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-5 {
			t.Fatalf("dim %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestWeightedAverageSkippedPullsAway(t *testing.T) {
	liked := []float32{1, 0}
	skipped := []float32{1, 0}
	got, err := WeightedAverage(
		[][]float32{liked, skipped},
		[]float32{EventMultiplier(EventFavorited), EventMultiplier(EventSkipped)},
	)
	if err != nil {
		t.Fatal(err)
	}
	// 1.5*liked + (-1)*skipped → 0.5 on dim0; absWeight=2.5 → 0.5/2.5=0.2
	if math.Abs(float64(got[0]-0.2)) > 1e-5 {
		t.Fatalf("expected diluted dim0, got %v", got)
	}
}

func TestWeightedAverageEmpty(t *testing.T) {
	if _, err := WeightedAverage(nil, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestBlend(t *testing.T) {
	q := []float32{1, 0}
	u := []float32{0, 1}
	got, err := Blend(q, u, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(got[0]-0.75)) > 1e-5 || math.Abs(float64(got[1]-0.25)) > 1e-5 {
		t.Fatalf("got %v", got)
	}
}

func TestBlendNilUser(t *testing.T) {
	q := []float32{1, 2}
	got, err := Blend(q, nil, DefaultUserBlend)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 1 || got[1] != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestFormatParseVector(t *testing.T) {
	in := []float32{0.5, -1.25, 0}
	raw := FormatVector(in)
	out, err := ParseVector(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("len %d", len(out))
	}
	for i := range in {
		if math.Abs(float64(out[i]-in[i])) > 1e-5 {
			t.Fatalf("dim %d: %v vs %v", i, out[i], in[i])
		}
	}
}

func TestEventMultiplier(t *testing.T) {
	if EventMultiplier(EventSkipped) >= 0 {
		t.Fatal("skipped should be negative")
	}
	if EventMultiplier(EventFavorited) <= EventMultiplier(EventSaved) {
		t.Fatal("favorited should outweigh saved")
	}
}
