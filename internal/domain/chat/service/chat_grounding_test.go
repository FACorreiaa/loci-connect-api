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
	realID := uuid.New()
	invented := uuid.New()

	cc := &common.ChatContext{Packet: testPacket(realID)}
	data := &locitypes.AiCityResponse{
		PointsOfInterest: []locitypes.POIDetailedInfo{
			{ID: realID, Name: "Bar Alta"},
			{ID: invented, Name: "Bar That Does Not Exist"},
			{Name: "Unnamed suggestion with no id"},
		},
	}

	l := &ServiceImpl{}
	grounded, fabricated := l.neutralizeFabricatedIDs(cc, data)

	if len(grounded) != 1 || grounded[0] != realID {
		t.Errorf("grounded = %v, want [%s]", grounded, realID)
	}
	if len(fabricated) != 1 || fabricated[0] != invented {
		t.Errorf("fabricated = %v, want [%s]", fabricated, invented)
	}

	if !data.PointsOfInterest[0].Grounded {
		t.Error("retrieved place was not marked grounded")
	}
	if data.PointsOfInterest[0].ID != realID {
		t.Error("retrieved place lost its identifier")
	}

	if data.PointsOfInterest[1].ID != uuid.Nil {
		t.Errorf("fabricated identifier survived: %s", data.PointsOfInterest[1].ID)
	}
	if data.PointsOfInterest[1].Grounded {
		t.Error("fabricated place was marked grounded")
	}
	// The suggestion itself is kept — it may be a realID place we simply do not
	// have. Only the false claim of provenance is removed.
	if data.PointsOfInterest[1].Name == "" {
		t.Error("fabricated place was dropped entirely; only its id should be")
	}
}

func TestNeutralizeFabricatedIDsCoversHotelsAndRestaurants(t *testing.T) {
	realID := uuid.New()
	invented := uuid.New()

	cc := &common.ChatContext{Packet: testPacket(realID)}
	data := &locitypes.AiCityResponse{
		Hotels:      []locitypes.HotelDetailedInfo{{ID: invented, Name: "Ghost Hotel"}},
		Restaurants: []locitypes.RestaurantDetailedInfo{{ID: realID, Name: "Real Tasca"}},
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

// A cited place takes its identity and its pin from the packet. The model's own
// coordinates are a guess; the packet's came from PostGIS, and a map link built
// on the guess would send someone to the wrong place.
func TestResolvePacketPOIsTakesTheRowsCoordinates(t *testing.T) {
	id := uuid.New()
	packet := testPacket(id)
	packet.Evidence[0].Latitude = 32.6825
	packet.Evidence[0].Longitude = -17.0695
	packet.Evidence[0].Address = "Ribeira Brava, Madeira"

	cc := &common.ChatContext{Packet: packet}
	data := &locitypes.AiCityResponse{
		PointsOfInterest: []locitypes.POIDetailedInfo{{
			Name:      "Ribeira Brava Town Center [poi:" + id.String() + "]",
			Latitude:  1.1, // the model's guess
			Longitude: 2.2,
		}},
	}

	(&ServiceImpl{}).resolvePacketPOIs(cc, data)

	got := data.PointsOfInterest[0]
	if got.Name != "Ribeira Brava Town Center" {
		t.Errorf("the marker survived in the name: %q", got.Name)
	}
	if got.ID != id {
		t.Errorf("the citation did not become the id: got %v, want %v", got.ID, id)
	}
	if !got.Grounded {
		t.Error("a place cited from the packet should be grounded")
	}
	if got.Latitude != 32.6825 || got.Longitude != -17.0695 {
		t.Errorf("kept the model's coordinates: got %v,%v", got.Latitude, got.Longitude)
	}
	if got.Address != "Ribeira Brava, Madeira" {
		t.Errorf("the row's address was not adopted: %q", got.Address)
	}
}

// A place the packet does not know keeps what the model said. It is still shown
// — it may be real — but nothing here claims to know where it is.
func TestResolvePacketPOIsLeavesAnUncitedPlaceAlone(t *testing.T) {
	cc := &common.ChatContext{Packet: testPacket(uuid.New())}
	data := &locitypes.AiCityResponse{
		PointsOfInterest: []locitypes.POIDetailedInfo{
			{Name: "Somewhere The Model Imagined", Latitude: 1.1, Longitude: 2.2},
			{Name: "Cited But Invented [poi:" + uuid.New().String() + "]", Latitude: 3.3},
		},
	}

	(&ServiceImpl{}).resolvePacketPOIs(cc, data)

	if got := data.PointsOfInterest[0]; got.Latitude != 1.1 || got.Grounded {
		t.Errorf("an uncited place was altered: %+v", got)
	}
	second := data.PointsOfInterest[1]
	if strings.Contains(second.Name, "poi:") {
		t.Errorf("an invented citation was left in the name: %q", second.Name)
	}
	if second.Grounded || second.Latitude != 3.3 {
		t.Errorf("an invented citation should ground nothing: %+v", second)
	}
}

// On a full cache hit there is no packet, and the replayed text still carries
// the original turn's markers. Stripping cannot depend on having evidence.
func TestResolvePacketPOIsStripsWithoutAPacket(t *testing.T) {
	cc := &common.ChatContext{} // Packet is nil, as on a cache replay
	data := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{
				{Name: "Monte Palace Tropical Garden [poi:" + uuid.New().String() + "]"},
			},
		},
		Hotels:      []locitypes.HotelDetailedInfo{{Name: "Reid's Palace [poi:" + uuid.New().String() + "]"}},
		Restaurants: []locitypes.RestaurantDetailedInfo{{Name: "Il Gallo d'Oro [poi:" + uuid.New().String() + "]"}},
	}

	(&ServiceImpl{}).resolvePacketPOIs(cc, data)

	for _, name := range []string{
		data.AIItineraryResponse.PointsOfInterest[0].Name,
		data.Hotels[0].Name,
		data.Restaurants[0].Name,
	} {
		if strings.Contains(name, "poi:") {
			t.Errorf("a marker survived a packetless turn: %q", name)
		}
	}
}
