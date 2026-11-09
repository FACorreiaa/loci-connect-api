package placematch

import (
	"sync"
	"testing"
)

func TestNameSimilarity(t *testing.T) {
	ig := map[string]bool{"hotel": true}
	cases := []struct {
		a, b string
		want float64
	}{
		{"Pastéis de Belém", "Antiga Confeitaria de Belém - Pastéis de Belém", 1},
		{"HOTEL ARTS", "Arts", 1},
		{"Casa Fuster", "Casa Batlló", 1.0 / 3},
		{"", "x", 0},
	}
	for _, c := range cases {
		if got := NameSimilarity(c.a, c.b, ig); got != c.want {
			t.Errorf("NameSimilarity(%q, %q) = %v want %v", c.a, c.b, got, c.want)
		}
	}
}

// Fold is called from concurrent matchers; a shared transformer panics.
func TestFoldConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 200 {
				if Fold("Hôtel Gràcia Ñandú") != "hotel gracia nandu" {
					t.Error("fold")
					return
				}
			}
		})
	}
	wg.Wait()
}
