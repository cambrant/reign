package models

import (
	"database/sql"
	"fmt"
	"time"
)

// ServiceType represents the type of service.
type ServiceType string

const (
	ServiceTypeCompose ServiceType = "compose"
	ServiceTypeBinary  ServiceType = "binary"
)

// ServiceStatus represents the current status of a service.
type ServiceStatus string

const (
	StatusStopped  ServiceStatus = "stopped"
	StatusStarting ServiceStatus = "starting"
	StatusRunning  ServiceStatus = "running"
	StatusStopping ServiceStatus = "stopping"
	StatusFailed   ServiceStatus = "failed"
	StatusDisabled ServiceStatus = "disabled"
)

// Service represents a managed service.
type Service struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Type           ServiceType `json:"type"`
	Path           string      `json:"path"`
	Command        string      `json:"command,omitempty"`
	Enabled        bool        `json:"enabled"`
	Infrastructure bool        `json:"infrastructure"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// ServiceState represents the runtime state of a service.
type ServiceState struct {
	ServiceID    string        `json:"service_id"`
	Status       ServiceStatus `json:"status"`
	PID          *int          `json:"pid,omitempty"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
	StoppedAt    *time.Time    `json:"stopped_at,omitempty"`
	RestartCount int           `json:"restart_count"`
	LastError    *string       `json:"last_error,omitempty"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// ServiceWithState combines service definition with its runtime state.
type ServiceWithState struct {
	Service
	State ServiceState `json:"state"`
}

// ServiceEvent represents a lifecycle event for a service.
type ServiceEvent struct {
	ID        int       `json:"id"`
	ServiceID string    `json:"service_id"`
	EventType string    `json:"event_type"`
	Message   string    `json:"message,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Create inserts a new service into the database.
func (s *Service) Create(db *sql.DB) error {
	query := `
		INSERT INTO services (id, name, type, path, command, enabled, infrastructure, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
	`
	_, err := db.Exec(query, s.ID, s.Name, s.Type, s.Path, s.Command, s.Enabled, s.Infrastructure)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	// Create initial state record
	stateQuery := `
		INSERT INTO service_state (service_id, status, updated_at)
		VALUES (?, ?, datetime('now'))
	`
	status := StatusStopped
	if !s.Enabled {
		status = StatusDisabled
	}
	_, err = db.Exec(stateQuery, s.ID, status)
	if err != nil {
		return fmt.Errorf("failed to create service state: %w", err)
	}

	return nil
}

// Update updates an existing service in the database.
func (s *Service) Update(db *sql.DB) error {
	query := `
		UPDATE services
		SET name = ?, type = ?, path = ?, enabled = ?, infrastructure = ?, updated_at = datetime('now')
		WHERE id = ?
	`
	result, err := db.Exec(query, s.Name, s.Type, s.Path, s.Enabled, s.Infrastructure, s.ID)
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("service not found: %s", s.ID)
	}

	return nil
}

// GetServiceByID retrieves a service by its ID.
func GetServiceByID(db *sql.DB, id string) (*Service, error) {
	query := `
		SELECT id, name, type, path, command, enabled, infrastructure, created_at, updated_at
		FROM services
		WHERE id = ?
	`
	var s Service
	var createdAt, updatedAt string
	var command sql.NullString
	err := db.QueryRow(query, id).Scan(
		&s.ID, &s.Name, &s.Type, &s.Path, &command, &s.Enabled, &s.Infrastructure, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %w", err)
	}

	s.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	s.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	if command.Valid {
		s.Command = command.String
	}

	return &s, nil
}

// ListServices retrieves all services.
func ListServices(db *sql.DB) ([]Service, error) {
	query := `
		SELECT id, name, type, path, command, enabled, infrastructure, created_at, updated_at
		FROM services
		ORDER BY id
	`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}
	defer rows.Close()

	var services []Service
	for rows.Next() {
		var s Service
		var createdAt, updatedAt string
		var command sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.Type, &s.Path, &command, &s.Enabled, &s.Infrastructure, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan service: %w", err)
		}
		s.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		s.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		if command.Valid {
			s.Command = command.String
		}
		services = append(services, s)
	}

	return services, rows.Err()
}

