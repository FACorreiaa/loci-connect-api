package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type rerankVectorReader struct{ found bool }

func (r rerankVectorReader) GetEmbedding(context.Context, uuid.UUID) ([]float32, bool, error) {
	return []float32{1, 0}, r.found, nil
}

type rerankRepo struct {
	missing []uuid.UUID
	ranked  []uuid.UUID
	updated []uuid.UUID
}

func (r *rerankRepo) POIIDsMissingEmbeddings(context.Context, []uuid.UUID) ([]uuid.UUID, error) {
	return r.missing, nil
}

func (r *rerankRepo) UpdatePOIEmbedding(_ context.Context, id uuid.UUID, _ []float32) error {
	r.updated = append(r.updated, id)
	return nil
}

func (r *rerankRepo) RankPOIsByPreference(context.Context, uuid.UUID, []uuid.UUID) ([]uuid.UUID, error) {
	return r.ranked, nil
}

type rerankEmbedder struct{ calls int }

func (e *rerankEmbedder) BatchGenerateEmbeddings(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	result := make([][]float32, len(texts))
	for index := range result {
		result[index] = []float32{0, 1}
	}
	return result, nil
}

func userIDForVariant(t *testing.T, variant string) uuid.UUID {
	t.Helper()
	for index := 0; index < 1000; index++ {
		id := uuid.NewSHA1(uuid.Nil, []byte(fmt.Sprintf("rerank-user-%d", index)))
		if preference.ExperimentVariant(id) == variant {
			return id
		}
	}
	t.Fatalf("no deterministic user found for variant %q", variant)
	return uuid.Nil
}

func TestRerankPOIsWithDependencies(t *testing.T) {
	t.Parallel()
	poiA := uuid.New()
	poiB := uuid.New()
	poiC := uuid.New()
	candidates := []locitypes.POIDetailedInfo{
		{ID: poiA, Name: "Museum"},
		{ID: poiB, Name: "Market"},
		{ID: poiC, Name: "Garden"},
	}

	tests := []struct {
		name       string
		variant    string
		found      bool
		wantOrder  []uuid.UUID
		wantEmbeds int
	}{
		{name: "treatment uses learned pgvector order", variant: "personalized", found: true, wantOrder: []uuid.UUID{poiC, poiA, poiB}, wantEmbeds: 1},
		{name: "control preserves candidate order", variant: "control", found: true, wantOrder: []uuid.UUID{poiA, poiB, poiC}},
		{name: "cold start preserves candidate order", variant: "personalized", wantOrder: []uuid.UUID{poiA, poiB, poiC}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := &rerankRepo{missing: []uuid.UUID{poiB}, ranked: []uuid.UUID{poiC, poiA, poiB}}
			embedder := &rerankEmbedder{}
			got := rerankPOIsWithDependencies(
				context.Background(), userIDForVariant(t, tt.variant), candidates,
				rerankVectorReader{found: tt.found}, repo, embedder, nil,
			)
			require.Len(t, got, len(tt.wantOrder))
			for index, wantID := range tt.wantOrder {
				assert.Equal(t, wantID, got[index].ID)
			}
			assert.Equal(t, tt.wantEmbeds, embedder.calls)
		})
	}
}
