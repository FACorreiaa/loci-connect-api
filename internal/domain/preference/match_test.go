package preference

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRescale(t *testing.T) {
	cases := []struct {
		sim, p10, p90 float64
		want          float32
		ok            bool
	}{
		{0.5, 0.2, 0.8, 0.5, true},
		{0.9, 0.2, 0.8, 1, true},
		{0.1, 0.2, 0.8, 0, true},
		{0.5, 0.5, 0.505, 0, false},
	}
	for _, c := range cases {
		got, ok := rescale(c.sim, c.p10, c.p90)
		if ok != c.ok || (ok && absf(got-c.want) > 1e-6) {
			t.Errorf("rescale(%v, %v, %v) = %v, %v; want %v, %v", c.sim, c.p10, c.p90, got, ok, c.want, c.ok)
		}
	}
}

func TestTraitReason(t *testing.T) {
	cases := []struct {
		label             string
		score, confidence float64
		want              string
	}{
		{"Cafe", 0.6, 0.5, "You tend to like cafe"},
		{"Museum", -0.5, 0.4, "You've passed on museum before"},
		{"Bar", 0.6, 0.1, ""},
		{"Park", 0.2, 0.9, ""},
	}
	for _, c := range cases {
		if got := traitReason(c.label, c.score, c.confidence); got != c.want {
			t.Errorf("traitReason(%q, %v, %v) = %q; want %q", c.label, c.score, c.confidence, got, c.want)
		}
	}
}

func TestVerdictReason(t *testing.T) {
	want := map[int16]string{1: "You rated it Loved", 2: "You rated it Fine", 3: "You rated it Not for me", 0: ""}
	for v, w := range want {
		if got := verdictReason(v); got != w {
			t.Errorf("verdictReason(%d) = %q; want %q", v, got, w)
		}
	}
}

func TestLikeReason_CapsAt120Runes(t *testing.T) {
	if got := likeReason("Manteigaria"); got != "Similar to Manteigaria, one of your picks" {
		t.Fatalf("likeReason = %q", got)
	}
	long := likeReason(strings.Repeat("é", 200))
	if n := utf8.RuneCountInString(long); n > 120 {
		t.Fatalf("likeReason length = %d runes, want ≤ 120", n)
	}
}

func absf(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
