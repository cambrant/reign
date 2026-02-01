// Package executor provides service execution implementations.
package executor

import (
	"context"

	"github.com/reign/internal/models"
)

// Executor defines the interface for executing and managing services.
type Executor interface {
	// Start starts the service.
	Start(ctx context.Context, service *models.Service) error

	// Stop stops the service.
	Stop(ctx context.Context, service *models.Service) error

	// Status returns the current status of the service.
	Status(ctx context.Context, service *models.Service) (models.ServiceStatus, error)

	// Pull pulls the latest images (for compose) or is a no-op (for binary).
	Pull(ctx context.Context, service *models.Service) error
}

// NewExecutor returns the appropriate executor for the service type.
func NewExecutor(serviceType models.ServiceType) Executor {
	switch serviceType {
	case models.ServiceTypeCompose:
		return &ComposeExecutor{}
	case models.ServiceTypeBinary:
		return &BinaryExecutor{}
	default:
		return nil
	}
}
