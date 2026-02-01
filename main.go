package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/reign/internal/config"
	"github.com/reign/internal/handlers"
	"github.com/reign/internal/logger"
	"github.com/reign/internal/models"
	"github.com/reign/internal/orchestrator"
	"github.com/reign/internal/version"
)

func main() {
	// Parse command-line flags
	configPath, showVersion := config.ParseFlags()

	if showVersion {
		fmt.Println(version.Get())
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatal("Failed to load configuration: %v", err)
	}

	// Set log level
	if err := logger.SetLevelFromString(cfg.LogLevel); err != nil {
		logger.Warn("Invalid log level '%s', using 'info'", cfg.LogLevel)
	}

	logger.Info("Starting %s", version.Get())
	logger.Info("Listening on %s", cfg.ListenAddr)
	logger.Info("Database: %s", cfg.DatabasePath)

	// Initialize database
	db, err := models.InitDB(cfg.DatabasePath)
	if err != nil {
		logger.Fatal("Failed to initialize database: %v", err)
	}
	defer db.Close()

	logger.Info("Database initialized")

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create orchestrator
	orch := orchestrator.New(db, cfg)

	// Set up HTTP handlers
	mux := http.NewServeMux()

	healthHandler := handlers.NewHealthHandler(db, orch)
	servicesHandler := handlers.NewServicesHandler(db, orch)

	// Set version for health handler
	handlers.SetVersion(version.Short())

	// Register routes
	mux.Handle("/health", healthHandler)
	mux.Handle("/services", servicesHandler)
	mux.Handle("/services/", servicesHandler)

	// Create HTTP server
	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start services
	if err := orch.StartupServices(ctx); err != nil {
		logger.Error("Error during startup: %v", err)
	}

	// Start health checker
	orch.StartHealthChecker(ctx)

	// Start HTTP server in goroutine
	go func() {
		logger.Info("HTTP API server starting")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received signal: %v, shutting down...", sig)

	// Cancel context to stop background operations
	cancel()

	// Shutdown HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error: %v", err)
	}

	// Shutdown services
	if err := orch.ShutdownServices(shutdownCtx); err != nil {
		logger.Error("Error during service shutdown: %v", err)
	}

	logger.Info("Shutdown complete")
}
