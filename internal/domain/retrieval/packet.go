// Package retrieval assembles the evidence a generator is allowed to speak
// from, and verifies afterwards that it did.
//
// The contract is deliberately narrow and deterministic:
//
//   - Assemble gathers candidate POIs plus the facts, visit history, and taste
//     labels that already exist in the database but never reached a prompt, and
//     returns them as an ordered ContextPacket with a stable PacketID.
//   - Render turns that packet into a prompt block in which every place carries
//     its own identifier.
//   - Verify reads the model's answer back and reports which places were cited
//     from the packet, which were cited but unknown, and which were asserted
//     with no citation at all.
//
// Nothing here calls an LLM. The same packet that shaped a prompt is persisted
// to answer_evidence, so a recommendation can always be traced to the rows that
// produced it.
package retrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MatchReason records why a candidate entered the packet. It is carried through
// to the client and to MCP consumers so a recommendation can explain itself.
type MatchReason string

const (
	MatchLexical  MatchReason = "lexical"
	MatchSemantic MatchReason = "semantic"
	MatchBoth     MatchReason = "both"
	MatchNearby   MatchReason = "nearby"
	MatchVisited  MatchReason = "visited"
)

// Fact is a crowd-verified assertion about a place, sourced from place_facts.
// Only unexpired facts are attached; expiry is field-dependent and enforced by
// the query, not here.
type Fact struct {
	Field            string    `json:"field"`
	Value            string    `json:"value"`
	Confidence       float64   `json:"confidence"`
	ContributorCount int32     `json:"contributor_count"`
	VerifiedAt       time.Time `json:"verified_at"`
}

// Evidence is one candidate place offered to the generator, with everything
// needed to cite it.
type Evidence struct {
	POIID       uuid.UUID `json:"poi_id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Description string    `json:"description,omitempty"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	Address     string    `json:"address,omitempty"`

	// DistanceKm is populated only when the caller genuinely measured a
	// distance. It is NOT read from POIDetailedInfo.Distance: that field is
	// overloaded across the POI repository — kilometres on the spatial paths
	// (SearchPOIsHybrid), but a raw cosine similarity score on the vector paths
	// (FindSimilarPOIs, FindSimilarPOIsByCity). Rendering a similarity score as
	// "300m away" would be a confident lie, so distance must be passed in
	// explicitly by a caller that knows which lane produced it.
	DistanceKm  float64     `json:"distance_km,omitempty"`
	Source      string      `json:"source,omitempty"`
	MatchReason MatchReason `json:"match_reason"`
	Rank        int         `json:"rank"`

	// Facts are crowd-verified fields from place_facts, unexpired at assembly
	// time. LastVerifiedAt is the most recent verification across them.
	Facts          []Fact     `json:"facts,omitempty"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`

	// Visited marks a place the user has already been to. It stays in the
	// packet — knowing where someone has been is useful context — but the
	// prompt instructs the model not to re-pitch it as a discovery.
	Visited bool `json:"visited"`
}

// ContextPacket is the complete, ordered evidence set for one generation.
type ContextPacket struct {
	PacketID    string     `json:"packet_id"`
	Query       string     `json:"query,omitempty"`
	CityID      uuid.UUID  `json:"city_id,omitempty"`
	CityName    string     `json:"city_name,omitempty"`
	Evidence    []Evidence `json:"evidence"`
	TraitLabels []string   `json:"trait_labels,omitempty"`
	AssembledAt time.Time  `json:"assembled_at"`
}

// RecordedEvidence is one persisted answer_evidence row, read back for display
// or audit. A row with Grounded false is an identifier the model invented.
type RecordedEvidence struct {
	PacketID    string    `json:"packet_id"`
	POIID       string    `json:"poi_id"`
	Rank        int       `json:"rank"`
	Cited       bool      `json:"cited"`
	Grounded    bool      `json:"grounded"`
	MatchReason string    `json:"match_reason"`
	CreatedAt   time.Time `json:"created_at"`
}

// IsEmpty reports whether the packet carries no candidates. An empty packet is
// valid: it means retrieval found nothing, and the caller should say so rather
// than let the model fill the silence.
func (p *ContextPacket) IsEmpty() bool {
	return p == nil || len(p.Evidence) == 0
}

// IDs returns the packet's POI identifiers in packet order.
func (p *ContextPacket) IDs() []uuid.UUID {
	if p == nil {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(p.Evidence))
	for _, e := range p.Evidence {
		ids = append(ids, e.POIID)
	}
	return ids
}

// Has reports whether id is part of this packet.
func (p *ContextPacket) Has(id uuid.UUID) bool {
	if p == nil {
		return false
	}
	for _, e := range p.Evidence {
		if e.POIID == id {
			return true
		}
	}
	return false
}

// computePacketID derives a stable identifier from the inputs that define the
// packet: the requesting user, the query, the city, and the ordered candidate
// set. Identical retrieval always yields an identical ID, so the same packet
// recurring across turns is recognizable in answer_evidence.
//
// Deliberately excludes wall-clock time and any machine identity.
func computePacketID(userID uuid.UUID, query string, cityID uuid.UUID, ids []uuid.UUID) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", userID, strings.TrimSpace(strings.ToLower(query)), cityID)
	for _, id := range ids {
		h.Write([]byte{0})
		h.Write([]byte(id.String()))
	}
	return "pkt_" + hex.EncodeToString(h.Sum(nil))[:32]
}

