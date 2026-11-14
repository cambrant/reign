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

	// pendingRestarts holds services with an automatic restart already scheduled,
	// so repeated health checks don't stack up restarts for the same service.
	pendingMu       sync.Mutex
	pendingRestarts map[string]bool
	// externalStops holds services stopped outside reign, with the time it was
	// noticed. They are restarted once ExternalStopGrace has passed, unless
	// they come back on their own or are started or stopped through reign.
	externalStops map[string]time.Time
}

const (
	// maxRestartBackoff caps the exponential delay between automatic restarts.
	maxRestartBackoff = 15 * time.Minute
	// stableRunTime is how long a service must stay up before its restart count resets.
	stableRunTime = 10 * time.Minute
)

// New creates a new Orchestrator.
func New(db *sql.DB, cfg *config.Config) *Orchestrator {
	return &Orchestrator{
		db:              db,
		config:          cfg,
		composeExecutor: newComposeExecutor(cfg),
		binaryExecutor: executor.NewBinaryExecutorWithOptions(executor.BinaryOptions{
			Mode:        executor.ResolveBinaryMode(cfg.BinaryRunner),
			EnvDir:      cfg.RuntimeDir,
			StopTimeout: cfg.ShutdownTimeout,
		}),
		startedAt:       time.Now(),
		stopChan:        make(chan struct{}),
		pendingRestarts: make(map[string]bool),
		externalStops:   make(map[string]time.Time),
	}
}

// newComposeExecutor uses the Docker API for status checks when a socket is configured.
func newComposeExecutor(cfg *config.Config) *executor.ComposeExecutor {
	if cfg.DockerSocket == "" {
		return &executor.ComposeExecutor{}
	}
	return executor.NewComposeExecutorWithDocker(executor.NewDockerClient(cfg.DockerSocket))
}

