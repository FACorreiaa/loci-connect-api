package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// defaultSemanticWeight is the semantic/spatial blend used when retrieving
// evidence, matching the weight ContinueSessionStreamed already uses.
const defaultSemanticWeight = 0.6

// resolveCityID looks up the city for this turn, exact match first and trigram
// fuzzy match second — the same two-step used by ContinueSessionStreamed.
// Returns uuid.Nil when the city is unknown, which is not an error: the turn
// proceeds ungrounded.
func (l *ServiceImpl) resolveCityID(ctx context.Context, cityName string) uuid.UUID {
	if cityName == "" || l.cityRepo == nil {
		return uuid.Nil
	}
	cityData, err := l.cityRepo.FindCityByNameAndCountry(ctx, cityName, "")
	if err != nil || cityData == nil {
		cityData, err = l.cityRepo.FindCityByFuzzyName(ctx, cityName)
		if err != nil || cityData == nil {
			l.logger.InfoContext(ctx, "city not resolved; turn will generate ungrounded",
				slog.String("city_name", cityName))
			return uuid.Nil
		}
	}
	return cityData.ID
}

// assembleEvidencePacket retrieves real POI rows for this turn and builds the
// packet the prompt will be grounded in.
//
// Best-effort throughout. Retrieval failures degrade the turn to the previous
// ungrounded behaviour rather than failing the user's request — but every
// degradation is logged and counted, because a silent fallback to hallucination
// is exactly the failure this phase exists to expose.
func (l *ServiceImpl) assembleEvidencePacket(cc *common.ChatContext) {
	if l.assembler == nil {
		return
	}
	ctx := cc.Ctx

	cc.CityID = l.resolveCityID(ctx, cc.CityName)
	if cc.CityID == uuid.Nil {
		observability.RecordUngroundedTurn("city_unresolved")
		return
	}

	candidates, reasons, err := l.retrieveCandidates(ctx, cc)
	if err != nil {
		l.logger.WarnContext(ctx, "retrieval failed; turn will generate ungrounded",
			slog.Any("error", err), slog.String("city_id", cc.CityID.String()))
		observability.RecordUngroundedTurn("retrieval_failed")
		return
	}

	packet, err := l.assembler.Assemble(ctx, retrieval.Request{
		UserID:     cc.UserID,
		Query:      cc.Message,
		CityID:     cc.CityID,
		CityName:   cc.CityName,
		Candidates: candidates,
		Reasons:    reasons,
	})
	if err != nil {
		l.logger.WarnContext(ctx, "packet assembly failed; turn will generate ungrounded",
			slog.Any("error", err))
		observability.RecordUngroundedTurn("assembly_failed")
		return
	}

	if packet.IsEmpty() {
		// A resolved city with no candidates is worth knowing about: it means
		// the corpus is thin for that city, not that retrieval is broken.
		observability.RecordUngroundedTurn("no_candidates")
	}

	cc.Packet = packet
	l.logger.InfoContext(ctx, "evidence packet assembled",
		slog.String("packet_id", packet.PacketID),
		slog.Int("evidence_count", len(packet.Evidence)),
		slog.String("city_id", cc.CityID.String()))
}

