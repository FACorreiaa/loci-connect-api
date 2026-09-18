// Command bundle-forge generates, reviews and publishes City Packs.
//
// Packs are product: they are generated offline, read by a person, and only
// then published. That is why this is a binary somebody runs rather than an
// endpoint. Generation spends real model budget and is deliberately outside
// the metered RPC path; publishing is a judgement about whether writing is
// good enough to sell.
//
// The pipeline is draft -> approved -> published, with archive as the way out.
// Only published packs are ever served.
//
//	bundle-forge -mode=generate -limit=3 -city=Lisbon
//	bundle-forge -mode=list -status=draft
//	bundle-forge -mode=review -slug=lisbon-jacarandas-3day
//	bundle-forge -mode=approve -slug=lisbon-jacarandas-3day -by=me@example.com
//	bundle-forge -mode=publish -slug=lisbon-jacarandas-3day
//
// generate needs the model provider and so is usually run in the cluster;
// review, approve and publish only need the database and read better in a
// terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/bundle"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/bundle/forge"
	"github.com/FACorreiaa/loci-connect-api/pkg/ai"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

func main() {
	mode := flag.String("mode", "list", "generate | list | review | approve | publish | archive")
	slug := flag.String("slug", "", "pack to act on (review, approve, publish, archive)")
	status := flag.String("status", "draft", "status to list")
	city := flag.String("city", "", "only this city (generate)")
	theme := flag.String("theme", "", "only this theme (generate)")
	limit := flag.Int("limit", 1, "how many packs to generate")
	paid := flag.Bool("paid", true, "generated packs are paid")
	approvedBy := flag.String("by", "", "who approved (defaults to ADMIN_EMAIL)")
	dryRun := flag.Bool("dry-run", false, "generate and print, write nothing")
	out := flag.String("out", "", "write review output to this file instead of stdout")
	flag.Parse()

	_ = godotenv.Load()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		fail(logger, "config load failed", err)
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
		fail(logger, "db connect failed", err)
	}
	defer database.Close()

	repo := bundle.NewRepository(database.Pool, logger)

	switch *mode {
	case "generate":
		err = runGenerate(ctx, cfg, repo, logger, *city, *theme, *limit, *paid, *dryRun)
	case "list":
		err = runList(ctx, repo, bundle.Status(*status))
	case "review":
		err = runReview(ctx, repo, *slug, *out)
	case "approve":
		by := *approvedBy
		if by == "" {
			by = cfg.Auth.AdminEmail
		}
		err = runTransition(ctx, repo, *slug, bundle.StatusApproved, by)
	case "publish":
		err = runPublish(ctx, repo, *slug)
	case "archive":
		err = runTransition(ctx, repo, *slug, bundle.StatusRetired, "")
	default:
		err = fmt.Errorf("unknown mode %q", *mode)
	}

	if err != nil {
		fail(logger, "bundle-forge failed", err)
	}
}

func fail(logger *slog.Logger, msg string, err error) {
	logger.Error(msg, "error", err)
	os.Exit(1)
}

// llmGenerator asks the configured model for a pack.
type llmGenerator struct {
	client generativeAI.ChatClient
}

func (g *llmGenerator) GeneratePlan(ctx context.Context, prompt string) (forge.Plan, string, error) {
	raw, err := g.client.GenerateText(ctx, prompt, nil)
	if err != nil {
		return forge.Plan{}, "", fmt.Errorf("generate: %w", err)
	}
	plan, err := forge.ParsePlan(raw)
	if err != nil {
		return forge.Plan{}, "", err
	}
	return plan, g.client.Model(), nil
}

