package orchestrator

import (
	"context"
	"time"

	"github.com/reign/internal/executor"
	"github.com/reign/internal/logger"
	"github.com/reign/internal/models"
)

const (
	// eventDebounce lets a burst of events (kill, die, stop for every
	// container of a project) settle into one check.
	eventDebounce = 2 * time.Second
	// maxEventBackoff caps the delay between reconnects to the event stream.
	maxEventBackoff = 30 * time.Second
)

// StartEventWatcher subscribes to Docker container events and checks the
// affected service right away, instead of waiting for the next health tick.
// The periodic health check keeps running as a safety net for missed events
// and daemon restarts. It does nothing when the Docker API is not configured.
func (o *Orchestrator) StartEventWatcher(ctx context.Context) {
	docker := o.composeExecutor.Docker()
	if docker == nil {
		return
	}
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-o.stopChan:
				cancel()
			case <-ctx.Done():
			}
		}()

		backoff := time.Second
		for {
			start := time.Now()
			err := docker.Events(ctx, func(ev executor.ContainerEvent) {
				o.handleContainerEvent(ctx, ev)
			})
			if ctx.Err() != nil {
				return
			}
			if time.Since(start) > time.Minute {
				backoff = time.Second // the stream was healthy for a while
			}
			logger.Warn("Docker event stream ended, reconnecting in %s: %v", backoff, err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = min(backoff*2, maxEventBackoff)
		}
	}()
}

// handleContainerEvent schedules a debounced check of the service whose
// compose project the event belongs to.
func (o *Orchestrator) handleContainerEvent(ctx context.Context, ev executor.ContainerEvent) {
	logger.Debug("Docker event: %s %s (project %s)", ev.Action, ev.Name, ev.Project)
	o.composeExecutor.InvalidateContainers()

	service := o.serviceForProject(ctx, ev.Project)
	if service == nil {
		return
	}

	o.eventMu.Lock()
	defer o.eventMu.Unlock()
	if _, pending := o.eventChecks[service.ID]; pending {
		return
	}
	o.wg.Add(1) // shutdown waits for pending checks
	o.eventChecks[service.ID] = time.AfterFunc(o.eventDebounce, func() {
		defer o.wg.Done()
		o.eventMu.Lock()
		delete(o.eventChecks, service.ID)
		o.eventMu.Unlock()
		select {
		case <-o.stopChan:
			return
		default:
		}
		o.checkService(WithRequester(ctx, RequesterHealthCheck), *service)
	})
}

// serviceForProject finds the compose service whose project name is project.
func (o *Orchestrator) serviceForProject(ctx context.Context, project string) *models.Service {
	if project == "" {
		return nil
	}
	services, err := models.ListServices(o.db)
	if err != nil {
		logger.Error("Failed to list services for event: %v", err)
		return nil
	}
	for _, s := range services {
		if s.Type != models.ServiceTypeCompose {
			continue
		}
		name, err := o.composeExecutor.ProjectName(ctx, &s)
		if err == nil && name == project {
			return &s
		}
	}
	return nil
}
