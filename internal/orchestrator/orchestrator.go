// Package orchestrator provides the core service lifecycle management for Reign.
package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/reign/internal/config"
	"github.com/reign/internal/executor"
	"github.com/reign/internal/logger"
	"github.com/reign/internal/models"
)

// Orchestrator manages service lifecycles.
type Orchestrator struct {
	db              *sql.DB
	config          *config.Config
	composeExecutor *executor.ComposeExecutor
	binaryExecutor  *executor.BinaryExecutor

	mu        sync.RWMutex
	startedAt time.Time

	// stopChan signals background goroutines to stop
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// New creates a new Orchestrator.
func New(db *sql.DB, cfg *config.Config) *Orchestrator {
	return &Orchestrator{
		db:              db,
		config:          cfg,
		composeExecutor: &executor.ComposeExecutor{},
		binaryExecutor:  executor.NewBinaryExecutor(),
		startedAt:       time.Now(),
		stopChan:        make(chan struct{}),
	}
}

// StartupServices starts all enabled services in the correct order.
// If syncFirst is true, it first probes actual service states and only starts services that aren't running.
func (o *Orchestrator) StartupServices(ctx context.Context, syncFirst bool) error {
	if syncFirst {
		if err := o.SyncServiceStates(ctx); err != nil {
			logger.Warn("Failed to sync service states: %v", err)
		}
	}

	logger.Info("Starting enabled services...")

	services, err := models.ListEnabledServicesForStartup(o.db)
	if err != nil {
		return fmt.Errorf("failed to list services for startup: %w", err)
	}

	if len(services) == 0 {
		logger.Info("No enabled services to start")
		return nil
	}

	started := 0

	// Start infrastructure services first
	for _, service := range services {
		if !service.Infrastructure {
			break // Infrastructure services are first in the list
		}

		// Check if already running (from sync)
		state, _ := models.GetServiceState(o.db, service.ID)
		if state != nil && state.Status == models.StatusRunning {
			logger.Debug("Infrastructure service %s already running, skipping", service.ID)
			continue
		}

		logger.Info("Starting infrastructure service: %s", service.ID)
		if err := o.StartService(ctx, service.ID); err != nil {
			logger.Error("Failed to start infrastructure service %s: %v", service.ID, err)
			// Continue with other infrastructure services
			continue
		}
		started++

		// Wait for infrastructure service to be healthy
		if err := o.waitForHealthy(ctx, &service); err != nil {
			logger.Warn("Infrastructure service %s did not become healthy: %v", service.ID, err)
		}
	}

	// Start regular services
	for _, service := range services {
		if service.Infrastructure {
			continue // Skip infrastructure services (already started)
		}

		// Check if already running (from sync)
		state, _ := models.GetServiceState(o.db, service.ID)
		if state != nil && state.Status == models.StatusRunning {
			logger.Debug("Service %s already running, skipping", service.ID)
			continue
		}

		logger.Info("Starting service: %s", service.ID)
		if err := o.StartService(ctx, service.ID); err != nil {
			logger.Error("Failed to start service %s: %v", service.ID, err)
			continue
		}
		started++
	}

	logger.Info("Startup complete. Started %d services (%d already running).", started, len(services)-started)
	return nil
}

// ShutdownServices stops all running services (unless keepRunning is enabled).
func (o *Orchestrator) ShutdownServices(ctx context.Context, keepRunning bool) error {
	logger.Info("Shutting down orchestrator...")

	// Signal background goroutines to stop
	close(o.stopChan)

	if keepRunning {
		logger.Info("keepRunning enabled - leaving services running")
		o.wg.Wait()
		logger.Info("Shutdown complete (services still running)")
		return nil
	}

	services, err := models.ListServices(o.db)
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	// Stop services in reverse alphabetical order
	for i := len(services) - 1; i >= 0; i-- {
		service := services[i]
		state, err := models.GetServiceState(o.db, service.ID)
		if err != nil {
			logger.Error("Failed to get state for %s: %v", service.ID, err)
			continue
		}

		if state != nil && state.Status == models.StatusRunning {
			logger.Info("Stopping service: %s", service.ID)
			if err := o.StopService(ctx, service.ID); err != nil {
				logger.Error("Failed to stop service %s: %v", service.ID, err)
			}
		}
	}

	// Wait for background goroutines
	o.wg.Wait()

	logger.Info("Shutdown complete")
	return nil
}

// SyncServiceStates probes the actual state of all services and updates the database.
// This is used on startup when keepRunning is enabled to discover services that are still running.
func (o *Orchestrator) SyncServiceStates(ctx context.Context) error {
	logger.Info("Syncing service states with actual running state...")

	services, err := models.ListServices(o.db)
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	synced := 0
	for _, service := range services {
		exec := o.getExecutor(service.Type)
		if exec == nil {
			logger.Warn("Unknown service type for %s: %s", service.ID, service.Type)
			continue
		}

		// Probe actual status from executor
		actualStatus, err := exec.Status(ctx, &service)
		if err != nil {
			logger.Warn("Failed to get status for %s: %v", service.ID, err)
			continue
		}

		// Get current database state
		state, err := models.GetServiceState(o.db, service.ID)
		if err != nil {
			logger.Warn("Failed to get state for %s: %v", service.ID, err)
			continue
		}

		// Skip disabled services
		if state != nil && state.Status == models.StatusDisabled {
			continue
		}

		// Update database to match actual state
		if actualStatus == models.StatusRunning {
			if state == nil || state.Status != models.StatusRunning {
				logger.Info("Service %s is running (synced)", service.ID)
				if err := models.SetServiceStarted(o.db, service.ID, nil); err != nil {
					logger.Error("Failed to update state for %s: %v", service.ID, err)
				}
				synced++
			}
		} else if actualStatus == models.StatusStopped {
			if state != nil && state.Status == models.StatusRunning {
				logger.Info("Service %s is stopped (synced)", service.ID)
				if err := models.SetServiceStopped(o.db, service.ID); err != nil {
					logger.Error("Failed to update state for %s: %v", service.ID, err)
				}
				synced++
			}
		}
	}

	logger.Info("Synced %d service states", synced)
	return nil
}

// StartService starts a specific service.
func (o *Orchestrator) StartService(ctx context.Context, id string) error {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return fmt.Errorf("service not found: %s", id)
	}

	state, err := models.GetServiceState(o.db, id)
	if err != nil {
		return fmt.Errorf("failed to get service state: %w", err)
	}
	if state != nil && state.Status == models.StatusDisabled {
		return fmt.Errorf("service is disabled: %s", id)
	}

	// Update status to starting
	if err := models.UpdateServiceStatus(o.db, id, models.StatusStarting); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	models.LogEvent(o.db, id, "starting", "Service start initiated")

	// Reset restart count on manual start
	models.ResetRestartCount(o.db, id)

	// Get executor
	exec := o.getExecutor(service.Type)
	if exec == nil {
		models.SetServiceFailed(o.db, id, "unknown service type")
		return fmt.Errorf("unknown service type: %s", service.Type)
	}

	// Pull images first (for compose services)
	if service.Type == models.ServiceTypeCompose {
		logger.Debug("Pulling images for %s", id)
		if err := exec.Pull(ctx, service); err != nil {
			logger.Warn("Failed to pull images for %s: %v", id, err)
			models.LogEvent(o.db, id, "pull_failed", err.Error())
			// Continue anyway - might work with cached images
		} else {
			models.LogEvent(o.db, id, "pulled", "Images pulled successfully")
		}
	}

	// Start the service
	if err := exec.Start(ctx, service); err != nil {
		models.SetServiceFailed(o.db, id, err.Error())
		models.LogEvent(o.db, id, "failed", err.Error())
		return fmt.Errorf("failed to start service: %w", err)
	}

	// Get PID for binary services
	var pid *int
	if service.Type == models.ServiceTypeBinary {
		pid = o.binaryExecutor.GetPID(id)
	}

	// Update status to running
	if err := models.SetServiceStarted(o.db, id, pid); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	models.LogEvent(o.db, id, "started", "Service started successfully")

	return nil
}

