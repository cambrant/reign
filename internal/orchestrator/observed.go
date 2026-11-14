package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/reign/internal/models"
)

// Observation is the status an executor last reported for a service, as
// opposed to the status reign intends (service_state.status).
type Observation struct {
	Status    models.ServiceStatus `json:"status"`
	CheckedAt time.Time            `json:"checked_at"`
}

// probeStatus asks the executor for a service's status and remembers the
// answer, so API and CLI requests can be served from the health loop's last
// result instead of probing docker or systemd again.
func (o *Orchestrator) probeStatus(ctx context.Context, service *models.Service) (models.ServiceStatus, error) {
	exec := o.getExecutor(service.Type)
	if exec == nil {
		return "", fmt.Errorf("unknown service type: %s", service.Type)
	}
	status, err := exec.Status(ctx, service)
	if err != nil {
		return "", err
	}
	o.obsMu.Lock()
	o.observed[service.ID] = Observation{Status: status, CheckedAt: time.Now()}
	o.obsMu.Unlock()
	return status, nil
}

// Observed returns the last observed status of a service, if any.
func (o *Orchestrator) Observed(id string) (Observation, bool) {
	o.obsMu.Lock()
	defer o.obsMu.Unlock()
	obs, ok := o.observed[id]
	return obs, ok
}

// GetServiceStatus returns a service's observed status. It is served from the
// last check unless fresh is set or no check has run yet; then the executor
// is asked live.
func (o *Orchestrator) GetServiceStatus(ctx context.Context, id string, fresh bool) (Observation, error) {
	service, err := models.GetServiceByID(o.db, id)
	if err != nil {
		return Observation{}, fmt.Errorf("failed to get service: %w", err)
	}
	if service == nil {
		return Observation{}, fmt.Errorf("service not found: %s", id)
	}
	if !fresh {
		if obs, ok := o.Observed(id); ok {
			return obs, nil
		}
	}
	if _, err := o.probeStatus(ctx, service); err != nil {
		return Observation{}, err
	}
	obs, _ := o.Observed(id)
	return obs, nil
}
