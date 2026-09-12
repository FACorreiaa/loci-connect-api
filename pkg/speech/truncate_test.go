package speech

import (
	"strings"
	"testing"
)

// The hint is user-adjacent text — place names, in any language — so cutting it
// on a byte boundary would split a multi-byte character and send a broken one.
func TestTruncateCutsOnRuneBoundaries(t *testing.T) {
	// "Belém" and "Sodré" both carry two-byte characters.
	long := strings.Repeat("Belém, Cais do Sodré, ", 200)

	for _, limit := range []int{1, 2, 3, 7, 8, 50, 801} {
		got := truncate(long, limit)
		if len(got) > limit {
			t.Errorf("truncate(limit=%d) returned %d bytes", limit, len(got))
		}
		if !utf8Valid(got) {
			t.Errorf("truncate(limit=%d) split a character: %q", limit, got)
		}
	}

	if got := truncate("Belém", 100); got != "Belém" {
		t.Errorf("a short hint was altered: %q", got)
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