// StopService stops a specific service.
func (o *Orchestrator) StopService(ctx context.Context, id string) error {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return fmt.Errorf("service not found: %s", id)
	}

	// Update status to stopping
	if err := models.UpdateServiceStatus(o.db, id, models.StatusStopping); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	models.LogEvent(o.db, id, "stopping", "Service stop initiated")

	// Get executor
	exec := o.getExecutor(service.Type)
	if exec == nil {
		return fmt.Errorf("unknown service type: %s", service.Type)
	}

	// Stop the service
	if err := exec.Stop(ctx, service); err != nil {
		models.LogEvent(o.db, id, "stop_failed", err.Error())
		return fmt.Errorf("failed to stop service: %w", err)
	}

	// Update status to stopped
	if err := models.SetServiceStopped(o.db, id); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	models.LogEvent(o.db, id, "stopped", "Service stopped successfully")

	return nil
}

// RestartService restarts a specific service.
func (o *Orchestrator) RestartService(ctx context.Context, id string) error {
	if err := o.StopService(ctx, id); err != nil {
		logger.Warn("Error stopping service %s during restart: %v", id, err)
	}

	// Brief pause between stop and start
	time.Sleep(1 * time.Second)

	return o.StartService(ctx, id)
}

// StartServiceAsync validates and initiates a service start in the background.
// It returns the service with its current state immediately after setting the status to "starting".
func (o *Orchestrator) StartServiceAsync(id string) (*models.ServiceWithState, error) {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return nil, fmt.Errorf("service not found: %s", id)
	}

	state, err := models.GetServiceState(o.db, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get service state: %w", err)
	}
	if state != nil && state.Status == models.StatusDisabled {
		return nil, fmt.Errorf("service is disabled: %s", id)
	}

	// Update status to starting
	if err := models.UpdateServiceStatus(o.db, id, models.StatusStarting); err != nil {
		return nil, fmt.Errorf("failed to update status: %w", err)
	}
	models.LogEvent(o.db, id, "starting", "Service start initiated")
	models.ResetRestartCount(o.db, id)

	// Build the response before launching the goroutine to avoid races
	result := &models.ServiceWithState{Service: *service}
	if state != nil {
		result.State = *state
	}
	result.State.Status = models.StatusStarting

	// Run the actual start in the background
	go func() {
		ctx := context.Background()
		exec := o.getExecutor(service.Type)
		if exec == nil {
			models.SetServiceFailed(o.db, id, "unknown service type")
			return
		}

		if service.Type == models.ServiceTypeCompose {
			logger.Debug("Pulling images for %s", id)
			if err := exec.Pull(ctx, service); err != nil {
				logger.Warn("Failed to pull images for %s: %v", id, err)
				models.LogEvent(o.db, id, "pull_failed", err.Error())
			} else {
				models.LogEvent(o.db, id, "pulled", "Images pulled successfully")
			}
		}

		if err := exec.Start(ctx, service); err != nil {
			models.SetServiceFailed(o.db, id, err.Error())
			models.LogEvent(o.db, id, "failed", err.Error())
			logger.Error("Failed to start service %s: %v", id, err)
			return
		}

		var pid *int
		if service.Type == models.ServiceTypeBinary {
			pid = o.binaryExecutor.GetPID(id)
		}

		if err := models.SetServiceStarted(o.db, id, pid); err != nil {
			logger.Error("Failed to update status for %s: %v", id, err)
			return
		}
		models.LogEvent(o.db, id, "started", "Service started successfully")
	}()

	return result, nil
}