// retrieveCandidates runs both retrieval lanes and fuses them.
//
// Lexical first, and unconditionally: it needs no embedding, no provider call
// and no quota, so it is the lane that still works when the AI provider is down
// or the POI has no embedding yet. It is also the only lane that reliably finds
// a place by its actual name — a rare proper noun is a weak signal in a
// 768-dimension space and a decisive one in an inverted index.
//
// Semantic second, for the intent a keyword cannot express ("somewhere quiet
// for a first date"). Its failure is tolerated: a lexical-only result set is
// worth far more than no evidence at all.
//
// The two are fused by rank rather than by score, because their scores are not
// comparable quantities. See retrieval.FuseRRF.
func (l *ServiceImpl) retrieveCandidates(
	ctx context.Context,
	cc *common.ChatContext,
) ([]locitypes.POIDetailedInfo, map[uuid.UUID]retrieval.MatchReason, error) {
	byID := make(map[uuid.UUID]locitypes.POIDetailedInfo)

	var lanes []retrieval.Ranked

	lexical, err := l.poiRepo.SearchPOIsLexical(ctx, cc.CityID, cc.Message, retrieval.MaxSearchResults)
	if err != nil {
		// Not fatal: the semantic lane may still carry the turn.
		l.logger.WarnContext(ctx, "lexical retrieval lane failed",
			slog.Any("error", err), slog.String("city_id", cc.CityID.String()))
	} else if len(lexical) > 0 {
		ids := make([]uuid.UUID, 0, len(lexical))
		for _, hit := range lexical {
			byID[hit.POI.ID] = hit.POI
			ids = append(ids, hit.POI.ID)
		}
		lanes = append(lanes, retrieval.Ranked{Reason: retrieval.MatchLexical, IDs: ids})
	}

	semantic, err := l.generateSemanticPOIRecommendations(
		ctx, cc.Message, cc.CityID, cc.UserID, cc.UserLocation, defaultSemanticWeight,
	)
	if err != nil {
		l.logger.WarnContext(ctx, "semantic retrieval lane failed",
			slog.Any("error", err), slog.String("city_id", cc.CityID.String()))
	} else if len(semantic) > 0 {
		ids := make([]uuid.UUID, 0, len(semantic))
		for _, poi := range semantic {
			if _, known := byID[poi.ID]; !known {
				byID[poi.ID] = poi
			}
			ids = append(ids, poi.ID)
		}
		lanes = append(lanes, retrieval.Ranked{Reason: retrieval.MatchSemantic, IDs: ids})
	}

	if len(lanes) == 0 {
		return nil, nil, fmt.Errorf("both retrieval lanes failed for city %s", cc.CityID)
	}

	fused := retrieval.FuseRRF(lanes...)
	candidates := make([]locitypes.POIDetailedInfo, 0, len(fused))
	reasons := make(map[uuid.UUID]retrieval.MatchReason, len(fused))
	for _, f := range fused {
		poi, ok := byID[f.POIID]
		if !ok {
			continue
		}
		candidates = append(candidates, poi)
		reasons[f.POIID] = f.Reason
	}

	l.logger.InfoContext(ctx, "candidates retrieved",
		slog.Int("lanes", len(lanes)),
		slog.Int("lexical", len(lexical)),
		slog.Int("semantic", len(semantic)),
		slog.Int("fused", len(candidates)))

	return candidates, reasons, nil
}

// packetIDFor returns the packet identifier for cache-key purposes, or a fixed
// marker when the turn is running without evidence. The marker matters: it keeps
// grounded and ungrounded responses in separate cache entries.
func packetIDFor(cc *common.ChatContext) string {
	if cc.Packet == nil {
		return "ungrounded"
	}
	return cc.Packet.PacketID
}

// groundPrompt appends the evidence block to a prompt that produces places.
//
// Prompts that only describe a city (city_data) are left alone: there is
// nothing to cite in them, and the extra tokens would be waste.
func groundPrompt(prompt string, packet *retrieval.ContextPacket) string {
	if packet == nil {
		return prompt
	}
	return prompt + "\n\n" + retrieval.Render(packet) + `
When a place below appears in your JSON output, set its "id" field to that
place's exact identifier. Leave "id" absent for any place not listed.
`
}

