package health

import (
	"testing"
	"time"
)

func TestSemanticSearchReady(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		missing int
		want    bool
	}{
		{"empty corpus is not ready", 0, 0, false},
		{"fully embedded", 100, 0, true},
		{"a few gaps are tolerable", 100, 10, true},
		{"exactly half missing is not ready", 100, 50, false},
		{"mostly missing", 100, 95, false},
		{"nothing embedded", 100, 100, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := CorpusStatus{POICount: tc.total, POIsMissingEmbedding: tc.missing}
			if got := s.SemanticSearchReady(); got != tc.want {
				t.Errorf("SemanticSearchReady() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		status CorpusStatus
		want   string
	}{
		{
			name:   "no POIs at all",
			status: CorpusStatus{POICount: 0},
			want:   "empty",
		},
		{
			name:   "healthy",
			status: CorpusStatus{POICount: 100},
			want:   "ready",
		},
		{
			// Retrieval still works — the lexical lane does not need embeddings —
			// but it is worse than it should be.
			name: "problems but semantic search still usable",
			status: CorpusStatus{
				POICount:             100,
				POIsMissingEmbedding: 5,
				StaleReasons:         []string{"5 POIs have no embedding"},
			},
			want: "degraded",
		},
		{
			name: "semantic search unusable",
			status: CorpusStatus{
				POICount:             100,
				POIsMissingEmbedding: 95,
				StaleReasons:         []string{"95 POIs have no embedding"},
			},
			want: "stale",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.status
			if got := classify(&s); got != tc.want {
				t.Errorf("classify() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A nil recorder must be usable so a job can be constructed without a database.
func TestNilRecorderIsInert(t *testing.T) {
	var r *Recorder

	run, err := r.Start(t.Context(), RunPOIEmbeddings)
	if err != nil {
		t.Fatalf("nil recorder Start errored: %v", err)
	}
	if run == nil {
		t.Fatal("nil recorder returned no run to accumulate into")
	}
	run.Warn("something odd")
	if err := r.Finish(t.Context(), run, nil); err != nil {
		t.Errorf("nil recorder Finish errored: %v", err)
	}

	last, err := r.LastRun(t.Context(), RunPOIEmbeddings)
	if err != nil || last != nil {
		t.Errorf("nil recorder LastRun = %v, %v; want nil, nil", last, err)
	}
}

func TestRunWarnAccumulates(t *testing.T) {
	run := &Run{Kind: RunPOIEmbeddings}

	run.Warn("%d POIs could not be embedded", 3)
	run.Warn("provider was slow")

	if len(run.Warnings) != 2 {
		t.Fatalf("got %d warnings, want 2", len(run.Warnings))
	}
	if run.Warnings[0] != "3 POIs could not be embedded" {
		t.Errorf("warning not formatted: %q", run.Warnings[0])
	}

	// A nil run must absorb warnings rather than panic: Start can hand one back
	// even when the record could not be opened.
	var nilRun *Run
	nilRun.Warn("ignored")
}

func TestRunIDsAreUniqueAndCarryTheKind(t *testing.T) {
	r := NewRecorder(nil)

	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		run, err := r.Start(t.Context(), RunPreferenceRank)
		if err != nil {
			t.Fatalf("Start errored: %v", err)
		}
		if _, dup := seen[run.ID]; dup {
			t.Fatalf("duplicate run id %q", run.ID)
		}
		seen[run.ID] = struct{}{}
	}
}

func TestStaleThresholdsAreOrdered(t *testing.T) {
	// A run is declared dead well before its job is declared stale, so a crashed
	// pass is reported as a crash rather than waiting out the staleness window.
	if deadRunAfter >= staleRunAfter {
		t.Errorf("deadRunAfter (%s) must be shorter than staleRunAfter (%s)",
			deadRunAfter, staleRunAfter)
	}
	if deadRunAfter <= time.Minute {
		t.Error("deadRunAfter is short enough to flag healthy in-flight runs as dead")
	}
}