// StopServiceAsync validates and initiates a service stop in the background.
// It returns the service with its current state immediately after setting the status to "stopping".
func (o *Orchestrator) StopServiceAsync(id string) (*models.ServiceWithState, error) {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return nil, fmt.Errorf("service not found: %s", id)
	}

	state, err := models.GetServiceState(o.db, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get service state: %w", err)
	}

	// Update status to stopping
	if err := models.UpdateServiceStatus(o.db, id, models.StatusStopping); err != nil {
		return nil, fmt.Errorf("failed to update status: %w", err)
	}
	models.LogEvent(o.db, id, "stopping", "Service stop initiated")

	// Build the response before launching the goroutine to avoid races
	result := &models.ServiceWithState{Service: *service}
	if state != nil {
		result.State = *state
	}
	result.State.Status = models.StatusStopping

	// Run the actual stop in the background
	go func() {
		ctx := context.Background()
		exec := o.getExecutor(service.Type)
		if exec == nil {
			logger.Error("Unknown service type for %s: %s", id, service.Type)
			return
		}

		if err := exec.Stop(ctx, service); err != nil {
			models.LogEvent(o.db, id, "stop_failed", err.Error())
			logger.Error("Failed to stop service %s: %v", id, err)
			return
		}

		if err := models.SetServiceStopped(o.db, id); err != nil {
			logger.Error("Failed to update status for %s: %v", id, err)
			return
		}
		models.LogEvent(o.db, id, "stopped", "Service stopped successfully")
	}()

	return result, nil
}

