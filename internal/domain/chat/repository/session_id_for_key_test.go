package repository

import (
	"testing"

	"github.com/google/uuid"
)

func TestSessionIDForKey(t *testing.T) {
	real := uuid.MustParse("6b2a1d3e-4f0c-4a9b-8c1d-2e3f4a5b6c7d")
	if got := sessionIDForKey(real.String()); got != real {
		t.Fatalf("a real session id must round-trip, got %s", got)
	}

	synthesized := sessionIDForKey("Porto_2026-09-22")
	if synthesized == uuid.Nil || synthesized.Version() != 5 {
		t.Fatalf("a synthesized key must become a stable v5 UUID, got %s (v%d)", synthesized, synthesized.Version())
	}
	if sessionIDForKey("Porto_2026-09-22") != synthesized {
		t.Fatal("synthesized ids must be stable across calls")
	}
}
