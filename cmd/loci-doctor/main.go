// Command loci-doctor reports whether Loci's derived data can be trusted.
//
// It answers, in one place, the questions that previously required reading logs
// and counting nulls by hand: how much of the POI corpus is searchable, how many
// crowd-verified facts have expired, and whether the jobs that maintain either
// are still running.
//
// Exit codes are meant for a health check or a cron wrapper:
//
//	0  ready     — nothing wrong
//	1  degraded  — retrieval works but is worse than it should be
//	2  stale     — a maintenance job is not running, or semantic search is unusable
//	3  empty     — the corpus has no POIs at all
//	4  error     — the check itself could not run
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/health"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

const (
	exitReady    = 0
	exitDegraded = 1
	exitStale    = 2
	exitEmpty    = 3
	exitError    = 4
)

func main() {
	asJSON := flag.Bool("json", false, "emit the full report as JSON")
	timeout := flag.Duration("timeout", 30*time.Second, "overall timeout for the check")
	flag.Parse()

	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	cfg, err := config.Load()
	if err != nil {
		fail("config load failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	database, err := db.New(db.Config{
		DSN:             cfg.Database.DSN(),
		MaxConns:        2,
		MinConns:        1,
		MaxConnLifetime: cfg.Database.MaxConnLifetime,
		MaxConnIdleTime: cfg.Database.MaxConnIdleTime,
	}, logger)
	if err != nil {
		fail("database connect failed: %v", err)
	}
	defer database.Close()

	status, err := health.NewService(database.Pool).Corpus(ctx)
	if err != nil {
		fail("health check failed: %v", err)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(status); err != nil {
			fail("could not encode report: %v", err)
		}
	} else {
		printReport(status)
	}

	os.Exit(exitFor(status.Status))
}

func printReport(s *health.CorpusStatus) {
	fmt.Printf("Loci corpus: %s\n\n", s.Status)

	fmt.Printf("  POIs                 %d\n", s.POICount)
	fmt.Printf("  without embedding    %d\n", s.POIsMissingEmbedding)
	fmt.Printf("  cities               %d\n", s.CityCount)
	fmt.Printf("  verified facts       %d (%d expired)\n", s.FactsTotal, s.FactsExpired)
	fmt.Printf("  semantic search      %s\n", readiness(s.SemanticSearchReady()))

	fmt.Println("\n  Background jobs")
	for kind, run := range s.LastRuns {
		switch {
		case run == nil:
			fmt.Printf("    %-20s never completed a pass\n", kind)
		case run.CompletedAt == nil:
			fmt.Printf("    %-20s in flight since %s\n", kind, run.StartedAt.Format(time.RFC3339))
		default:
			outcome := "ok"
			if !run.Success {
				outcome = "FAILED"
			}
			fmt.Printf("    %-20s %s  %s ago  (%d seen, %d updated, %d failed)\n",
				kind, outcome, time.Since(*run.CompletedAt).Round(time.Minute),
				run.ItemsSeen, run.ItemsUpdated, run.ItemsFailed)
		}
	}

	if len(s.StaleReasons) == 0 {
		fmt.Println("\n  No problems found.")
		return
	}
	fmt.Println("\n  Problems")
	for _, reason := range s.StaleReasons {
		fmt.Printf("    - %s\n", reason)
	}
}

func readiness(ready bool) string {
	if ready {
		return "usable"
	}
	return "NOT usable (too few embeddings; lexical search still works)"
}

func exitFor(status string) int {
	switch status {
	case "ready":
		return exitReady
	case "degraded":
		return exitDegraded
	case "stale":
		return exitStale
	case "empty":
		return exitEmpty
	default:
		return exitError
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "loci-doctor: "+format+"\n", args...)
	os.Exit(exitError)
}
