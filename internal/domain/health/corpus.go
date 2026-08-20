package health

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// staleRunAfter is how long a job may go without a successful pass before the
// corpus is reported stale. The reranker and embedding backfill are meant to run
// far more often than this; a full day of silence means something is wrong.
const staleRunAfter = 24 * time.Hour

// deadRunAfter is how long an unfinished run may sit before it is treated as
// died rather than in flight.
const deadRunAfter = 2 * time.Hour

// Service reports corpus health.
type Service struct {
	db       *pgxpool.Pool
	recorder *Recorder
}

// NewService wires a health service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db, recorder: NewRecorder(db)}
}

// CorpusStatus is the answer to "should I trust this data right now?"
type CorpusStatus struct {
	// Status is one of ready, degraded, stale, empty. Derived from the reasons
	// below so a caller can branch without interpreting them.
	Status string `json:"status"`

	POICount             int `json:"poi_count"`
	POIsMissingEmbedding int `json:"pois_missing_embedding"`
	CityCount            int `json:"city_count"`

	// FactsTotal counts crowd-verified facts; FactsExpired counts those past
	// their field-dependent TTL. An expired fact is not deleted — it simply
	// stops being offered as evidence.
	FactsTotal   int `json:"facts_total"`
	FactsExpired int `json:"facts_expired"`

	LastRuns map[string]*RunRecord `json:"last_runs"`

	// StaleReasons is empty when the corpus is healthy. Each entry is a plain
	// sentence an operator can act on, not a code to look up.
	StaleReasons []string `json:"stale_reasons,omitempty"`

	CheckedAt time.Time `json:"checked_at"`
}

// SemanticSearchReady reports whether enough of the corpus carries embeddings
// for vector search to be meaningful. The lexical lane works regardless, which
// is precisely why it exists.
func (s CorpusStatus) SemanticSearchReady() bool {
	if s.POICount == 0 {
		return false
	}
	return float64(s.POIsMissingEmbedding)/float64(s.POICount) < 0.5
}

// Corpus gathers the health picture.
//
// One query per fact rather than a single wide join: these run rarely, the
// counts are independent, and a failure in one should not blank the others.
func (s *Service) Corpus(ctx context.Context) (*CorpusStatus, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("health service is not configured with a database")
	}

	status := &CorpusStatus{
		LastRuns:  make(map[string]*RunRecord),
		CheckedAt: time.Now().UTC(),
	}

	if err := s.db.QueryRow(ctx, `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE embedding IS NULL)::int
		FROM points_of_interest`).Scan(&status.POICount, &status.POIsMissingEmbedding); err != nil {
		return nil, fmt.Errorf("count points of interest: %w", err)
	}

	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM cities`).
		Scan(&status.CityCount); err != nil {
		return nil, fmt.Errorf("count cities: %w", err)
	}

	if err := s.db.QueryRow(ctx, `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE expires_at <= NOW())::int
		FROM place_facts`).Scan(&status.FactsTotal, &status.FactsExpired); err != nil {
		return nil, fmt.Errorf("count place facts: %w", err)
	}

	for _, kind := range []RunKind{RunPOIEmbeddings, RunPreferenceRank} {
		last, err := s.recorder.LastRun(ctx, kind)
		if err != nil {
			return nil, err
		}
		status.LastRuns[string(kind)] = last
	}

	status.StaleReasons = s.staleReasons(ctx, status)
	status.Status = classify(status)
	return status, nil
}

// staleReasons builds the human-readable list of what is wrong.
func (s *Service) staleReasons(ctx context.Context, status *CorpusStatus) []string {
	var reasons []string

	if status.POICount == 0 {
		reasons = append(reasons, "the POI corpus is empty; nothing can be retrieved")
		return reasons
	}

	if status.POIsMissingEmbedding > 0 {
		pct := float64(status.POIsMissingEmbedding) / float64(status.POICount) * 100
		reasons = append(reasons, fmt.Sprintf(
			"%d of %d POIs (%.0f%%) have no embedding and cannot be found by semantic search",
			status.POIsMissingEmbedding, status.POICount, pct,
		))
	}

	if status.FactsExpired > 0 {
		reasons = append(reasons, fmt.Sprintf(
			"%d of %d crowd-verified facts are past their TTL and are no longer offered as evidence",
			status.FactsExpired, status.FactsTotal,
		))
	}

	for kind, last := range status.LastRuns {
		switch {
		case last == nil:
			reasons = append(reasons, fmt.Sprintf("the %s job has never completed a pass", kind))
		case !last.Success:
			summary := "no detail recorded"
			if last.ErrorSummary != nil {
				summary = *last.ErrorSummary
			}
			reasons = append(reasons, fmt.Sprintf("the last %s run failed: %s", kind, summary))
		case last.CompletedAt != nil && time.Since(*last.CompletedAt) > staleRunAfter:
			reasons = append(reasons, fmt.Sprintf(
				"the %s job last succeeded %s ago", kind,
				time.Since(*last.CompletedAt).Round(time.Hour),
			))
		}
	}

	if stuck, err := s.stuckRuns(ctx); err == nil {
		for _, kind := range stuck {
			reasons = append(reasons, fmt.Sprintf(
				"a %s run started over %s ago and never finished; it probably died",
				kind, deadRunAfter,
			))
		}
	}

	return reasons
}

// stuckRuns finds runs that opened a record and never closed it.
func (s *Service) stuckRuns(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT kind
		FROM enrichment_runs
		WHERE completed_at IS NULL AND started_at < NOW() - $1::interval
		ORDER BY kind`, deadRunAfter.String())
	if err != nil {
		return nil, fmt.Errorf("find stuck enrichment runs: %w", err)
	}
	defer rows.Close()

	var kinds []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, fmt.Errorf("scan stuck run: %w", err)
		}
		kinds = append(kinds, kind)
	}
	return kinds, rows.Err()
}

// classify reduces the reasons to a single word.
func classify(status *CorpusStatus) string {
	if status.POICount == 0 {
		return "empty"
	}
	if len(status.StaleReasons) == 0 {
		return "ready"
	}
	// Degraded means retrieval still works but is worse than it should be;
	// stale means a maintenance job is not running at all.
	if !status.SemanticSearchReady() {
		return "stale"
	}
	return "degraded"
}
