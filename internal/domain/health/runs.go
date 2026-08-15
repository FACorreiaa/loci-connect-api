// Package health reports on the state of Loci's derived data: how much of the
// POI corpus is usable for semantic search, how stale the crowd-verified facts
// are, and whether the jobs that maintain any of it are still running.
//
// The question it answers is the one nobody could answer before: "should I
// trust what this system just told me?" An agent asks it before spending a turn;
// an operator asks it before believing a dashboard.
package health

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunKind identifies a background job. These match the CHECK constraint on
// enrichment_runs.kind.
type RunKind string

const (
	RunPOIEmbeddings  RunKind = "poi_embeddings"
	RunCityEmbeddings RunKind = "city_embeddings"
	RunPreferenceRank RunKind = "preference_rerank"
)

// Recorder writes run records. A job that cannot record must still run: losing
// the audit row is worse than nothing, but far better than skipping the work.
type Recorder struct {
	db *pgxpool.Pool
}

var (
	runIDMu   sync.Mutex
	lastTick  int64
	runIDRand = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// newRunID returns a unique, time-sortable run identifier.
//
// run_id is a primary key, and a bare nanosecond timestamp is not unique: some
// platforms return the same wall-clock value for adjacent calls, so two runs
// started in quick succession collide and the second insert fails. A
// process-monotonic tick guarantees ordering within a process, and the random
// suffix covers two processes ticking at the same instant.
func newRunID(kind RunKind) string {
	runIDMu.Lock()
	tick := time.Now().UTC().UnixNano()
	if tick <= lastTick {
		tick = lastTick + 1
	}
	lastTick = tick
	runIDMu.Unlock()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err == nil {
		for i, b := range suffix {
			suffix[i] = runIDRand[int(b)%len(runIDRand)]
		}
	}
	return fmt.Sprintf("run_%020d_%s_%s", tick, kind, suffix)
}

// NewRecorder wires a Recorder. A nil pool yields a Recorder whose methods are
// no-ops, so a job can be constructed without one in tests.
func NewRecorder(db *pgxpool.Pool) *Recorder {
	return &Recorder{db: db}
}

// Run accumulates the outcome of one pass.
type Run struct {
	ID           string
	Kind         RunKind
	StartedAt    time.Time
	ItemsSeen    int
	ItemsUpdated int
	ItemsFailed  int
	Warnings     []string
}

// Start opens a run record and returns it for the job to fill in.
//
// The row is written immediately with no completed_at, so a job that dies
// mid-pass leaves evidence. That is the state this table exists to expose: a
// crashed job and a job with nothing to do used to look identical.
func (r *Recorder) Start(ctx context.Context, kind RunKind) (*Run, error) {
	run := &Run{
		ID:        newRunID(kind),
		Kind:      kind,
		StartedAt: time.Now().UTC(),
	}
	if r == nil || r.db == nil {
		return run, nil
	}

	_, err := r.db.Exec(ctx, `
		INSERT INTO enrichment_runs (run_id, kind, started_at)
		VALUES ($1, $2, $3)`, run.ID, string(kind), run.StartedAt)
	if err != nil {
		return run, fmt.Errorf("open enrichment run: %w", err)
	}
	return run, nil
}

// Warn attaches a non-fatal problem to the run.
func (run *Run) Warn(format string, args ...any) {
	if run == nil {
		return
	}
	run.Warnings = append(run.Warnings, fmt.Sprintf(format, args...))
}

// Finish closes a run record. Pass the error the job ended with, or nil.
//
// A run is successful only when it completed and returned no error; a run that
// processed nothing successfully is still a success, because "nothing to do" is
// a legitimate outcome and must be distinguishable from "died".
func (r *Recorder) Finish(ctx context.Context, run *Run, runErr error) error {
	if r == nil || r.db == nil || run == nil {
		return nil
	}

	warnings, err := json.Marshal(run.Warnings)
	if err != nil {
		warnings = []byte(`[]`)
	}
	if len(run.Warnings) == 0 {
		warnings = []byte(`[]`)
	}

	var errSummary *string
	success := runErr == nil
	if runErr != nil {
		s := runErr.Error()
		errSummary = &s
	}

	_, err = r.db.Exec(ctx, `
		UPDATE enrichment_runs
		SET completed_at = NOW(),
		    items_seen = $2,
		    items_updated = $3,
		    items_failed = $4,
		    warnings = $5,
		    success = $6,
		    error_summary = $7
		WHERE run_id = $1`,
		run.ID, run.ItemsSeen, run.ItemsUpdated, run.ItemsFailed,
		warnings, success, errSummary)
	if err != nil {
		return fmt.Errorf("close enrichment run: %w", err)
	}
	return nil
}

// RunRecord is a completed or in-flight run, read back for reporting.
type RunRecord struct {
	ID           string     `json:"run_id"`
	Kind         string     `json:"kind"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	ItemsSeen    int        `json:"items_seen"`
	ItemsUpdated int        `json:"items_updated"`
	ItemsFailed  int        `json:"items_failed"`
	Warnings     []string   `json:"warnings,omitempty"`
	Success      bool       `json:"success"`
	ErrorSummary *string    `json:"error_summary,omitempty"`
}

// LastRun returns the most recent completed run of a kind, or nil when the job
// has never finished a pass.
func (r *Recorder) LastRun(ctx context.Context, kind RunKind) (*RunRecord, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}

	var rec RunRecord
	var warnings []byte
	err := r.db.QueryRow(ctx, `
		SELECT run_id, kind, started_at, completed_at, items_seen, items_updated,
		       items_failed, warnings, success, error_summary
		FROM enrichment_runs
		WHERE kind = $1 AND completed_at IS NOT NULL
		ORDER BY completed_at DESC
		LIMIT 1`, string(kind)).
		Scan(&rec.ID, &rec.Kind, &rec.StartedAt, &rec.CompletedAt, &rec.ItemsSeen,
			&rec.ItemsUpdated, &rec.ItemsFailed, &warnings, &rec.Success, &rec.ErrorSummary)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read last enrichment run: %w", err)
	}
	_ = json.Unmarshal(warnings, &rec.Warnings)
	return &rec, nil
}
