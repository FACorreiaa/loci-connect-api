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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

func main() {
	lookback := flag.Duration("lookback", 7*24*time.Hour, "only users with feedback in this window (0 = all)")
	interval := flag.Duration("interval", 0, "repeat interval (0 = run once)")
	embeddingBatch := flag.Int("embedding-batch", 50, "backfill this many missing POI embeddings before reranking (0 = disabled)")
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

	store := preference.NewVectorStore(database.Pool, logger)
	job := preference.NewReranker(database.Pool, store, logger)
	poiRepo := poi.NewRepository(database.Pool, logger)
	var embeddingClient generativeAI.EmbeddingClient
	if *embeddingBatch > 0 {
		embeddingClient, err = generativeAI.NewGeminiEmbeddingClient(
			ctx,
			cfg.Gemini.APIKey,
			cfg.Gemini.EmbeddingModel,
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
			processed, failed, backfillErr := backfillPOIEmbeddings(
				ctx, database.Pool, poiRepo, embeddingClient, *embeddingBatch, logger,
			)
			if backfillErr != nil {
				logger.Warn("POI embedding backfill failed", "error", backfillErr)
			} else {
				logger.Info("POI embedding backfill complete", "processed", processed, "failed", failed)
			}
		}
		stats, runErr := job.Run(ctx, *lookback)
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
