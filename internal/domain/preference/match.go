package preference

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// MinMatchSignals is how much feedback a traveller needs before Loci shows
	// a match. Below it the vector is mostly one or two places, and a confident
	// "92% match" would be a guess dressed up as knowledge.
	MinMatchSignals = 5
	// maxMatchReasons and maxReasonRunes mirror TasteMatch's validation rules.
	maxMatchReasons = 3
	maxReasonRunes  = 120
	// likeSimilarity is how close a place must be to one the traveller liked
	// before Loci names that place as the reason.
	likeSimilarity = 0.80
	// likedSample bounds the places considered for "Similar to …".
	likedSample = 200
)

// Match is how well a place fits a traveller: Score in [0,1] and up to three
// plain-language reasons.
type Match struct {
	Score   float32
	Reasons []string
}

// POIScorer scores places against a traveller's taste. A place it cannot score
// is absent from the result.
type POIScorer interface {
	ScorePOIs(ctx context.Context, userID uuid.UUID, poiIDs []string) (map[string]Match, error)
}

// MatchScorer scores places against the stored preference vector.
type MatchScorer struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

// NewMatchScorer builds a scorer. A nil pool gives a scorer that scores
// nothing.
func NewMatchScorer(db *pgxpool.Pool, logger *slog.Logger) *MatchScorer {
	l := logger
	if l == nil {
		l = slog.Default()
	}
	return &MatchScorer{db: db, logger: l.With(slog.String("component", "taste-match"))}
}

// ScorePOIs returns a match for each place it can score. It scores nothing for
// anonymous travellers, the control holdout, anyone who turned personalization
// off, a vector built from fewer than MinMatchSignals signals, or a vector not
// yet calibrated. Places without an embedding, and ids that are not UUIDs, are
// left out.
func (s *MatchScorer) ScorePOIs(ctx context.Context, userID uuid.UUID, poiIDs []string) (map[string]Match, error) {
	out := map[string]Match{}
	if s == nil || s.db == nil || userID == uuid.Nil || ExperimentVariant(userID) == "control" {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(poiIDs))
	for _, raw := range poiIDs {
		if id, err := uuid.Parse(raw); err == nil && id != uuid.Nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}

	var (
		count    int
		p10, p90 *float64
	)
	err := s.db.QueryRow(ctx, `
		SELECT v.feedback_count, v.match_p10, v.match_p90
		FROM user_preference_vectors v
		LEFT JOIN personalization_settings settings ON settings.user_id = v.user_id
		WHERE v.user_id = $1
		  AND COALESCE(settings.personalization_enabled, TRUE)`, userID).Scan(&count, &p10, &p90)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("load taste calibration: %w", err)
	}
	if count < MinMatchSignals || p10 == nil || p90 == nil {
		return out, nil
	}

	rows, err := s.db.Query(ctx, `
		WITH liked AS (
			SELECT pf.poi_id
			FROM preference_feedback pf
			WHERE pf.user_id = $1 AND pf.poi_id IS NOT NULL AND pf.poi_id <> ''
			GROUP BY pf.poi_id
			HAVING SUM(pf.weight * CASE WHEN pf.event = 'skipped' THEN -1 ELSE 1 END) > 0
			ORDER BY MAX(pf.created_at) DESC
			LIMIT $3
		), liked_pois AS (
			SELECT p.id, p.name, p.embedding
			FROM liked
			JOIN points_of_interest p ON p.id::text = liked.poi_id
			WHERE p.embedding IS NOT NULL
		)
		SELECT
			p.id::text,
			1 - (p.embedding <=> v.embedding),
			t.label, t.score, t.confidence,
			r.verdict,
			near.name
		FROM points_of_interest p
		JOIN user_preference_vectors v ON v.user_id = $1
		LEFT JOIN user_taste_traits t
			ON t.user_id = $1
		   AND t.trait_key = LOWER(COALESCE(NULLIF(p.category, ''), NULLIF(p.poi_type, ''), 'places'))
		LEFT JOIN user_poi_rankings r ON r.user_id = $1 AND r.poi_id = p.id::text
		LEFT JOIN LATERAL (
			SELECT lp.name
			FROM liked_pois lp
			WHERE lp.id <> p.id AND 1 - (lp.embedding <=> p.embedding) >= $4
			ORDER BY lp.embedding <=> p.embedding
			LIMIT 1
		) near ON TRUE
		WHERE p.id = ANY($2::uuid[]) AND p.embedding IS NOT NULL`,
		userID, ids, likedSample, likeSimilarity)
	if err != nil {
		return out, fmt.Errorf("score places: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id                string
			sim               float64
			label             *string
			trait, confidence *float64
			verdict           *int16
			nearName          *string
		)
		if err := rows.Scan(&id, &sim, &label, &trait, &confidence, &verdict, &nearName); err != nil {
			return out, fmt.Errorf("scan taste match: %w", err)
		}
		score, ok := rescale(sim, *p10, *p90)
		if !ok {
			continue
		}
		var reasons []string
		if verdict != nil {
			reasons = appendReason(reasons, verdictReason(*verdict))
		}
		if label != nil && trait != nil && confidence != nil {
			reasons = appendReason(reasons, traitReason(*label, *trait, *confidence))
		}
		if nearName != nil {
			reasons = appendReason(reasons, likeReason(*nearName))
		}
		out[id] = Match{Score: score, Reasons: reasons}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("read taste matches: %w", err)
	}
	return out, nil
}

// rescale places a similarity within the traveller's own p10–p90 band, clamped
// to [0,1]. A band too narrow to tell places apart scores nothing.
func rescale(sim, p10, p90 float64) (float32, bool) {
	span := p90 - p10
	if span < 0.01 {
		return 0, false
	}
	v := (sim - p10) / span
	switch {
	case v < 0:
		v = 0
	case v > 1:
		v = 1
	}
	return float32(v), true
}

// traitReason explains a match through the traveller's trait for this kind of
// place, or says nothing when the trait is weak or unsure.
func traitReason(label string, score, confidence float64) string {
	if confidence < 0.3 || (score < 0.3 && score > -0.3) {
		return ""
	}
	kind := strings.ToLower(strings.TrimSpace(label))
	if score > 0 {
		return "You tend to like " + kind
	}
	return "You've passed on " + kind + " before"
}

func verdictReason(v int16) string {
	switch v {
	case 1:
		return "You rated it Loved"
	case 2:
		return "You rated it Fine"
	case 3:
		return "You rated it Not for me"
	}
	return ""
}

func likeReason(name string) string {
	return truncateReason("Similar to " + strings.TrimSpace(name) + ", one of your picks")
}

func appendReason(reasons []string, r string) []string {
	if r == "" || len(reasons) >= maxMatchReasons {
		return reasons
	}
	return append(reasons, truncateReason(r))
}

func truncateReason(s string) string {
	runes := []rune(s)
	if len(runes) <= maxReasonRunes {
		return s
	}
	return string(runes[:maxReasonRunes-1]) + "…"
}
