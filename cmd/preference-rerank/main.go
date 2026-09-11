package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/health"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/pkg/ai"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

func main() {
	lookback := flag.Duration("lookback", 7*24*time.Hour, "only users with feedback in this window (0 = all)")
	interval := flag.Duration("interval", 0, "repeat interval (0 = run once)")
	embeddingBatch := flag.Int("embedding-batch", 50, "backfill this many missing POI embeddings before reranking (0 = disabled)")
	imageBatch := flag.Int("image-batch", 50, "look up Wikimedia images for this many POIs that have none (0 = disabled)")
	flag.Parse()

	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := db.New(db.Config{
		DSN:             cfg.Database.DSN(),
		MaxConns:        4,
		MinConns:        1,
		MaxConnLifetime: cfg.Database.MaxConnLifetime,
		MaxConnIdleTime: cfg.Database.MaxConnIdleTime,
	}, logger)
	if err != nil {
		logger.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Run records make these jobs observable. Until now a job that died and a
	// job with nothing to do left identical evidence: none.
	recorder := health.NewRecorder(database.Pool)

	store := preference.NewVectorStore(database.Pool, logger)
	job := preference.NewReranker(database.Pool, store, logger)
	poiRepo := poi.NewRepository(database.Pool, logger)
	var embeddingClient generativeAI.EmbeddingClient
	if *embeddingBatch > 0 {
		embeddingClient, err = ai.NewEmbeddingClient(
			ctx,
			cfg.AI,
			logger,
		)
		if err != nil {
			logger.Error("embedding client initialization failed", "error", err)
			os.Exit(1)
		}
		defer embeddingClient.Close()
	}

	run := func() error {
		if *embeddingBatch > 0 {
			embeddingRun, startErr := recorder.Start(ctx, health.RunPOIEmbeddings)
			if startErr != nil {
				logger.Warn("could not open embedding run record", "error", startErr)
			}
			processed, failed, backfillErr := backfillPOIEmbeddings(
				ctx, database.Pool, poiRepo, embeddingClient, *embeddingBatch, logger,
			)
			embeddingRun.ItemsSeen = processed + failed
			embeddingRun.ItemsUpdated = processed
			embeddingRun.ItemsFailed = failed
			if failed > 0 {
				embeddingRun.Warn("%d POIs could not be embedded", failed)
			}
			if finishErr := recorder.Finish(ctx, embeddingRun, backfillErr); finishErr != nil {
				logger.Warn("could not close embedding run record", "error", finishErr)
			}
			if backfillErr != nil {
				logger.Warn("POI embedding backfill failed", "error", backfillErr)
			} else {
				logger.Info("POI embedding backfill complete", "processed", processed, "failed", failed)
			}
		}

		if *imageBatch > 0 {
			imageRun, startErr := recorder.Start(ctx, health.RunPOIImages)
			if startErr != nil {
				logger.Warn("could not open image run record", "error", startErr)
			}
			found, missing, failed, backfillErr := backfillPOIImages(
				ctx, database.Pool, poiRepo, poi.NewImageFetcher(nil), *imageBatch, logger,
			)
			imageRun.ItemsSeen = found + missing + failed
			imageRun.ItemsUpdated = found
			imageRun.ItemsFailed = failed
			if failed > 0 {
				imageRun.Warn("%d POIs could not be looked up", failed)
			}
			if finishErr := recorder.Finish(ctx, imageRun, backfillErr); finishErr != nil {
				logger.Warn("could not close image run record", "error", finishErr)
			}
			if backfillErr != nil {
				logger.Warn("POI image backfill failed", "error", backfillErr)
			} else {
				// missing is not a failure: Commons simply has no picture of
				// that place, which is the expected answer for most of them.
				logger.Info("POI image backfill complete",
					"found", found, "no_image_on_commons", missing, "failed", failed)
			}
		}

		rerankRun, startErr := recorder.Start(ctx, health.RunPreferenceRank)
		if startErr != nil {
			logger.Warn("could not open rerank run record", "error", startErr)
		}
		stats, runErr := job.Run(ctx, *lookback)
		rerankRun.ItemsSeen = stats.UsersConsidered
		rerankRun.ItemsUpdated = stats.UsersUpdated
		if stats.UsersSkipped > 0 {
			rerankRun.Warn("%d users skipped", stats.UsersSkipped)
		}
		if finishErr := recorder.Finish(ctx, rerankRun, runErr); finishErr != nil {
			logger.Warn("could not close rerank run record", "error", finishErr)
		}
		if runErr != nil {
			return runErr
		}
		logger.Info("preference re-rank complete",
			"users_considered", stats.UsersConsidered,
			"users_updated", stats.UsersUpdated,
			"users_skipped", stats.UsersSkipped,
			"signals_used", stats.SignalsUsed,
			"lookback", lookback.String())
		return nil
	}

	if err := run(); err != nil {
		logger.Error("preference re-rank failed", "error", err)
		if *interval <= 0 {
			os.Exit(1)
		}
	}
	if *interval <= 0 {
		return
	}

	logger.Info("preference re-rank scheduler started", "interval", interval.String())
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("preference re-rank scheduler stopped")
			return
		case <-ticker.C:
			if err := run(); err != nil {
				logger.Error("scheduled preference re-rank failed", "error", err)
			}
		}
	}
}

