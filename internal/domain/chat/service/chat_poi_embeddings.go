package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// maxIngestEmbeddings caps how many places one turn will embed.
//
// A turn discovers a dozen or two places. The cap is a guard against a
// pathological answer, not a throughput knob: anything beyond it is left to the
// nightly backfill, which exists precisely to catch what this misses.
const maxIngestEmbeddings = 30

// ingestEmbeddingTimeout bounds the whole embedding pass.
//
// It runs detached from the turn, so nothing is waiting on it, but it must not
// outlive its usefulness or pile up if the provider is slow.
const ingestEmbeddingTimeout = 2 * time.Minute

// poiEmbeddingRepository is the slice of the POI repository this file needs.
type poiEmbeddingRepository interface {
	POIIDsMissingEmbeddings(ctx context.Context, poiIDs []uuid.UUID) ([]uuid.UUID, error)
	GetPOIByID(ctx context.Context, poiID uuid.UUID) (*locitypes.POIDetailedInfo, error)
	UpdatePOIEmbedding(ctx context.Context, poiID uuid.UUID, embedding []float32) error
}

// embedNewPOIs gives freshly stored places their vectors, now rather than at
// 03:15 tomorrow.
//
// Retrieval has two lanes. The lexical one matches a place by name, so it finds
// nothing for "four days in Madeira" — the words a traveller uses are not the
// words on a POI. The semantic lane is the one that answers a request phrased
// like a request, and it can only search places that have an embedding.
//
// Until now those were written solely by the nightly preference-rerank job. So
// the first person to ask about a city got an ungrounded answer: its POIs were
// discovered during their own turn and embedded hours later. Worse since the
// generation cache landed — that ungrounded answer is stored and replayed to
// everyone who asks the same thing, so the city stays ungrounded until its
// cache entries expire.
//
// This closes the window to a single turn: ask about Rome twice and the second
// answer is grounded.
//
// Deliberately best-effort and detached. An embedding is an improvement to the
// next turn, never a condition of this one, so every failure is logged and
// swallowed. The nightly job remains the safety net.
func (l *ServiceImpl) embedNewPOIs(pois []locitypes.POIDetailedInfo) {
	if l.poiRepo == nil || l.embeddingService == nil || len(pois) == 0 {
		return
	}

	ids := make([]uuid.UUID, 0, len(pois))
	seen := make(map[uuid.UUID]struct{}, len(pois))
	for _, poi := range pois {
		if poi.ID == uuid.Nil {
			continue
		}
		if _, dup := seen[poi.ID]; dup {
			continue
		}
		seen[poi.ID] = struct{}{}
		ids = append(ids, poi.ID)
	}
	if len(ids) == 0 {
		return
	}

	go func() {
		// Its own context: the request's is cancelled the moment the answer is
		// delivered, and this work outlives the answer on purpose.
		ctx, cancel := context.WithTimeout(context.Background(), ingestEmbeddingTimeout)
		defer cancel()

		embedPOIsWithDependencies(ctx, ids, l.poiRepo, l.embeddingService, l.logger)
	}()
}

// embeddingGenerator is the one method of the embedding client this needs.
type embeddingGenerator interface {
	GeneratePOIEmbedding(ctx context.Context, name, description, category string) ([]float32, error)
}

func embedPOIsWithDependencies(
	ctx context.Context,
	ids []uuid.UUID,
	repo poiEmbeddingRepository,
	client embeddingGenerator,
	logger *slog.Logger,
) {
	if logger == nil {
		logger = slog.Default()
	}

	// Ask the database which of these actually need one. A turn mostly
	// recommends places we already know, and re-embedding them would spend a
	// provider call to write the vector that is already there.
	missing, err := repo.POIIDsMissingEmbeddings(ctx, ids)
	if err != nil {
		logger.WarnContext(ctx, "could not check which POIs need embeddings",
			slog.Any("error", err))
		return
	}
	if len(missing) == 0 {
		return
	}
	if len(missing) > maxIngestEmbeddings {
		logger.InfoContext(ctx, "leaving some POIs to the nightly embedding backfill",
			slog.Int("deferred", len(missing)-maxIngestEmbeddings))
		missing = missing[:maxIngestEmbeddings]
	}

	var embedded, failed int
	for _, id := range missing {
		if ctx.Err() != nil {
			break
		}

		poi, err := repo.GetPOIByID(ctx, id)
		if err != nil || poi == nil {
			failed++
			continue
		}

		description := poi.DescriptionPOI
		if description == "" {
			description = poi.Description
		}

		embedding, err := client.GeneratePOIEmbedding(ctx, poi.Name, description, poi.Category)
		if err != nil {
			failed++
			logger.WarnContext(ctx, "could not embed a new POI",
				slog.String("poi_id", id.String()), slog.Any("error", err))
			continue
		}
		if err := repo.UpdatePOIEmbedding(ctx, id, embedding); err != nil {
			failed++
			logger.WarnContext(ctx, "could not store a new POI's embedding",
				slog.String("poi_id", id.String()), slog.Any("error", err))
			continue
		}
		embedded++
	}

	logger.InfoContext(ctx, "embedded newly discovered POIs",
		slog.Int("embedded", embedded), slog.Int("failed", failed))
}
