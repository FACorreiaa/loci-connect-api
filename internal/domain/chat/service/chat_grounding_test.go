package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func testPacket(ids ...uuid.UUID) *retrieval.ContextPacket {
	p := &retrieval.ContextPacket{PacketID: "pkt_test"}
	for i, id := range ids {
		p.Evidence = append(p.Evidence, retrieval.Evidence{
			POIID:       id,
			Name:        "Retrieved Place",
			Category:    "bar",
			MatchReason: retrieval.MatchSemantic,
			Rank:        i,
		})
	}
	return p
}

// The core safety property of grounding: an identifier the model invented must
// be stripped before anything downstream can persist it. canonicalizePOIs runs
// after this and would otherwise treat a fabricated UUID as a retrieved row.
func TestNeutralizeFabricatedIDsStripsInventedIdentifiers(t *testing.T) {
	real := uuid.New()
	invented := uuid.New()

	cc := &common.ChatContext{Packet: testPacket(real)}
	data := &locitypes.AiCityResponse{
		PointsOfInterest: []locitypes.POIDetailedInfo{
			{ID: real, Name: "Bar Alta"},
			{ID: invented, Name: "Bar That Does Not Exist"},
			{Name: "Unnamed suggestion with no id"},
		},
	}

	l := &ServiceImpl{}
	grounded, fabricated := l.neutralizeFabricatedIDs(cc, data)

	if len(grounded) != 1 || grounded[0] != real {
		t.Errorf("grounded = %v, want [%s]", grounded, real)
	}
	if len(fabricated) != 1 || fabricated[0] != invented {
		t.Errorf("fabricated = %v, want [%s]", fabricated, invented)
	}

	if !data.PointsOfInterest[0].Grounded {
		t.Error("retrieved place was not marked grounded")
	}
	if data.PointsOfInterest[0].ID != real {
		t.Error("retrieved place lost its identifier")
	}

	if data.PointsOfInterest[1].ID != uuid.Nil {
		t.Errorf("fabricated identifier survived: %s", data.PointsOfInterest[1].ID)
	}
	if data.PointsOfInterest[1].Grounded {
		t.Error("fabricated place was marked grounded")
	}
	// The suggestion itself is kept — it may be a real place we simply do not
	// have. Only the false claim of provenance is removed.
	if data.PointsOfInterest[1].Name == "" {
		t.Error("fabricated place was dropped entirely; only its id should be")
	}
}

func TestNeutralizeFabricatedIDsCoversHotelsAndRestaurants(t *testing.T) {
	real := uuid.New()
	invented := uuid.New()

	cc := &common.ChatContext{Packet: testPacket(real)}
	data := &locitypes.AiCityResponse{
		Hotels:      []locitypes.HotelDetailedInfo{{ID: invented, Name: "Ghost Hotel"}},
		Restaurants: []locitypes.RestaurantDetailedInfo{{ID: real, Name: "Real Tasca"}},
	}

	l := &ServiceImpl{}
	grounded, fabricated := l.neutralizeFabricatedIDs(cc, data)

	if len(fabricated) != 1 || fabricated[0] != invented {
		t.Errorf("fabricated = %v, want [%s]", fabricated, invented)
	}
	if data.Hotels[0].ID != uuid.Nil {
		t.Error("fabricated hotel identifier survived")
	}
	if len(grounded) != 1 || !data.Restaurants[0].Grounded {
		t.Error("retrieved restaurant was not marked grounded")
	}
}

func TestGroundPromptWithoutPacketIsUnchanged(t *testing.T) {
	const base = "Generate an itinerary for Lisbon."

	if got := groundPrompt(base, nil); got != base {
		t.Errorf("groundPrompt with no packet altered the prompt:\n%q", got)
	}
}

func TestGroundPromptCarriesEvidenceAndIDInstruction(t *testing.T) {
	id := uuid.New()
	const base = "Generate an itinerary for Lisbon."

	got := groundPrompt(base, testPacket(id))

	if !strings.HasPrefix(got, base) {
		t.Error("grounding replaced the original prompt instead of extending it")
	}
	if !strings.Contains(got, "[poi:"+id.String()+"]") {
		t.Error("grounded prompt omits the retrieved identifier")
	}
	if !strings.Contains(got, `set its "id" field`) {
		t.Error("grounded prompt omits the structured-output instruction")
	}
}

// An empty packet must still change the prompt: the model is told retrieval
// found nothing, rather than being left free to fill the gap silently.
func TestGroundPromptWithEmptyPacketStatesTheAbsence(t *testing.T) {
	got := groundPrompt("Generate an itinerary for Lisbon.", &retrieval.ContextPacket{})

	if !strings.Contains(got, "none found") {
		t.Errorf("empty packet did not produce an explicit absence instruction:\n%q", got)
	}
}

func TestPacketIDForSeparatesGroundedAndUngroundedCacheEntries(t *testing.T) {
	if got := packetIDFor(&common.ChatContext{}); got != "ungrounded" {
		t.Errorf("packetIDFor(no packet) = %q, want %q", got, "ungrounded")
	}
	cc := &common.ChatContext{Packet: &retrieval.ContextPacket{PacketID: "pkt_abc"}}
	if got := packetIDFor(cc); got != "pkt_abc" {
		t.Errorf("packetIDFor(packet) = %q, want %q", got, "pkt_abc")
	}
}

func TestVerifyAndRecordGroundingWithoutPacketIsInert(t *testing.T) {
	l := &ServiceImpl{}
	cc := &common.ChatContext{}
	data := &locitypes.AiCityResponse{
		PointsOfInterest: []locitypes.POIDetailedInfo{{ID: uuid.New(), Name: "Somewhere"}},
	}
	originalID := data.PointsOfInterest[0].ID

	v := l.verifyAndRecordGrounding(cc, data, map[string]string{"itinerary": "text"})

	if len(v.Grounded) != 0 || len(v.Unknown) != 0 || len(v.Unused) != 0 {
		t.Errorf("expected empty verification without a packet, got %+v", v)
	}
	// Without a packet there is nothing to verify against, so ids must be left
	// exactly as they were rather than stripped as unverifiable.
	if data.PointsOfInterest[0].ID != originalID {
		t.Error("ungrounded turn altered a POI identifier")
	}
}