func backfillPOIEmbeddings(
	ctx context.Context,
	database *pgxpool.Pool,
	repo poi.Repository,
	client generativeAI.EmbeddingClient,
	batchSize int,
	logger *slog.Logger,
) (processed, failed int, err error) {
	if database == nil || repo == nil || client == nil || batchSize <= 0 {
		return 0, 0, nil
	}
	lockConn, err := database.Acquire(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer lockConn.Release()
	var locked bool
	const embeddingBackfillLockID int64 = 0x4c4f4345 // "LOCE"
	if err := lockConn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, embeddingBackfillLockID).Scan(&locked); err != nil {
		return 0, 0, err
	}
	if !locked {
		logger.InfoContext(ctx, "POI embedding backfill already running; skipping overlap")
		return 0, 0, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, unlockErr := lockConn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, embeddingBackfillLockID); unlockErr != nil {
			logger.WarnContext(unlockCtx, "failed to release embedding backfill lock", "error", unlockErr)
		}
	}()

	pois, err := repo.GetPOIsWithoutEmbeddings(ctx, batchSize)
	if err != nil {
		return 0, 0, err
	}
	for _, place := range pois {
		description := place.DescriptionPOI
		if description == "" {
			description = place.Description
		}
		embedding, generateErr := client.GeneratePOIEmbedding(
			ctx, place.Name, description, place.Category,
		)
		if generateErr != nil {
			failed++
			logger.WarnContext(ctx, "POI embedding generation failed",
				"poi_id", place.ID.String(), "error", generateErr)
			continue
		}
		if updateErr := repo.UpdatePOIEmbedding(ctx, place.ID, embedding); updateErr != nil {
			failed++
			logger.WarnContext(ctx, "POI embedding update failed",
				"poi_id", place.ID.String(), "error", updateErr)
			continue
		}
		processed++
	}
	return processed, failed, nil
}

// backfillPOIImages attaches a Wikimedia picture to places that have none.
//
// Mirrors backfillPOIEmbeddings, including the advisory lock, under a lock id
// of its own so the two backfills in this job cannot block each other.
//
// A place Commons knows nothing about is the common case, not an error:
// landmarks resolve, the restaurant round the corner does not. Those count as
// processed rather than failed — the job did its work and the answer was no.
func backfillPOIImages(
	ctx context.Context,
	database *pgxpool.Pool,
	repo poi.Repository,
	fetcher *poi.ImageFetcher,
	batchSize int,
	logger *slog.Logger,
) (found, missing, failed int, err error) {
	if database == nil || repo == nil || fetcher == nil || batchSize <= 0 {
		return 0, 0, 0, nil
	}
	lockConn, err := database.Acquire(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	defer lockConn.Release()
	var locked bool
	const imageBackfillLockID int64 = 0x4c4f4349 // "LOCI"
	if err := lockConn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, imageBackfillLockID).Scan(&locked); err != nil {
		return 0, 0, 0, err
	}
	if !locked {
		logger.InfoContext(ctx, "POI image backfill already running; skipping overlap")
		return 0, 0, 0, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, unlockErr := lockConn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, imageBackfillLockID); unlockErr != nil {
			logger.WarnContext(unlockCtx, "failed to release image backfill lock", "error", unlockErr)
		}
	}()

	pois, err := repo.POIsWithoutImages(ctx, uuid.Nil, batchSize)
	if err != nil {
		return 0, 0, 0, err
	}

	for _, place := range pois {
		images, fetchErr := fetcher.FetchForPOI(ctx, place.ID, place.Name, place.City, 1)
		if fetchErr != nil {
			failed++
			logger.WarnContext(ctx, "POI image lookup failed",
				"poi_id", place.ID.String(), "error", fetchErr)
			continue
		}
		if len(images) == 0 {
			missing++
			continue
		}
		if saveErr := repo.SavePOIImages(ctx, images); saveErr != nil {
			failed++
			logger.WarnContext(ctx, "POI image save failed",
				"poi_id", place.ID.String(), "error", saveErr)
			continue
		}
		found++
	}
	return found, missing, failed, nil
}
