package retrieval

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}

const (
	idA = "11111111-1111-4111-8111-111111111111"
	idB = "22222222-2222-4222-8222-222222222222"
	idC = "33333333-3333-4333-8333-333333333333"
)

func packetWith(t *testing.T, ids ...string) *ContextPacket {
	t.Helper()
	p := &ContextPacket{}
	for i, s := range ids {
		p.Evidence = append(p.Evidence, Evidence{
			POIID:       mustUUID(t, s),
			Name:        "Place " + s[:2],
			Category:    "bar",
			MatchReason: MatchSemantic,
			Rank:        i,
		})
	}
	return p
}

func TestComputePacketIDIsStableAndOrderSensitive(t *testing.T) {
	user := mustUUID(t, idA)
	city := mustUUID(t, idB)
	ids := []uuid.UUID{mustUUID(t, idA), mustUUID(t, idB)}

	first := computePacketID(user, "wine bars", city, ids)
	second := computePacketID(user, "wine bars", city, ids)
	if first != second {
		t.Fatalf("packet id not deterministic: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "pkt_") || len(first) != len("pkt_")+32 {
		t.Fatalf("unexpected packet id shape: %q", first)
	}

	// Query normalization: case and surrounding whitespace must not change it.
	if got := computePacketID(user, "  Wine Bars  ", city, ids); got != first {
		t.Errorf("query normalization failed: got %q want %q", got, first)
	}

	// A different candidate order is a different packet.
	reversed := []uuid.UUID{ids[1], ids[0]}
	if got := computePacketID(user, "wine bars", city, reversed); got == first {
		t.Error("packet id ignored candidate order")
	}

	// A different user is a different packet, even for identical retrieval.
	if got := computePacketID(mustUUID(t, idC), "wine bars", city, ids); got == first {
		t.Error("packet id ignored user identity")
	}
}

func TestParseCitations(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"none", "Just go somewhere nice.", nil},
		{"single", "Try Bar Alta [poi:" + idA + "] tonight.", []string{idA}},
		{
			"multiple in order",
			"A [poi:" + idB + "] then B [poi:" + idA + "]",
			[]string{idB, idA},
		},
		{
			"deduplicated",
			"A [poi:" + idA + "] and again [poi:" + idA + "]",
			[]string{idA},
		},
		{
			"tolerates whitespace and case",
			"X [ POI : " + strings.ToUpper(idA) + " ]",
			[]string{idA},
		},
		{"rejects malformed uuid", "X [poi:not-a-uuid]", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCitations(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d citations %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != mustUUID(t, tc.want[i]) {
					t.Errorf("citation %d = %s, want %s", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestStripCitationsLeavesReadableProse(t *testing.T) {
	in := "Start at Bar Alta [poi:" + idA + "], then Café Sul [poi:" + idB + "]."
	got := StripCitations(in)
	want := "Start at Bar Alta, then Café Sul."
	if got != want {
		t.Errorf("StripCitations() = %q, want %q", got, want)
	}
}

// The property this whole phase exists for: an identifier the model invented
// must never be reported as grounded.
func TestVerifySeparatesFabricatedCitations(t *testing.T) {
	p := packetWith(t, idA, idB)
	text := "Go to A [poi:" + idA + "] and to the wonderful Nonexistent Bar [poi:" + idC + "]."

	v := Verify(p, text)

	if len(v.Grounded) != 1 || v.Grounded[0] != mustUUID(t, idA) {
		t.Errorf("Grounded = %v, want [%s]", v.Grounded, idA)
	}
	if len(v.Unknown) != 1 || v.Unknown[0] != mustUUID(t, idC) {
		t.Errorf("Unknown = %v, want [%s]", v.Unknown, idC)
	}
	if len(v.Unused) != 1 || v.Unused[0] != mustUUID(t, idB) {
		t.Errorf("Unused = %v, want [%s]", v.Unused, idB)
	}
	if got := v.GroundedRatio(); got != 0.5 {
		t.Errorf("GroundedRatio() = %v, want 0.5", got)
	}
}

func TestVerifyUncitedAnswerScoresZero(t *testing.T) {
	p := packetWith(t, idA, idB)

	v := Verify(p, "Lisbon has many lovely bars worth your time.")

	if len(v.Grounded) != 0 || len(v.Unknown) != 0 {
		t.Fatalf("expected no citations, got grounded=%v unknown=%v", v.Grounded, v.Unknown)
	}
	if len(v.Unused) != 2 {
		t.Errorf("Unused = %v, want both packet ids", v.Unused)
	}
	// Nothing cited is the worst case, not a neutral one.
	if got := v.GroundedRatio(); got != 0 {
		t.Errorf("GroundedRatio() = %v, want 0", got)
	}
}

func TestRenderCarriesEveryIdentifier(t *testing.T) {
	p := packetWith(t, idA, idB)
	p.Evidence[1].Visited = true

	out := Render(p)

	for _, id := range []string{idA, idB} {
		if !strings.Contains(out, "[poi:"+id+"]") {
			t.Errorf("rendered packet missing marker for %s", id)
		}
	}
	if !strings.Contains(out, "ALREADY VISITED") {
		t.Error("visited place not flagged in rendered packet")
	}
	if !strings.Contains(out, "CITATION RULES") {
		t.Error("rendered packet omits the citation instruction")
	}
}

// An empty packet must produce an explicit "we found nothing" instruction. The
// failure mode being prevented is a silent prompt that invites the model to
// fill the gap from memory.
func TestRenderEmptyPacketSaysSo(t *testing.T) {
	out := Render(&ContextPacket{})

	if !strings.Contains(out, "none found") {
		t.Errorf("empty packet render does not state the absence: %q", out)
	}
	if strings.Contains(out, "[poi:") {
		t.Error("empty packet render fabricated a marker")
	}
}

func TestClampLimit(t *testing.T) {
	tests := []struct {
		value, fallback, maximum, want int
	}{
		{0, 10, 20, 10},   // unset falls back
		{-5, 10, 20, 10},  // negative falls back
		{5, 10, 20, 5},    // in range passes through
		{999, 10, 20, 20}, // over maximum clamps
	}
	for _, tc := range tests {
		if got := ClampLimit(tc.value, tc.fallback, tc.maximum); got != tc.want {
			t.Errorf("ClampLimit(%d, %d, %d) = %d, want %d",
				tc.value, tc.fallback, tc.maximum, got, tc.want)
		}
	}
}

func TestValidateLimitRejectsOutOfRange(t *testing.T) {
	if _, err := ValidateLimit(0, "limit", MaxSearchResults); err == nil {
		t.Error("expected error for limit below minimum")
	}
	if _, err := ValidateLimit(MaxSearchResults+1, "limit", MaxSearchResults); err == nil {
		t.Error("expected error for limit above maximum")
	}
	got, err := ValidateLimit(5, "limit", MaxSearchResults)
	if err != nil || got != 5 {
		t.Errorf("ValidateLimit(5) = %d, %v; want 5, nil", got, err)
	}
}

func TestTruncateRunesIsRuneSafe(t *testing.T) {
	// Multi-byte characters must not be split mid-character.
	got := TruncateRunes("Café Sul São Paulo", 6)
	if got != "Café S…" {
		t.Errorf("TruncateRunes() = %q, want %q", got, "Café S…")
	}
	if got := TruncateRunes("short", 50); got != "short" {
		t.Errorf("TruncateRunes() shortened a short string: %q", got)
	}
}