// StartupServices starts all enabled services in the correct order.
// If syncFirst is true, it first probes actual service states and only starts services that aren't running.
func (o *Orchestrator) StartupServices(ctx context.Context, syncFirst bool) error {
	ctx = WithRequester(ctx, RequesterStartup)
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
	ctx = WithRequester(ctx, RequesterShutdown)
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

// StartService starts a specific service. It counts as a manual start and
// resets the service's automatic restart count.
func (o *Orchestrator) StartService(ctx context.Context, id string) error {
	return o.startService(ctx, id, false)
}

// startService starts a service. Automatic restarts (auto=true) keep the restart
// count, so MaxRestarts can take effect; manual starts reset it.
func (o *Orchestrator) startService(ctx context.Context, id string, auto bool) error {
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

	logger.Info("Starting service: %s%s", id, byRequester(ctx))
	o.clearExternalStop(id)

	// Update status to starting
	if err := models.UpdateServiceStatus(o.db, id, models.StatusStarting); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	o.logEvent(ctx, id, "starting", "Service start initiated")

	// Reset restart count on manual start
	if !auto {
		models.ResetRestartCount(o.db, id)
	}

	// Get executor
	exec := o.getExecutor(service.Type)
	if exec == nil {
		models.SetServiceFailed(o.db, id, "unknown service type")
		return fmt.Errorf("unknown service type: %s", service.Type)
	}

	// Images are not pulled here: compose's pull policy handles missing images,
	// and upgrades are explicit (reign pull / reign restart --pull).
	// Start the service
	if err := exec.Start(ctx, service); err != nil {
		models.SetServiceFailed(o.db, id, err.Error())
		o.logEvent(ctx, id, "failed", err.Error())
		return fmt.Errorf("failed to start service: %w", err)
	}

	// Get PID for binary services
	var pid *int
	if service.Type == models.ServiceTypeBinary {
		pid = o.binaryExecutor.GetPID(ctx, service)
	}

	// Update status to running
	if err := models.SetServiceStarted(o.db, id, pid); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	o.logEvent(ctx, id, "started", "Service started successfully")
	logger.Info("Service started: %s", id)

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

	logger.Info("Stopping service: %s%s", id, byRequester(ctx))
	o.clearExternalStop(id)

	// Update status to stopping
	if err := models.UpdateServiceStatus(o.db, id, models.StatusStopping); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	o.logEvent(ctx, id, "stopping", "Service stop initiated")

	// Get executor
	exec := o.getExecutor(service.Type)
	if exec == nil {
		return fmt.Errorf("unknown service type: %s", service.Type)
	}

	// Stop the service
	if err := exec.Stop(ctx, service); err != nil {
		o.logEvent(ctx, id, "stop_failed", err.Error())
		return fmt.Errorf("failed to stop service: %w", err)
	}

	// Update status to stopped
	if err := models.SetServiceStopped(o.db, id); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	o.logEvent(ctx, id, "stopped", "Service stopped successfully")

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
// ctx only supplies the requester (see WithRequester); the start itself outlives it.
func (o *Orchestrator) StartServiceAsync(ctx context.Context, id string) (*models.ServiceWithState, error) {
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
	o.logEvent(ctx, id, "starting", "Service start initiated")
	logger.Info("Starting service: %s%s", id, byRequester(ctx))
	o.clearExternalStop(id)
	models.ResetRestartCount(o.db, id)

	// Build the response before launching the goroutine to avoid races
	result := &models.ServiceWithState{Service: *service}
	if state != nil {
		result.State = *state
	}
	result.State.Status = models.StatusStarting

	// Run the actual start in the background
	go func() {
		ctx := context.WithoutCancel(ctx)
		exec := o.getExecutor(service.Type)
		if exec == nil {
			models.SetServiceFailed(o.db, id, "unknown service type")
			return
		}

		if err := exec.Start(ctx, service); err != nil {
			models.SetServiceFailed(o.db, id, err.Error())
			o.logEvent(ctx, id, "failed", err.Error())
			logger.Error("Failed to start service %s: %v", id, err)
			return
		}

		var pid *int
		if service.Type == models.ServiceTypeBinary {
			pid = o.binaryExecutor.GetPID(ctx, service)
		}

		if err := models.SetServiceStarted(o.db, id, pid); err != nil {
			logger.Error("Failed to update status for %s: %v", id, err)
			return
		}
		o.logEvent(ctx, id, "started", "Service started successfully")
	}()

	return result, nil
}

// StopServiceAsync validates and initiates a service stop in the background.
// It returns the service with its current state immediately after setting the status to "stopping".
// ctx only supplies the requester (see WithRequester); the stop itself outlives it.
func (o *Orchestrator) StopServiceAsync(ctx context.Context, id string) (*models.ServiceWithState, error) {
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
	o.logEvent(ctx, id, "stopping", "Service stop initiated")
	logger.Info("Stopping service: %s%s", id, byRequester(ctx))
	o.clearExternalStop(id)

	// Build the response before launching the goroutine to avoid races
	result := &models.ServiceWithState{Service: *service}
	if state != nil {
		result.State = *state
	}
	result.State.Status = models.StatusStopping

	// Run the actual stop in the background
	go func() {
		ctx := context.WithoutCancel(ctx)
		exec := o.getExecutor(service.Type)
		if exec == nil {
			logger.Error("Unknown service type for %s: %s", id, service.Type)
			return
		}

		if err := exec.Stop(ctx, service); err != nil {
			o.logEvent(ctx, id, "stop_failed", err.Error())
			logger.Error("Failed to stop service %s: %v", id, err)
			return
		}

		if err := models.SetServiceStopped(o.db, id); err != nil {
			logger.Error("Failed to update status for %s: %v", id, err)
			return
		}
		o.logEvent(ctx, id, "stopped", "Service stopped successfully")
	}()

	return result, nil
}

// RestartServiceAsync validates and initiates a service restart in the background.
// With pull set, images are pulled first, while the service keeps running; a
// failed pull aborts the restart. It returns the service with its current state.
// ctx only supplies the requester (see WithRequester); the restart itself outlives it.
func (o *Orchestrator) RestartServiceAsync(ctx context.Context, id string, pull bool) (*models.ServiceWithState, error) {
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
	if pull && service.Type != models.ServiceTypeCompose {
		return nil, fmt.Errorf("pull is only supported for compose services: %s", id)
	}

	// Build the response before launching the goroutine to avoid races
	result := &models.ServiceWithState{Service: *service}
	if state != nil {
		result.State = *state
	}

	if pull {
		// Keep the current status while pulling: the service is still up.
		o.logEvent(ctx, id, "restarting", "Service restart with image pull initiated")
	} else {
		// Update status to stopping (first phase of restart)
		if err := models.UpdateServiceStatus(o.db, id, models.StatusStopping); err != nil {
			return nil, fmt.Errorf("failed to update status: %w", err)
		}
		o.logEvent(ctx, id, "restarting", "Service restart initiated")
		result.State.Status = models.StatusStopping
	}
	logger.Info("Restarting service: %s%s", id, byRequester(ctx))

	// Run the actual restart in the background
	go func() {
		ctx := context.WithoutCancel(ctx)
		if pull {
			if err := o.pullImages(ctx, service); err != nil {
				logger.Error("Not restarting %s: %v", id, err)
				return
			}
		}
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

// PullService pulls the images of a compose service without restarting it.
func (o *Orchestrator) PullService(ctx context.Context, id string) error {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return fmt.Errorf("service not found: %s", id)
	}
	if service.Type != models.ServiceTypeCompose {
		return fmt.Errorf("pull is only supported for compose services: %s", id)
	}
	return o.pullImages(ctx, service)
}

// pullImages pulls a compose service's images and records the outcome as an event.
func (o *Orchestrator) pullImages(ctx context.Context, service *models.Service) error {
	logger.Info("Pulling images for %s", service.ID)
	if err := o.composeExecutor.Pull(ctx, service); err != nil {
		o.logEvent(ctx, service.ID, "pull_failed", err.Error())
		return fmt.Errorf("failed to pull images: %w", err)
	}
	o.logEvent(ctx, service.ID, "pulled", "Images pulled successfully")
	return nil
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
	ctx = WithRequester(ctx, RequesterHealthCheck)
	services, err := models.ListServices(o.db)
	if err != nil {
		logger.Error("Failed to list services for health check: %v", err)
		return
	}

	for _, service := range services {
		state, err := models.GetServiceState(o.db, service.ID)
		if err != nil || state == nil {
			continue
		}

		switch state.Status {
		case models.StatusRunning:
			o.checkRunningService(ctx, service, state)
		case models.StatusStopped:
			o.checkExternallyStopped(ctx, service, state)
		case models.StatusFailed:
			// A failed start (e.g. a transient docker error) is retried too,
			// not only services that die while running.
			o.maybeScheduleRestart(ctx, service, state)
		}
	}
}

// checkRunningService compares a service that should be running with reality.
func (o *Orchestrator) checkRunningService(ctx context.Context, service models.Service, state *models.ServiceState) {
	exec := o.getExecutor(service.Type)
	if exec == nil {
		return
	}

	actualStatus, err := exec.Status(ctx, &service)
	if err != nil {
		// Unknown state (e.g. docker daemon restarting): don't act on it.
		logger.Debug("Failed to get status for %s: %v", service.ID, err)
		return
	}

	switch actualStatus {
	case models.StatusRunning:
		// Forget old failures once the service has been stable for a while.
		if state.RestartCount > 0 && state.StartedAt != nil && time.Since(*state.StartedAt) > stableRunTime {
			if err := models.ResetRestartCount(o.db, service.ID); err != nil {
				logger.Error("Failed to reset restart count for %s: %v", service.ID, err)
			}
		}
	case models.StatusStopped, models.StatusFailed:
		logger.Warn("Service %s expected running but is %s", service.ID, actualStatus)
		cause := o.stopCause(ctx, &service, state)
		if cause.External {
			o.handleExternalStop(ctx, service, cause)
			return
		}
		msg := "Service stopped unexpectedly: " + cause.Detail
		if err := models.SetServiceFailed(o.db, service.ID, msg); err != nil {
			logger.Error("Failed to update status for %s: %v", service.ID, err)
		}
		o.logEvent(ctx, service.ID, "failed", msg)
		o.maybeScheduleRestart(ctx, service, state)
	default:
		logger.Warn("Service %s expected running but is %s", service.ID, actualStatus)
	}
}

// stopCause asks the executor why a service is down. When that cannot be
// determined, the stop is treated as a crash, which is the safe default.
func (o *Orchestrator) stopCause(ctx context.Context, service *models.Service, state *models.ServiceState) executor.StopCause {
	causer, ok := o.getExecutor(service.Type).(executor.StopCauser)
	if !ok {
		return executor.StopCause{Detail: "stopped unexpectedly"}
	}
	since := time.Now().Add(-time.Hour)
	if state.StartedAt != nil {
		since = *state.StartedAt
	}
	cause, err := causer.StopCause(ctx, service, since)
	if err != nil {
		logger.Debug("Could not determine why %s stopped: %v", service.ID, err)
		return executor.StopCause{Detail: "stopped unexpectedly"}
	}
	return cause
}

// handleExternalStop records a stop made outside reign. The service is marked
// stopped and, unless ExternalStopGrace is 0, restarted once the grace period
// has passed without it coming back (e.g. after a cold backup).
func (o *Orchestrator) handleExternalStop(ctx context.Context, service models.Service, cause executor.StopCause) {
	grace := o.config.ExternalStopGrace
	msg := "Service " + cause.Detail
	if grace > 0 && service.Enabled {
		msg += fmt.Sprintf("; restarting in %s unless it is started again", grace)
	} else {
		msg += "; not restarting automatically"
	}
	logger.Warn("%s: %s", service.ID, msg)

	if err := models.SetServiceStoppedExternally(o.db, service.ID, cause.Detail); err != nil {
		logger.Error("Failed to update status for %s: %v", service.ID, err)
	}
	o.logEvent(ctx, service.ID, "stopped_externally", msg)

	if grace > 0 && service.Enabled {
		o.pendingMu.Lock()
		o.externalStops[service.ID] = time.Now()
		o.pendingMu.Unlock()
	}
}

// checkExternallyStopped follows a service that was stopped outside reign:
// it is marked running again if it came back, and restarted once the grace
// period is over.
func (o *Orchestrator) checkExternallyStopped(ctx context.Context, service models.Service, state *models.ServiceState) {
	o.pendingMu.Lock()
	since, watched := o.externalStops[service.ID]
	o.pendingMu.Unlock()
	if !watched {
		return
	}

	exec := o.getExecutor(service.Type)
	if exec == nil {
		return
	}
	actual, err := exec.Status(ctx, &service)
	if err != nil {
		logger.Debug("Failed to get status for %s: %v", service.ID, err)
		return
	}

	if actual == models.StatusRunning {
		o.clearExternalStop(service.ID)
		var pid *int
		if service.Type == models.ServiceTypeBinary {
			pid = o.binaryExecutor.GetPID(ctx, &service)
		}
		if err := models.SetServiceStarted(o.db, service.ID, pid); err != nil {
			logger.Error("Failed to update status for %s: %v", service.ID, err)
		}
		o.logEvent(ctx, service.ID, "resumed", "Service is running again after being stopped outside reign")
		return
	}

	if time.Since(since) < o.config.ExternalStopGrace {
		return
	}
	o.clearExternalStop(service.ID)
	msg := fmt.Sprintf("Service still down %s after being stopped outside reign", o.config.ExternalStopGrace)
	logger.Warn("%s: %s", service.ID, msg)
	if err := models.SetServiceFailed(o.db, service.ID, msg); err != nil {
		logger.Error("Failed to update status for %s: %v", service.ID, err)
	}
	o.logEvent(ctx, service.ID, "failed", msg)
	o.maybeScheduleRestart(ctx, service, state)
}

// clearExternalStop forgets a service's external stop, e.g. when it is
// started or stopped through reign.
func (o *Orchestrator) clearExternalStop(id string) {
	o.pendingMu.Lock()
	delete(o.externalStops, id)
	o.pendingMu.Unlock()
}

// maybeScheduleRestart schedules an automatic restart if the service is enabled,
// under its restart limit and doesn't already have one pending.
func (o *Orchestrator) maybeScheduleRestart(ctx context.Context, service models.Service, state *models.ServiceState) {
	if !service.Enabled {
		return
	}
	if state.RestartCount >= o.config.MaxRestarts {
		logger.Debug("Service %s reached max restarts (%d), not restarting", service.ID, o.config.MaxRestarts)
		return
	}

	o.pendingMu.Lock()
	if o.pendingRestarts[service.ID] {
		o.pendingMu.Unlock()
		return
	}
	o.pendingRestarts[service.ID] = true
	o.pendingMu.Unlock()

	o.scheduleRestart(ctx, service.ID, restartBackoff(o.config.RestartDelay, state.RestartCount))
}

// restartBackoff returns base * 2^attempts, capped at maxRestartBackoff.
func restartBackoff(base time.Duration, attempts int) time.Duration {
	d := base
	for i := 0; i < attempts && d < maxRestartBackoff; i++ {
		d *= 2
	}
	if d > maxRestartBackoff {
		d = maxRestartBackoff
	}
	return d
}

// scheduleRestart schedules a delayed restart for a service.
func (o *Orchestrator) scheduleRestart(ctx context.Context, id string, delay time.Duration) {
	ctx = WithRequester(ctx, RequesterAutoRestart)
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer func() {
			o.pendingMu.Lock()
			delete(o.pendingRestarts, id)
			o.pendingMu.Unlock()
		}()

		logger.Info("Scheduling restart for %s in %s", id, delay)

		select {
		case <-time.After(delay):
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
			o.logEvent(ctx, id, "restarting", fmt.Sprintf("Automatic restart attempt %d", state.RestartCount+1))

			if err := o.startService(ctx, id, true); err != nil {
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

// logEvent records an event attributed to the requester carried by ctx.
func (o *Orchestrator) logEvent(ctx context.Context, id, eventType, message string) {
	if err := models.LogEventBy(o.db, id, eventType, message, RequesterFrom(ctx)); err != nil {
		logger.Error("Failed to log %s event for %s: %v", eventType, id, err)
	}
}

// byRequester formats the requester for log lines, e.g. " (requested by tim@kraut)".
func byRequester(ctx context.Context) string {
	if r := RequesterFrom(ctx); r != "" {
		return " (requested by " + r + ")"
	}
	return ""
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

// BinaryMode returns how binary services are run (systemd or direct).
func (o *Orchestrator) BinaryMode() string {
	return o.binaryExecutor.Mode()
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
