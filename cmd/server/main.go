package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/cmd/api"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

func main() {
	if err := godotenv.Load(); err != nil {
		slog.Warn("Error loading .env file")
		log.Fatal(err)
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

	// Initialize dependencies
	deps, err := api.InitDependencies(cfg, logger)
	if err != nil {
		logger.Error("failed to initialize dependencies", "error", err)
		os.Exit(1)
	}
	defer deps.Cleanup()

	// Start pprof server if enabled
	if cfg.Profiling.Enabled {
		go startPprofServer(cfg, logger)
	}

	// Setup router
	handler := api.SetupRouter(deps)

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
		Addr:    addr,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("pprof server error", "error", err)
	}
}

// runServer starts the HTTP server with graceful shutdown
func runServer(cfg *config.Config, logger *slog.Logger, handler http.Handler) error {
	// Create HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
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