// ListEnabledServicesForStartup retrieves enabled services ordered for startup.
// Infrastructure services come first (alphabetically), then regular services (alphabetically).
func ListEnabledServicesForStartup(db *sql.DB) ([]Service, error) {
	query := `
		SELECT id, name, type, path, command, enabled, infrastructure, created_at, updated_at
		FROM services
		WHERE enabled = 1
		ORDER BY infrastructure DESC, id ASC
	`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list services for startup: %w", err)
	}
	defer rows.Close()

	var services []Service
	for rows.Next() {
		var s Service
		var createdAt, updatedAt string
		var command sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.Type, &s.Path, &command, &s.Enabled, &s.Infrastructure, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan service: %w", err)
		}
		s.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		s.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		if command.Valid {
			s.Command = command.String
		}
		services = append(services, s)
	}

	return services, rows.Err()
}

// DeleteService removes a service from the database.
func DeleteService(db *sql.DB, id string) error {
	result, err := db.Exec("DELETE FROM services WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("service not found: %s", id)
	}

	return nil
}

// GetServiceState retrieves the current state of a service.
func GetServiceState(db *sql.DB, serviceID string) (*ServiceState, error) {
	query := `
		SELECT service_id, status, pid, started_at, stopped_at, restart_count, last_error, updated_at
		FROM service_state
		WHERE service_id = ?
	`
	var state ServiceState
	var startedAt, stoppedAt, lastError sql.NullString
	var pid sql.NullInt64
	var updatedAt string

	err := db.QueryRow(query, serviceID).Scan(
		&state.ServiceID, &state.Status, &pid, &startedAt, &stoppedAt,
		&state.RestartCount, &lastError, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get service state: %w", err)
	}

	if pid.Valid {
		p := int(pid.Int64)
		state.PID = &p
	}
	if startedAt.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", startedAt.String)
		state.StartedAt = &t
	}
	if stoppedAt.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", stoppedAt.String)
		state.StoppedAt = &t
	}
	if lastError.Valid {
		state.LastError = &lastError.String
	}
	state.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)

	return &state, nil
}

// UpdateServiceStatus updates the status of a service.
func UpdateServiceStatus(db *sql.DB, serviceID string, status ServiceStatus) error {
	query := `
		UPDATE service_state
		SET status = ?, updated_at = datetime('now')
		WHERE service_id = ?
	`
	_, err := db.Exec(query, status, serviceID)
	return err
}

// SetServiceStarted marks a service as started.
func SetServiceStarted(db *sql.DB, serviceID string, pid *int) error {
	query := `
		UPDATE service_state
		SET status = 'running', pid = ?, started_at = datetime('now'), stopped_at = NULL, updated_at = datetime('now')
		WHERE service_id = ?
	`
	_, err := db.Exec(query, pid, serviceID)
	return err
}

// SetServiceStopped marks a service as stopped.
func SetServiceStopped(db *sql.DB, serviceID string) error {
	query := `
		UPDATE service_state
		SET status = 'stopped', pid = NULL, stopped_at = datetime('now'), updated_at = datetime('now')
		WHERE service_id = ?
	`
	_, err := db.Exec(query, serviceID)
	return err
}

// SetServiceFailed marks a service as failed with an error message.
func SetServiceFailed(db *sql.DB, serviceID string, errMsg string) error {
	query := `
		UPDATE service_state
		SET status = 'failed', last_error = ?, updated_at = datetime('now')
		WHERE service_id = ?
	`
	_, err := db.Exec(query, errMsg, serviceID)
	return err
}

// IncrementRestartCount increments the restart count for a service.
func IncrementRestartCount(db *sql.DB, serviceID string) error {
	query := `
		UPDATE service_state
		SET restart_count = restart_count + 1, updated_at = datetime('now')
		WHERE service_id = ?
	`
	_, err := db.Exec(query, serviceID)
	return err
}

// ResetRestartCount resets the restart count for a service.
func ResetRestartCount(db *sql.DB, serviceID string) error {
	query := `
		UPDATE service_state
		SET restart_count = 0, last_error = NULL, updated_at = datetime('now')
		WHERE service_id = ?
	`
	_, err := db.Exec(query, serviceID)
	return err
}

// LogEvent records a service lifecycle event.
func LogEvent(db *sql.DB, serviceID, eventType, message string) error {
	query := `
		INSERT INTO service_events (service_id, event_type, message, created_at)
		VALUES (?, ?, ?, datetime('now'))
	`
	_, err := db.Exec(query, serviceID, eventType, message)
	return err
}