// RestartServiceAsync validates and initiates a service restart in the background.
// It returns the service with its current state immediately after setting the status to "stopping".
func (o *Orchestrator) RestartServiceAsync(id string) (*models.ServiceWithState, error) {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return nil, fmt.Errorf("service not found: %s", id)
	}

	state, err := models.GetServiceState(o.db, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get service state: %w", err)
	}
	if state != nil && state.Status == models.StatusDisabled {
		return nil, fmt.Errorf("service is disabled: %s", id)
	}

	// Update status to stopping (first phase of restart)
	if err := models.UpdateServiceStatus(o.db, id, models.StatusStopping); err != nil {
		return nil, fmt.Errorf("failed to update status: %w", err)
	}
	models.LogEvent(o.db, id, "restarting", "Service restart initiated")

	// Build the response before launching the goroutine to avoid races
	result := &models.ServiceWithState{Service: *service}
	if state != nil {
		result.State = *state
	}
	result.State.Status = models.StatusStopping

	// Run the actual restart in the background
	go func() {
		ctx := context.Background()
		if err := o.StopService(ctx, id); err != nil {
			logger.Warn("Error stopping service %s during restart: %v", id, err)
		}
		time.Sleep(1 * time.Second)
		if err := o.StartService(ctx, id); err != nil {
			logger.Error("Failed to start service %s during restart: %v", id, err)
		}
	}()

	return result, nil
}

// GetServiceStatus returns the current status of a service by querying the executor.
func (o *Orchestrator) GetServiceStatus(ctx context.Context, id string) (models.ServiceStatus, error) {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return "", fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return "", fmt.Errorf("service not found: %s", id)
	}

	exec := o.getExecutor(service.Type)
	if exec == nil {
		return "", fmt.Errorf("unknown service type: %s", service.Type)
	}

	return exec.Status(ctx, service)
}

// GetServiceStats returns statistics for a service.
func (o *Orchestrator) GetServiceStats(ctx context.Context, id string) (*ServiceStats, error) {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return nil, fmt.Errorf("service not found: %s", id)
	}

	state, err := models.GetServiceState(o.db, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get state: %w", err)
	}

	stats := &ServiceStats{
		ServiceID:    id,
		Status:       state.Status,
		RestartCount: state.RestartCount,
		LastStarted:  state.StartedAt,
	}

	// Calculate uptime if running
	if state.Status == models.StatusRunning && state.StartedAt != nil {
		stats.UptimeSeconds = int64(time.Since(*state.StartedAt).Seconds())
	}

	// Get container stats for compose services
	if service.Type == models.ServiceTypeCompose {
		containers, err := o.composeExecutor.GetContainerStats(ctx, service)
		if err != nil {
			logger.Debug("Failed to get container stats for %s: %v", id, err)
		} else {
			stats.Containers = containers
		}
	}

	return stats, nil
}

// GetServiceLogs returns recent logs for a service.
func (o *Orchestrator) GetServiceLogs(ctx context.Context, id string, lines int) (string, error) {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return "", fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return "", fmt.Errorf("service not found: %s", id)
	}

	if lines <= 0 {
		lines = 100
	}

	switch service.Type {
	case models.ServiceTypeCompose:
		return o.composeExecutor.GetLogs(ctx, service, lines)
	case models.ServiceTypeBinary:
		return o.binaryExecutor.GetLogs(ctx, service, lines)
	default:
		return "", fmt.Errorf("unknown service type: %s", service.Type)
	}
}

// StreamServiceLogs streams logs for a service, blocking until ctx is cancelled.
func (o *Orchestrator) StreamServiceLogs(ctx context.Context, id string, lines int, onLine func(line string)) error {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return fmt.Errorf("service not found: %s", id)
	}

	switch service.Type {
	case models.ServiceTypeCompose:
		return o.composeExecutor.FollowLogs(ctx, service, lines, onLine)
	case models.ServiceTypeBinary:
		return o.binaryExecutor.FollowLogs(ctx, service, lines, onLine)
	default:
		return fmt.Errorf("unknown service type: %s", service.Type)
	}
}

// StartHealthChecker starts the background health check loop.
func (o *Orchestrator) StartHealthChecker(ctx context.Context) {
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		ticker := time.NewTicker(o.config.HealthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-o.stopChan:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				o.runHealthChecks(ctx)
			}
		}
	}()
}

