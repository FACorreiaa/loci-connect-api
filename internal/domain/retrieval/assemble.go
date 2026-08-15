package retrieval

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Assembler builds ContextPackets. It owns no candidate-selection logic of its
// own: callers pass the candidates their existing search already produced, and
// the assembler enriches them with the knowledge Loci had all along but never
// put in front of a generator — crowd-verified facts, the user's visit history,
// and their learned taste labels.
type Assembler struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

// NewAssembler wires an Assembler. A nil logger is replaced with the default.
func NewAssembler(db *pgxpool.Pool, logger *slog.Logger) *Assembler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Assembler{db: db, logger: logger.With(slog.String("component", "retrieval"))}
}

// Request describes one assembly.
type Request struct {
	UserID   uuid.UUID
	Query    string
	CityID   uuid.UUID
	CityName string

	// Candidates come from whatever search the caller ran. Order is treated as
	// rank order; duplicates and zero-ID entries are dropped.
	Candidates []locitypes.POIDetailedInfo

	// Reasons optionally records why each candidate matched. Candidates without
	// an entry default to MatchSemantic, which is what the existing retrieval
	// paths actually do.
	Reasons map[uuid.UUID]MatchReason

	// DistancesKm carries measured distances for candidates that have one.
	// Supplied separately rather than read off the candidate, because
	// POIDetailedInfo.Distance means different things on different code paths;
	// see Evidence.DistanceKm.
	DistancesKm map[uuid.UUID]float64

	// Limit bounds the packet. Zero means DefaultEvidence; anything above
	// MaxEvidence is clamped.
	Limit int
}

// Assemble builds the packet for req.
//
// Enrichment is best-effort by design: a failure to read facts, visit history,
// or taste labels degrades the packet but never fails the user's request. The
// candidates themselves are the contract; the rest is context. Every degraded
// path is logged at warn with the reason.
func (a *Assembler) Assemble(ctx context.Context, req Request) (*ContextPacket, error) {
	if len(req.Query) > MaxQueryChars {
		return nil, fmt.Errorf("query must be at most %d characters; got %d", MaxQueryChars, len(req.Query))
	}

	limit := ClampLimit(req.Limit, DefaultEvidence, MaxEvidence)

	evidence := make([]Evidence, 0, limit)
	seen := make(map[uuid.UUID]struct{}, limit)
	for _, poi := range req.Candidates {
		if poi.ID == uuid.Nil {
			// A candidate with no stable identifier cannot be cited, so it
			// cannot be evidence.
			continue
		}
		if _, dup := seen[poi.ID]; dup {
			continue
		}
		seen[poi.ID] = struct{}{}

		reason := MatchSemantic
		if r, ok := req.Reasons[poi.ID]; ok && r != "" {
			reason = r
		}

		description := poi.DescriptionPOI
		if description == "" {
			description = poi.Description
		}

		evidence = append(evidence, Evidence{
			POIID:       poi.ID,
			Name:        poi.Name,
			Category:    poi.Category,
			Description: description,
			Latitude:    poi.Latitude,
			Longitude:   poi.Longitude,
			DistanceKm:  req.DistancesKm[poi.ID],
			Address:     poi.Address,
			Source:      poi.Source,
			MatchReason: reason,
			Rank:        len(evidence),
		})
		if len(evidence) >= limit {
			break
		}
	}

	packet := &ContextPacket{
		Query:       req.Query,
		CityID:      req.CityID,
		CityName:    req.CityName,
		Evidence:    evidence,
		AssembledAt: time.Now().UTC(),
	}

	if len(evidence) > 0 {
		ids := packet.IDs()
		a.attachFacts(ctx, packet, ids)
		a.markVisited(ctx, packet, req.UserID, ids)
	}
	packet.TraitLabels = a.traitLabels(ctx, req.UserID)

	sortEvidence(packet.Evidence)
	for i := range packet.Evidence {
		packet.Evidence[i].Rank = i
	}
	packet.PacketID = computePacketID(req.UserID, req.Query, req.CityID, packet.IDs())

	return packet, nil
}