// citationRE matches the citation form the prompt asks for: [poi:<uuid>].
// Case-insensitive on the tag and tolerant of internal whitespace, because
// models are inconsistent about both.
var citationRE = regexp.MustCompile(`(?i)\[\s*poi\s*:\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\s*\]`)

// ParseCitations extracts every [poi:<uuid>] citation from generated text, in
// order of first appearance and de-duplicated.
func ParseCitations(text string) []uuid.UUID {
	matches := citationRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(matches))
	out := make([]uuid.UUID, 0, len(matches))
	for _, m := range matches {
		id, err := uuid.Parse(m[1])
		if err != nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// StripCitations removes citation markers from text destined for a human
// reader. The parsed IDs are kept structurally; the prose should not show them.
func StripCitations(text string) string {
	cleaned := citationRE.ReplaceAllString(text, "")
	// Collapse the double spaces and orphaned spaces-before-punctuation that
	// removal leaves behind.
	cleaned = regexp.MustCompile(`[ \t]{2,}`).ReplaceAllString(cleaned, " ")
	cleaned = regexp.MustCompile(`[ \t]+([.,;:!?)])`).ReplaceAllString(cleaned, "$1")
	return strings.TrimSpace(cleaned)
}

// Verification is the result of checking generated output against the packet
// that was offered to it.
type Verification struct {
	// Grounded are packet IDs the model actually cited.
	Grounded []uuid.UUID
	// Unknown are IDs the model cited that were never in the packet. These are
	// fabrications wearing a citation and must not be trusted.
	Unknown []uuid.UUID
	// Unused are packet IDs the model ignored. Not an error — useful signal.
	Unused []uuid.UUID
}

// GroundedRatio is the share of cited identifiers that came from the packet.
// Returns 0 when nothing was cited, which is the worst case, not a neutral one.
func (v Verification) GroundedRatio() float64 {
	total := len(v.Grounded) + len(v.Unknown)
	if total == 0 {
		return 0
	}
	return float64(len(v.Grounded)) / float64(total)
}

// Verify partitions the citations found in text against the packet.
func Verify(p *ContextPacket, text string) Verification {
	cited := ParseCitations(text)

	inPacket := make(map[uuid.UUID]bool, len(p.IDs()))
	for _, id := range p.IDs() {
		inPacket[id] = true
	}

	var result Verification
	citedSet := make(map[uuid.UUID]struct{}, len(cited))
	for _, id := range cited {
		citedSet[id] = struct{}{}
		if inPacket[id] {
			result.Grounded = append(result.Grounded, id)
		} else {
			result.Unknown = append(result.Unknown, id)
		}
	}
	for _, id := range p.IDs() {
		if _, ok := citedSet[id]; !ok {
			result.Unused = append(result.Unused, id)
		}
	}
	return result
}

// Render turns the packet into the prompt block the generator sees.
//
// Two properties matter and are tested: every place carries its identifier, and
// the instruction to cite is unambiguous. Everything else is presentation.
func Render(p *ContextPacket) string {
	if p.IsEmpty() {
		return "**VERIFIED PLACES:** none found in Loci's database for this request. " +
			"Say so plainly rather than inventing places, and keep any general " +
			"suggestions clearly marked as unverified.\n"
	}

	var b strings.Builder
	b.WriteString("**VERIFIED PLACES FROM LOCI'S DATABASE**\n")
	b.WriteString("These are real, stored places. Prefer them over anything you recall.\n\n")

	for _, e := range p.Evidence {
		fmt.Fprintf(&b, "- [poi:%s] %s (%s)", e.POIID, e.Name, e.Category)
		if e.DistanceKm > 0 {
			fmt.Fprintf(&b, " — %.1f km away", e.DistanceKm)
		}
		if e.Visited {
			b.WriteString(" — ALREADY VISITED by this user")
		}
		b.WriteString("\n")

		if desc := TruncateRunes(strings.TrimSpace(e.Description), MaxDescriptionChars); desc != "" {
			fmt.Fprintf(&b, "    %s\n", desc)
		}
		if e.Address != "" {
			fmt.Fprintf(&b, "    Address: %s\n", e.Address)
		}
		for _, f := range e.Facts {
			fmt.Fprintf(&b, "    Verified %s: %s (%d contributors, verified %s)\n",
				f.Field, f.Value, f.ContributorCount, f.VerifiedAt.Format("2006-01-02"))
		}
	}

	if len(p.TraitLabels) > 0 {
		fmt.Fprintf(&b, "\n**Learned preferences:** %s\n", strings.Join(p.TraitLabels, ", "))
	}

	b.WriteString(`
**CITATION RULES — these are not optional:**
1. Every place you recommend that appears above MUST be followed by its exact
   marker, e.g. "Time Out Market [poi:` + p.Evidence[0].POIID.String() + `]".
2. Copy identifiers exactly. Never invent, alter, or reuse one for a different place.
3. You may suggest a place that is not listed above, but do NOT attach a marker
   to it — an uncited place is understood to be unverified.
4. Do not re-pitch a place marked ALREADY VISITED as a new discovery; reference
   it only as context the user already knows.
`)

	return b.String()
}

// sortEvidence orders a packet deterministically: rank ascending, then POI ID
// as the tiebreak, so two assemblies over the same rows produce the same
// PacketID regardless of how the database returned them.
func sortEvidence(ev []Evidence) {
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].Rank != ev[j].Rank {
			return ev[i].Rank < ev[j].Rank
		}
		return ev[i].POIID.String() < ev[j].POIID.String()
	})
}