// GetServiceEvents retrieves recent events for a service.
func GetServiceEvents(db *sql.DB, serviceID string, limit int) ([]ServiceEvent, error) {
	query := `
		SELECT id, service_id, event_type, message, created_at
		FROM service_events
		WHERE service_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`
	rows, err := db.Query(query, serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get service events: %w", err)
	}
	defer rows.Close()

	var events []ServiceEvent
	for rows.Next() {
		var e ServiceEvent
		var message sql.NullString
		var createdAt string
		if err := rows.Scan(&e.ID, &e.ServiceID, &e.EventType, &message, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		if message.Valid {
			e.Message = message.String
		}
		e.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		events = append(events, e)
	}

	return events, rows.Err()
}

// GetRecentEvents retrieves recent events across all services.
func GetRecentEvents(db *sql.DB, limit int) ([]ServiceEvent, error) {
	query := `
		SELECT id, service_id, event_type, message, created_at
		FROM service_events
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`
	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent events: %w", err)
	}
	defer rows.Close()

	var events []ServiceEvent
	for rows.Next() {
		var e ServiceEvent
		var message sql.NullString
		var createdAt string
		if err := rows.Scan(&e.ID, &e.ServiceID, &e.EventType, &message, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		if message.Valid {
			e.Message = message.String
		}
		e.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		events = append(events, e)
	}

	return events, rows.Err()
}

// EventLogEntry represents an event with service context for the unified event log.
type EventLogEntry struct {
	ID          int       `json:"id"`
	ServiceID   string    `json:"service_id"`
	ServiceName string    `json:"service_name"`
	EventType   string    `json:"event_type"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// GetEventLog retrieves events across all services with service names, ordered by time.
func GetEventLog(db *sql.DB, limit int) ([]EventLogEntry, error) {
	query := `
		SELECT e.id, e.service_id, s.name, e.event_type, e.message, e.created_at
		FROM service_events e
		JOIN services s ON s.id = e.service_id
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT ?
	`
	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get event log: %w", err)
	}
	defer rows.Close()

	var entries []EventLogEntry
	for rows.Next() {
		var entry EventLogEntry
		var message sql.NullString
		var createdAt string
		if err := rows.Scan(&entry.ID, &entry.ServiceID, &entry.ServiceName, &entry.EventType, &message, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan event log entry: %w", err)
		}
		if message.Valid {
			entry.Message = message.String
		}
		entry.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

// GetEventLogSince retrieves events with id greater than sinceID, ordered oldest first.
func GetEventLogSince(db *sql.DB, sinceID int) ([]EventLogEntry, error) {
	query := `
		SELECT e.id, e.service_id, s.name, e.event_type, e.message, e.created_at
		FROM service_events e
		JOIN services s ON s.id = e.service_id
		WHERE e.id > ?
		ORDER BY e.id ASC
	`
	rows, err := db.Query(query, sinceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get event log: %w", err)
	}
	defer rows.Close()

	var entries []EventLogEntry
	for rows.Next() {
		var entry EventLogEntry
		var message sql.NullString
		var createdAt string
		if err := rows.Scan(&entry.ID, &entry.ServiceID, &entry.ServiceName, &entry.EventType, &message, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan event log entry: %w", err)
		}
		if message.Valid {
			entry.Message = message.String
		}
		entry.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

// GetMaxEventID returns the current maximum event ID, or 0 if no events exist.
func GetMaxEventID(db *sql.DB) (int, error) {
	var maxID sql.NullInt64
	err := db.QueryRow("SELECT MAX(id) FROM service_events").Scan(&maxID)
	if err != nil {
		return 0, err
	}
	if maxID.Valid {
		return int(maxID.Int64), nil
	}
	return 0, nil
}

// GetServiceWithState retrieves a service with its current state.
func GetServiceWithState(db *sql.DB, id string) (*ServiceWithState, error) {
	service, err := GetServiceByID(db, id)
	if err != nil {
		return nil, err
	}
	if service == nil {
		return nil, nil
	}

	state, err := GetServiceState(db, id)
	if err != nil {
		return nil, err
	}

	result := &ServiceWithState{
		Service: *service,
	}
	if state != nil {
		result.State = *state
	}

	return result, nil
}

// ListServicesWithState retrieves all services with their current state.
func ListServicesWithState(db *sql.DB) ([]ServiceWithState, error) {
	services, err := ListServices(db)
	if err != nil {
		return nil, err
	}

	var result []ServiceWithState
	for _, s := range services {
		state, err := GetServiceState(db, s.ID)
		if err != nil {
			return nil, err
		}
		sws := ServiceWithState{Service: s}
		if state != nil {
			sws.State = *state
		}
		result = append(result, sws)
	}

	return result, nil
}
