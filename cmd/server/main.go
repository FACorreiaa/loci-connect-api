package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/cmd/api"
	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

func main() {
	// A .env file is a development convenience, not a requirement. In a
	// container every value arrives as an environment variable and there is no
	// file to read, so a missing one is normal and config.Load below is what
	// decides whether the resulting configuration is usable.
	//
	// This used to warn and then log.Fatal on the same error, which meant the
	// server could not start anywhere without a .env on disk — it crash-looped
	// on its first real deploy. cmd/loci-doctor and cmd/preference-rerank
	// already ignored it; this is now consistent with them.
	if err := godotenv.Load(); err != nil {
		slog.Warn("no .env file; reading configuration from the environment",
			slog.String("error", err.Error()))
	}

	// Initialize logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("starting loci API")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// The one line that says what the running value is. OPENROUTER_MODEL lives
	// in a sealed secret nobody can read from outside the pod, and the
	// difference between a cheap model and "openrouter/auto" is a bill, so
	// the resolved model is logged at boot where it can be checked.
	logger.Info("chat model resolved",
		"provider", cfg.AI.Provider,
		"model", cfg.AI.Model,
		"fallbacks", len(cfg.AI.Fallbacks))

	// Tracing must be installed before the router captures the global
	// tracer provider. No-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set.
	shutdownTracing, err := observability.InitTracing(context.Background(), "loci-connect-api", logger)
	if err != nil {
		logger.Error("failed to initialize tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(ctx); err != nil {
			logger.Warn("tracing shutdown failed", "error", err)
		}
	}()

	// Metric export is separate from tracing and separately opt-in: a host may
	// scrape /metrics, push OTLP, both, or neither.
	shutdownMetrics, metricsErr := observability.InitMetrics(context.Background(), "loci-connect-api", logger)
	if metricsErr != nil {
		logger.Error("failed to initialize metric export", "error", metricsErr)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownMetrics(ctx); err != nil {
			logger.Warn("metric export shutdown failed", "error", err)
		}
	}()

	// Initialize dependencies
	deps, err := api.InitDependencies(cfg, logger)
	if err != nil {
		logger.Error("failed to initialize dependencies", "error", err)
		os.Exit(1)
	}
	defer deps.Cleanup()

	// Start pprof server if enabled
	if cfg.Profiling.Enabled {
		concurrency.Run(logger, func() { startPprofServer(cfg, logger) })
	}

	// Setup router
	handler := api.SetupRouter(deps)

	// Background loops that live as long as the API does. Cancelled when the
	// server stops, which ends the Telegram long poll rather than leaving it
	// holding a request against Telegram.
	backgroundCtx, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()

	// Receive Telegram messages alongside the API.
	//
	// Returns immediately when no bot is configured, so this is started
	// unconditionally.
	concurrency.Run(logger, func() {
		if err := deps.RunTelegram(backgroundCtx); err != nil {
			// The bridge stopping is not the API failing: itineraries are
			// still answered in the app, and taking the server down with it
			// would turn a chat outage into an outage.
			logger.Error("telegram bridge stopped", "error", err)
		}
	})

	// Keep Apple's client secret fresh alongside the API.
	//
	// Returns immediately when Apple sign-in is not configured, so this is
	// started unconditionally.
	concurrency.Run(logger, func() {
		if err := deps.RunAppleSecretRefresh(backgroundCtx); err != nil {
			// The renewal loop stopping does not break sign-in today — the
			// secret it last signed is valid for months — so it is not a
			// reason to take the API down.
			logger.Error("apple client secret renewal stopped", "error", err)
		}
	})

	// Start HTTP server
	if err := runServer(cfg, logger, handler); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

// startPprofServer starts the pprof profiling server on a separate port
func startPprofServer(cfg *config.Config, logger *slog.Logger) {
	mux := http.NewServeMux()

	// Register pprof service
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	addr := fmt.Sprintf("localhost:%d", cfg.Profiling.Port)
	logger.Info("pprof server started", "addr", addr, "endpoints", "/debug/pprof/")

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Profiles (e.g. /debug/pprof/profile?seconds=30) stream for as long as
		// the caller asks, so the write side stays unbounded like the API server.
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("pprof server error", "error", err)
	}
}

// runServer starts the HTTP server with graceful shutdown
func runServer(cfg *config.Config, logger *slog.Logger, handler http.Handler) error {
	// Create HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	// Enable HTTP/2 support (h2c - HTTP/2 without TLS)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		// ReadHeaderTimeout: bounds a client that trickles headers (slowloris);
		// stated explicitly rather than inherited from ReadTimeout.
		ReadHeaderTimeout: 10 * time.Second,
		// ReadTimeout: Time to read the entire request including body
		ReadTimeout: 30 * time.Second,
		// WriteTimeout: For streaming endpoints (SSE/gRPC streams), this must be long enough
		// to handle the entire response duration. LLM responses can take 30+ seconds.
		// Setting to 0 disables the timeout - we rely on application-level timeouts
		// (context.WithTimeout in chat_process_stream.go) for proper deadline management.
		WriteTimeout: 0,
		// IdleTimeout: Time to wait for the next request when keep-alives are enabled
		IdleTimeout: 120 * time.Second,
		Protocols:   protocols,
	}

	// Start server in goroutine
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server started", "addr", addr)
		serverErrors <- srv.ListenAndServe()
	}()

	// Wait for interrupt signal
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdown:
		logger.Info("shutdown signal received", "signal", sig)

		// Graceful shutdown with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			srv.Close()
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}

		logger.Info("server stopped gracefully")
	}

	return nil
}
