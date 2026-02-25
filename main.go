package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/reign/internal/cli"
	"github.com/reign/internal/config"
	"github.com/reign/internal/handlers"
	"github.com/reign/internal/logger"
	"github.com/reign/internal/models"
	"github.com/reign/internal/orchestrator"
	"github.com/reign/internal/version"
)

func main() {
	// Check for CLI commands first
	handled, err := cli.Run(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if handled {
		os.Exit(0)
	}

	// Parse command-line flags for server mode
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

	// Events handler
	eventsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte(`{"error":"method not allowed"}`))
			return
		}

		follow := r.URL.Query().Get("follow") == "true"

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := fmt.Sscanf(l, "%d", &limit); err != nil || n != 1 || limit <= 0 {
				limit = 50
			}
		}

		if follow {
			streamEvents(w, r, db, limit)
			return
		}

		events, err := models.GetEventLog(db, limit)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"failed to get event log"}`))
			return
		}
		if events == nil {
			events = []models.EventLogEntry{}
		}

		resp := handlers.EventLogResponse{
			Events: events,
			Total:  len(events),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// Register routes
	mux.Handle("/health", healthHandler)
	mux.Handle("/events", eventsHandler)
	mux.Handle("/services", servicesHandler)
	mux.Handle("/services/", servicesHandler)

	// Create HTTP server
	server := &http.Server{
		Handler:     mux,
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	// Bind to the port early so we fail fast if another instance is running
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.Fatal("Failed to bind to %s (is another reign instance running?): %v", cfg.ListenAddr, err)
	}

	// Start services (sync first if keepRunning is enabled)
	if err := orch.StartupServices(ctx, cfg.KeepRunning); err != nil {
		logger.Error("Error during startup: %v", err)
	}

	// Start health checker
	orch.StartHealthChecker(ctx)

	// Start HTTP server in goroutine
	go func() {
		logger.Info("HTTP API server starting")
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
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
	if err := orch.ShutdownServices(shutdownCtx, cfg.KeepRunning); err != nil {
		logger.Error("Error during service shutdown: %v", err)
	}

	logger.Info("Shutdown complete")
}

// streamEvents streams events using Server-Sent Events, polling for new events.
func streamEvents(w http.ResponseWriter, r *http.Request, db *sql.DB, initialLimit int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"streaming not supported"}`))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// First send recent events as initial batch
	events, err := models.GetEventLog(db, initialLimit)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	lastID := 0
	for _, e := range events {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if e.ID > lastID {
			lastID = e.ID
		}
	}
	flusher.Flush()

	// If no events, get the current max ID so we only stream new ones
	if lastID == 0 {
		lastID, _ = models.GetMaxEventID(db)
	}

	// Poll for new events
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			newEvents, err := models.GetEventLogSince(db, lastID)
			if err != nil {
				continue
			}
			for _, e := range newEvents {
				data, _ := json.Marshal(e)
				fmt.Fprintf(w, "data: %s\n\n", data)
				if e.ID > lastID {
					lastID = e.ID
				}
			}
			if len(newEvents) > 0 {
				flusher.Flush()
			}
		}
	}
}