// attachFacts loads unexpired crowd-verified facts for the packet's places.
//
// place_facts has been populated by the verification flow since migration 0062
// and read by nothing outside placeintel's own handler. This is where it starts
// reaching the generator. Expiry is enforced in SQL — the TTL is field-dependent
// and already encoded in expires_at at write time.
func (a *Assembler) attachFacts(ctx context.Context, packet *ContextPacket, ids []uuid.UUID) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, id.String())
	}

	rows, err := a.db.Query(ctx, `
		SELECT poi_id, field, value, confidence, contributor_count, verified_at
		FROM place_facts
		WHERE poi_id = ANY($1) AND expires_at > NOW()
		ORDER BY poi_id, confidence DESC, field`, keys)
	if err != nil {
		a.logger.WarnContext(ctx, "packet assembled without place facts",
			slog.Any("error", err), slog.String("packet_query", packet.Query))
		return
	}
	defer rows.Close()

	byPOI := make(map[string][]Fact, len(ids))
	for rows.Next() {
		var poiID string
		var f Fact
		if err := rows.Scan(&poiID, &f.Field, &f.Value, &f.Confidence, &f.ContributorCount, &f.VerifiedAt); err != nil {
			a.logger.WarnContext(ctx, "skipping unreadable place fact", slog.Any("error", err))
			continue
		}
		if len(byPOI[poiID]) >= MaxFactsPerEvidence {
			continue
		}
		byPOI[poiID] = append(byPOI[poiID], f)
	}
	if err := rows.Err(); err != nil {
		a.logger.WarnContext(ctx, "place fact iteration ended early", slog.Any("error", err))
	}

	for i := range packet.Evidence {
		facts, ok := byPOI[packet.Evidence[i].POIID.String()]
		if !ok {
			continue
		}
		packet.Evidence[i].Facts = facts
		latest := facts[0].VerifiedAt
		for _, f := range facts[1:] {
			if f.VerifiedAt.After(latest) {
				latest = f.VerifiedAt
			}
		}
		verified := latest
		packet.Evidence[i].LastVerifiedAt = &verified
	}
}

// markVisited flags places the user has already been to, so the prompt can stop
// recommending them as discoveries. user_visited_pois has carried this since
// migration 0070 and has only ever fed the globe UI.
func (a *Assembler) markVisited(ctx context.Context, packet *ContextPacket, userID uuid.UUID, ids []uuid.UUID) {
	if userID == uuid.Nil {
		return
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, id.String())
	}

	rows, err := a.db.Query(ctx, `
		SELECT DISTINCT poi_id
		FROM user_visited_pois
		WHERE user_id = $1 AND poi_id = ANY($2)`, userID, keys)
	if err != nil {
		a.logger.WarnContext(ctx, "packet assembled without visit history", slog.Any("error", err))
		return
	}
	defer rows.Close()

	visited := make(map[string]struct{})
	for rows.Next() {
		var poiID string
		if err := rows.Scan(&poiID); err != nil {
			continue
		}
		visited[poiID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		a.logger.WarnContext(ctx, "visit history iteration ended early", slog.Any("error", err))
	}

	for i := range packet.Evidence {
		if _, ok := visited[packet.Evidence[i].POIID.String()]; ok {
			packet.Evidence[i].Visited = true
		}
	}
}

// traitLabels returns the user's confident taste-trait labels.
//
// Gated on personalization_settings exactly like preference.GetEmbedding: a user
// who turned personalization off gets an empty slice, and the absence of a
// settings row is treated as enabled, matching the column default.
func (a *Assembler) traitLabels(ctx context.Context, userID uuid.UUID) []string {
	if userID == uuid.Nil {
		return nil
	}

	rows, err := a.db.Query(ctx, `
		SELECT t.label
		FROM user_taste_traits t
		LEFT JOIN personalization_settings s ON s.user_id = t.user_id
		WHERE t.user_id = $1
		  AND COALESCE(s.personalization_enabled, TRUE)
		  AND t.score > 0
		ORDER BY t.confidence DESC, t.evidence_count DESC, t.trait_key
		LIMIT $2`, userID, MaxTraitLabels)
	if err != nil {
		a.logger.WarnContext(ctx, "packet assembled without taste labels", slog.Any("error", err))
		return nil
	}
	defer rows.Close()

	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			continue
		}
		if label != "" {
			labels = append(labels, label)
		}
	}
	if err := rows.Err(); err != nil {
		a.logger.WarnContext(ctx, "taste label iteration ended early", slog.Any("error", err))
	}
	return labels
}