func runGenerate(
	ctx context.Context,
	cfg *config.Config,
	repo *bundle.RepositoryImpl,
	logger *slog.Logger,
	city, theme string,
	limit int,
	paid, dryRun bool,
) error {
	seeds, err := forge.LoadSeeds()
	if err != nil {
		return err
	}
	seeds = forge.Filter(seeds, city, theme)
	if len(seeds) == 0 {
		return errors.New("no seeds match those filters")
	}

	client, err := ai.NewChatClient(ctx, cfg.AI, logger)
	if err != nil {
		return fmt.Errorf("ai client: %w", err)
	}
	defer client.Close()
	gen := &llmGenerator{client: client}

	done := 0
	for _, seed := range seeds {
		if done >= limit {
			break
		}

		// A slug that already exists is a pack somebody may already have
		// reviewed or bought. Skipping is the only safe default; regenerating
		// one is an explicit archive-then-generate.
		exists, err := repo.SlugExists(ctx, seed.Slug())
		if err != nil {
			return err
		}
		if exists {
			logger.Info("skipping, already generated", "slug", seed.Slug())
			continue
		}

		logger.Info("generating", "city", seed.City, "theme", seed.Theme, "days", seed.Days)
		plan, model, err := gen.GeneratePlan(ctx, forge.Prompt(seed))
		if err != nil {
			// One bad city must not end the run: the next seed may be fine,
			// and the model call already cost money.
			logger.Error("generation failed", "city", seed.City, "error", err)
			continue
		}

		draft := forge.ToDraft(seed, plan, model, nil, paid)
		if dryRun {
			b := &bundle.Bundle{
				Title: draft.Title, Slug: draft.Slug, Summary: draft.Summary,
				CityName: draft.CityName, CountryCode: draft.CountryCode,
				Theme: draft.Theme, Months: draft.Months,
				IsPaid: draft.IsPaid, Status: bundle.StatusDraft,
				DayCount: len(draft.Days), SourceModel: draft.SourceModel,
			}
			fmt.Print(forge.Render(b, draft.Days))
			done++
			continue
		}

		id, err := repo.CreateDraft(ctx, draft)
		if err != nil {
			return fmt.Errorf("write draft for %s: %w", seed.City, err)
		}
		logger.Info("draft written", "slug", draft.Slug, "id", id, "days", len(draft.Days))
		done++
	}

	if done == 0 {
		return errors.New("nothing generated")
	}
	fmt.Fprintf(os.Stderr, "\n%d pack(s) generated. Read one with -mode=review -slug=...\n", done)
	return nil
}

func runList(ctx context.Context, repo *bundle.RepositoryImpl, status bundle.Status) error {
	bundles, err := repo.ListByStatus(ctx, status)
	if err != nil {
		return err
	}
	if len(bundles) == 0 {
		fmt.Printf("no packs with status %q\n", status)
		return nil
	}
	for _, b := range bundles {
		paid := "free"
		if b.IsPaid {
			paid = "paid"
		}
		fmt.Printf("%-44s  %-10s  %-12s  %2dd  %s\n", b.Slug, b.Theme, paid, b.DayCount, b.CityName)
	}
	return nil
}

func runReview(ctx context.Context, repo *bundle.RepositoryImpl, slug, out string) error {
	if slug == "" {
		return errors.New("-slug is required")
	}
	b, err := repo.GetAnyBySlug(ctx, slug)
	if err != nil {
		return err
	}
	days, err := repo.LoadDays(ctx, b.ID, 0)
	if err != nil {
		return err
	}

	rendered := forge.Render(b, days)
	if out == "" {
		fmt.Print(rendered)
		return nil
	}
	if err := os.WriteFile(out, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("write review: %w", err)
	}
	fmt.Fprintf(os.Stderr, "written to %s\n", out)
	return nil
}

func runTransition(ctx context.Context, repo *bundle.RepositoryImpl, slug string, to bundle.Status, by string) error {
	if slug == "" {
		return errors.New("-slug is required")
	}
	b, err := repo.GetAnyBySlug(ctx, slug)
	if err != nil {
		return err
	}
	if err := repo.SetStatus(ctx, b.ID, to, by); err != nil {
		return err
	}
	fmt.Printf("%s: %s -> %s\n", slug, b.Status, to)
	return nil
}

// runPublish is the last gate before a pack can be sold.
//
// It refuses rather than warns. A paid guide with an empty map or a day with
// no stops is a broken product, and the map link builder in the client
// silently renders nothing for a stop with no coordinates, so the failure
// would reach a customer before it reached us.
func runPublish(ctx context.Context, repo *bundle.RepositoryImpl, slug string) error {
	if slug == "" {
		return errors.New("-slug is required")
	}
	b, err := repo.GetAnyBySlug(ctx, slug)
	if err != nil {
		return err
	}
	if b.Status != bundle.StatusApproved {
		return fmt.Errorf("%s is %s; only an approved pack can be published", slug, b.Status)
	}

	days, err := repo.LoadDays(ctx, b.ID, 0)
	if err != nil {
		return err
	}
	if issues := forge.Validate(b, days); len(issues) > 0 {
		return fmt.Errorf("refusing to publish %s:\n  - %s", slug, strings.Join(issues, "\n  - "))
	}

	if err := repo.SetStatus(ctx, b.ID, bundle.StatusPublished, ""); err != nil {
		return err
	}
	fmt.Printf("%s published\n", slug)
	return nil
}
