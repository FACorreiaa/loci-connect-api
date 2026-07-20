package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

func main() {
	lookback := flag.Duration("lookback", 7*24*time.Hour, "only users with feedback in this window (0 = all)")
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

	stats, err := job.Run(ctx, *lookback)
	if err != nil {
		logger.Error("preference re-rank failed", "error", err)
		os.Exit(1)
	}
	logger.Info("preference re-rank complete",
		"users_considered", stats.UsersConsidered,
		"users_updated", stats.UsersUpdated,
		"users_skipped", stats.UsersSkipped,
		"signals_used", stats.SignalsUsed,
		"lookback", lookback.String())
}
