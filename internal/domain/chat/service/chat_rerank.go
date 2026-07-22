package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type candidatePOIRanker interface {
	POIIDsMissingEmbeddings(context.Context, []uuid.UUID) ([]uuid.UUID, error)
	UpdatePOIEmbedding(context.Context, uuid.UUID, []float32) error
	RankPOIsByPreference(context.Context, uuid.UUID, []uuid.UUID) ([]uuid.UUID, error)
}

type candidateEmbeddingClient interface {
	BatchGenerateEmbeddings(context.Context, []string) ([][]float32, error)
}

// rerankPOIs applies the learned preference vector to an already relevant
// candidate set. The LLM remains candidate generation; Postgres/pgvector owns
// the final treatment ordering.
func (l *ServiceImpl) rerankPOIs(ctx context.Context, userID uuid.UUID, pois []locitypes.POIDetailedInfo) []locitypes.POIDetailedInfo {
	ranker, ok := l.poiRepo.(candidatePOIRanker)
	if !ok {
		return pois
	}
	return rerankPOIsWithDependencies(ctx, userID, pois, l.prefVectors, ranker, l.embeddingService, l.logger)
}

func rerankPOIsWithDependencies(
	ctx context.Context,
	userID uuid.UUID,
	pois []locitypes.POIDetailedInfo,
	vectors preference.VectorReader,
	repo candidatePOIRanker,
	embedder candidateEmbeddingClient,
	logger *slog.Logger,
) []locitypes.POIDetailedInfo {
	if len(pois) < 2 || userID == uuid.Nil || preference.ExperimentVariant(userID) == "control" || vectors == nil || repo == nil {
		return pois
	}
	_, found, err := vectors.GetEmbedding(ctx, userID)
	if err != nil || !found {
		return pois
	}
	if logger == nil {
		logger = slog.Default()
	}

	ids := uniquePOIIDs(pois)
	if len(ids) < 2 {
		return pois
	}
	if embedder != nil {
		ensureCandidateEmbeddings(ctx, pois, ids, repo, embedder, logger)
	}
	ranked, err := repo.RankPOIsByPreference(ctx, userID, ids)
	if err != nil {
		logger.WarnContext(ctx, "failed to rerank Discover candidates", slog.Any("error", err))
		return pois
	}
	return reorderPOIs(pois, ranked)
}

func uniquePOIIDs(pois []locitypes.POIDetailedInfo) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(pois))
	seen := make(map[uuid.UUID]struct{}, len(pois))
	for _, candidate := range pois {
		if candidate.ID == uuid.Nil {
			continue
		}
		if _, exists := seen[candidate.ID]; exists {
			continue
		}
		seen[candidate.ID] = struct{}{}
		ids = append(ids, candidate.ID)
	}
	return ids
}

func ensureCandidateEmbeddings(
	ctx context.Context,
	pois []locitypes.POIDetailedInfo,
	ids []uuid.UUID,
	repo candidatePOIRanker,
	embedder candidateEmbeddingClient,
	logger *slog.Logger,
) {
	missing, err := repo.POIIDsMissingEmbeddings(ctx, ids)
	if err != nil || len(missing) == 0 {
		return
	}
	byID := make(map[uuid.UUID]locitypes.POIDetailedInfo, len(pois))
	for _, candidate := range pois {
		byID[candidate.ID] = candidate
	}
	texts := make([]string, 0, len(missing))
	validIDs := make([]uuid.UUID, 0, len(missing))
	for _, id := range missing {
		candidate, ok := byID[id]
		if !ok {
			continue
		}
		description := candidate.DescriptionPOI
		if description == "" {
			description = candidate.Description
		}
		texts = append(texts, strings.TrimSpace(fmt.Sprintf("%s\n%s\nCategory: %s", candidate.Name, description, candidate.Category)))
		validIDs = append(validIDs, id)
	}
	if len(texts) == 0 {
		return
	}
	embeddings, err := embedder.BatchGenerateEmbeddings(ctx, texts)
	if err != nil || len(embeddings) != len(validIDs) {
		logger.WarnContext(ctx, "failed to embed Discover candidates", slog.Any("error", err))
		return
	}
	for index, embedding := range embeddings {
		if err := repo.UpdatePOIEmbedding(ctx, validIDs[index], embedding); err != nil {
			logger.WarnContext(ctx, "failed to persist Discover candidate embedding",
				slog.String("poi_id", validIDs[index].String()), slog.Any("error", err))
		}
	}
}

func reorderPOIs(pois []locitypes.POIDetailedInfo, ranked []uuid.UUID) []locitypes.POIDetailedInfo {
	if len(ranked) == 0 {
		return pois
	}
	byID := make(map[uuid.UUID][]locitypes.POIDetailedInfo, len(pois))
	for _, candidate := range pois {
		byID[candidate.ID] = append(byID[candidate.ID], candidate)
	}
	result := make([]locitypes.POIDetailedInfo, 0, len(pois))
	for _, id := range ranked {
		result = append(result, byID[id]...)
		delete(byID, id)
	}
	for _, candidate := range pois {
		if _, remains := byID[candidate.ID]; !remains {
			continue
		}
		result = append(result, byID[candidate.ID]...)
		delete(byID, candidate.ID)
	}
	return result
}