// verifyAndRecordGrounding checks the generated output against the packet,
// neutralises fabricated identifiers, and writes the evidence trail.
//
// The critical side effect is the neutralisation: an identifier the model
// invented is cleared from the parsed POI before canonicalizePOIs sees it.
// Left in place, a fabricated UUID would be persisted as though retrieval had
// produced it — the exact laundering of a guess into a fact this phase prevents.
func (l *ServiceImpl) verifyAndRecordGrounding(
	cc *common.ChatContext,
	data *locitypes.AiCityResponse,
	rawResponses map[string]string,
) retrieval.Verification {
	var verification retrieval.Verification
	if cc.Packet == nil {
		return verification
	}
	ctx := cc.Ctx

	// Citations may appear either as markers in prose or as "id" fields in the
	// JSON. Both are checked: the prose scan catches conversational parts, the
	// struct walk catches the structured ones.
	var combined strings.Builder
	for _, part := range groundedParts {
		if raw, ok := rawResponses[part]; ok {
			combined.WriteString(raw)
			combined.WriteString("\n")
		}
	}
	verification = retrieval.Verify(cc.Packet, combined.String())

	grounded, fabricated := l.neutralizeFabricatedIDs(cc, data)
	verification.Unknown = append(verification.Unknown, fabricated...)
	verification.Grounded = mergeUnique(verification.Grounded, grounded)
	verification.Unused = subtract(cc.Packet.IDs(), verification.Grounded)

	observability.RecordGrounding(len(verification.Grounded), len(verification.Unknown),
		len(cc.Packet.Evidence))

	if len(verification.Unknown) > 0 {
		l.logger.WarnContext(ctx, "model cited identifiers that were never retrieved",
			slog.Int("fabricated_count", len(verification.Unknown)),
			slog.String("packet_id", cc.Packet.PacketID),
			slog.String("session_id", cc.SessionID.String()))
	}

	return verification
}

// neutralizeFabricatedIDs walks every POI list in the response, keeping ids
// that came from the packet and clearing the ones that did not.
func (l *ServiceImpl) neutralizeFabricatedIDs(
	cc *common.ChatContext,
	data *locitypes.AiCityResponse,
) (grounded, fabricated []uuid.UUID) {
	// check decides one identifier's fate in place. Returns whether it was
	// grounded, so the caller can bucket it.
	check := func(id *uuid.UUID, groundedFlag *bool) {
		if *id == uuid.Nil {
			return
		}
		if cc.Packet.Has(*id) {
			*groundedFlag = true
			grounded = append(grounded, *id)
			return
		}
		// Invented. Drop the identifier but keep the suggestion — it may still
		// be a real place, it just is not one we retrieved.
		fabricated = append(fabricated, *id)
		*id = uuid.Nil
		*groundedFlag = false
	}

	poiLists := [][]locitypes.POIDetailedInfo{
		data.PointsOfInterest,
		data.AIItineraryResponse.PointsOfInterest,
		data.AIItineraryResponse.Restaurants,
		data.Activities,
	}
	for _, list := range poiLists {
		for i := range list {
			check(&list[i].ID, &list[i].Grounded)
		}
	}
	for i := range data.Hotels {
		check(&data.Hotels[i].ID, &data.Hotels[i].Grounded)
	}
	for i := range data.Restaurants {
		check(&data.Restaurants[i].ID, &data.Restaurants[i].Grounded)
	}

	return dedupe(grounded), dedupe(fabricated)
}

// recordEvidence persists the trail off the streaming path.
func (l *ServiceImpl) recordEvidence(
	ctx context.Context,
	interactionID string,
	packet *retrieval.ContextPacket,
	v retrieval.Verification,
) {
	if l.assembler == nil || packet == nil || interactionID == "" {
		return
	}
	if err := l.assembler.Record(ctx, interactionID, packet, v); err != nil {
		// Losing the audit trail must never cost the user their answer.
		l.logger.WarnContext(ctx, "failed to record answer evidence",
			slog.Any("error", err), slog.String("packet_id", packet.PacketID))
	}
}

// groundedParts are the response parts that name places and are therefore
// expected to cite. city_data is excluded: it describes a city, not places.
var groundedParts = []string{
	"general_pois", "itinerary", "hotels", "restaurants", "activities",
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func mergeUnique(a, b []uuid.UUID) []uuid.UUID {
	return dedupe(append(append([]uuid.UUID{}, a...), b...))
}

func subtract(all, remove []uuid.UUID) []uuid.UUID {
	if len(all) == 0 {
		return nil
	}
	drop := make(map[uuid.UUID]struct{}, len(remove))
	for _, id := range remove {
		drop[id] = struct{}{}
	}
	out := make([]uuid.UUID, 0, len(all))
	for _, id := range all {
		if _, ok := drop[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}
