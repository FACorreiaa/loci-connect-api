package retrieval

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Record persists the evidence trail for one generation: every place offered in
// the packet, plus every identifier the model cited that was not offered.
//
// Called after a turn completes, off the streaming path. A failure here loses
// audit data, never the user's answer, so callers log and continue.
func (a *Assembler) Record(ctx context.Context, llmInteractionID string, packet *ContextPacket, v Verification) error {
	if llmInteractionID == "" {
		return fmt.Errorf("llm interaction id is required to record evidence")
	}
	if packet == nil {
		return nil
	}

	cited := make(map[uuid.UUID]struct{}, len(v.Grounded))
	for _, id := range v.Grounded {
		cited[id] = struct{}{}
	}

	batch := &pgx.Batch{}
	const stmt = `
		INSERT INTO answer_evidence
			(llm_interaction_id, packet_id, poi_id, rank, cited, grounded, match_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (llm_interaction_id, poi_id) DO UPDATE
			SET cited = answer_evidence.cited OR EXCLUDED.cited`

	for _, e := range packet.Evidence {
		_, wasCited := cited[e.POIID]
		batch.Queue(stmt, llmInteractionID, packet.PacketID, e.POIID.String(),
			e.Rank, wasCited, true, string(e.MatchReason))
	}

	// Fabricated identifiers. rank -1: they held no position in the packet
	// because they were never in it.
	for _, id := range v.Unknown {
		batch.Queue(stmt, llmInteractionID, packet.PacketID, id.String(),
			-1, true, false, "")
	}

	if batch.Len() == 0 {
		return nil
	}

	results := a.db.SendBatch(ctx, batch)
	defer func() {
		if err := results.Close(); err != nil {
			a.logger.WarnContext(ctx, "closing answer evidence batch", slog.Any("error", err))
		}
	}()

	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("insert answer evidence: %w", err)
		}
	}
	return nil
}

// EvidenceForInteraction reads back the trail for one generation, packet order
// first and fabricated identifiers last. This is what answers "where did this
// recommendation come from?" for both the user and an inspecting agent.
func (a *Assembler) EvidenceForInteraction(ctx context.Context, llmInteractionID string) ([]RecordedEvidence, error) {
	rows, err := a.db.Query(ctx, `
		SELECT packet_id, poi_id, rank, cited, grounded, match_reason, created_at
		FROM answer_evidence
		WHERE llm_interaction_id = $1
		ORDER BY grounded DESC, rank`, llmInteractionID)
	if err != nil {
		return nil, fmt.Errorf("read answer evidence: %w", err)
	}
	defer rows.Close()

	var out []RecordedEvidence
	for rows.Next() {
		var r RecordedEvidence
		if err := rows.Scan(&r.PacketID, &r.POIID, &r.Rank, &r.Cited,
			&r.Grounded, &r.MatchReason, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan answer evidence: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate answer evidence: %w", err)
	}
	return out, nil
}