// runHealthChecks checks all running services and handles failures.
func (o *Orchestrator) runHealthChecks(ctx context.Context) {
	services, err := models.ListServices(o.db)
	if err != nil {
		logger.Error("Failed to list services for health check: %v", err)
		return
	}

	for _, service := range services {
		state, err := models.GetServiceState(o.db, service.ID)
		if err != nil {
			continue
		}

		// Only check services that should be running
		if state.Status != models.StatusRunning {
			continue
		}

		// Get actual status from executor
		exec := o.getExecutor(service.Type)
		if exec == nil {
			continue
		}

		actualStatus, err := exec.Status(ctx, &service)
		if err != nil {
			logger.Debug("Failed to get status for %s: %v", service.ID, err)
			continue
		}

		// Handle status mismatch
		if actualStatus != models.StatusRunning {
			logger.Warn("Service %s expected running but is %s", service.ID, actualStatus)

			// Update database to reflect actual status
			if actualStatus == models.StatusStopped || actualStatus == models.StatusFailed {
				if err := models.SetServiceFailed(o.db, service.ID, "service stopped unexpectedly"); err != nil {
					logger.Error("Failed to update status for %s: %v", service.ID, err)
				}
				models.LogEvent(o.db, service.ID, "failed", "Service stopped unexpectedly")

				// Attempt restart if enabled and under restart limit
				if service.Enabled && state.RestartCount < o.config.MaxRestarts {
					o.scheduleRestart(ctx, service.ID)
				}
			}
		}
	}
}

// scheduleRestart schedules a delayed restart for a service.
func (o *Orchestrator) scheduleRestart(ctx context.Context, id string) {
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()

		logger.Info("Scheduling restart for %s in %s", id, o.config.RestartDelay)

		select {
		case <-time.After(o.config.RestartDelay):
			// Check if we should still restart
			state, err := models.GetServiceState(o.db, id)
			if err != nil || state == nil {
				return
			}
			if state.Status == models.StatusRunning || state.Status == models.StatusDisabled {
				return
			}

			// Increment restart count
			if err := models.IncrementRestartCount(o.db, id); err != nil {
				logger.Error("Failed to increment restart count for %s: %v", id, err)
			}

			logger.Info("Restarting service %s (attempt %d)", id, state.RestartCount+1)
			models.LogEvent(o.db, id, "restarting", fmt.Sprintf("Automatic restart attempt %d", state.RestartCount+1))

			if err := o.StartService(ctx, id); err != nil {
				logger.Error("Failed to restart %s: %v", id, err)
			}

		case <-o.stopChan:
			return
		case <-ctx.Done():
			return
		}
	}()
}

// waitForHealthy waits for a service to become healthy.
func (o *Orchestrator) waitForHealthy(ctx context.Context, service *models.Service) error {
	exec := o.getExecutor(service.Type)
	if exec == nil {
		return fmt.Errorf("unknown service type: %s", service.Type)
	}

	timeout := time.After(2 * time.Minute) // TODO: make configurable
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for %s to become healthy", service.ID)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			status, err := exec.Status(ctx, service)
			if err != nil {
				logger.Debug("Error checking status of %s: %v", service.ID, err)
				continue
			}
			if status == models.StatusRunning {
				logger.Debug("Service %s is healthy", service.ID)
				return nil
			}
			if status == models.StatusFailed {
				return fmt.Errorf("service %s failed", service.ID)
			}
		}
	}
}

// getExecutor returns the appropriate executor for a service type.
func (o *Orchestrator) getExecutor(serviceType models.ServiceType) executor.Executor {
	switch serviceType {
	case models.ServiceTypeCompose:
		return o.composeExecutor
	case models.ServiceTypeBinary:
		return o.binaryExecutor
	default:
		return nil
	}
}

// GetStats returns orchestrator-level statistics.
func (o *Orchestrator) GetStats(ctx context.Context) (*OrchestratorStats, error) {
	services, err := models.ListServicesWithState(o.db)
	if err != nil {
		return nil, err
	}

	stats := &OrchestratorStats{
		Uptime:        int64(time.Since(o.startedAt).Seconds()),
		ServicesTotal: len(services),
	}

	for _, s := range services {
		switch s.State.Status {
		case models.StatusRunning:
			stats.ServicesRunning++
		case models.StatusFailed:
			stats.ServicesFailed++
		case models.StatusStopped:
			stats.ServicesStopped++
		}
	}

	return stats, nil
}

// ServiceStats holds statistics for a single service.
type ServiceStats struct {
	ServiceID     string                    `json:"service_id"`
	Status        models.ServiceStatus      `json:"status"`
	UptimeSeconds int64                     `json:"uptime_seconds"`
	LastStarted   *time.Time                `json:"last_started,omitempty"`
	RestartCount  int                       `json:"restart_count"`
	Containers    []executor.ContainerStats `json:"containers,omitempty"`
}

// OrchestratorStats holds orchestrator-level statistics.
type OrchestratorStats struct {
	Uptime          int64 `json:"uptime_seconds"`
	ServicesTotal   int   `json:"services_total"`
	ServicesRunning int   `json:"services_running"`
	ServicesStopped int   `json:"services_stopped"`
	ServicesFailed  int   `json:"services_failed"`
}
