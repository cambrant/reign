# Reign Data Model

This document describes the data structures and database schema used by Reign.

---

## Database Schema

Reign uses SQLite for persistent storage. The database is stored at the configured path (default: `/var/lib/reign/reign.db`).

### Tables

#### `services`

Stores the configuration and desired state for each managed service.

```sql
CREATE TABLE IF NOT EXISTS services (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('compose', 'binary')),
    path            TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    infrastructure  INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
```

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT | Unique identifier (e.g., "bookmarks", "postgres") |
| `name` | TEXT | Human-readable display name |
| `type` | TEXT | Service type: "compose" or "binary" |
| `path` | TEXT | Filesystem path to compose file directory or binary |
| `enabled` | INTEGER | 1 if service should auto-start, 0 if disabled |
| `infrastructure` | INTEGER | 1 if this is an infrastructure service (starts first) |
| `created_at` | TEXT | ISO 8601 timestamp of creation |
| `updated_at` | TEXT | ISO 8601 timestamp of last update |

#### `service_state`

Tracks the current runtime state of each service.

```sql
CREATE TABLE IF NOT EXISTS service_state (
    service_id      TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'stopped',
    pid             INTEGER,
    started_at      TEXT,
    stopped_at      TEXT,
    restart_count   INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
```

| Column | Type | Description |
|--------|------|-------------|
| `service_id` | TEXT | Foreign key to services.id |
| `status` | TEXT | Current status (see Service Status enum) |
| `pid` | INTEGER | Process ID for binary services, NULL for compose |
| `started_at` | TEXT | ISO 8601 timestamp when service was last started |
| `stopped_at` | TEXT | ISO 8601 timestamp when service was last stopped |
| `restart_count` | INTEGER | Number of automatic restarts since last manual start |
| `last_error` | TEXT | Last error message if status is 'failed' |
| `updated_at` | TEXT | ISO 8601 timestamp of last state change |

#### `service_events`

Audit log of service lifecycle events for analytics.

```sql
CREATE TABLE IF NOT EXISTS service_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    service_id      TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    message         TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_service_events_service_id ON service_events(service_id);
CREATE INDEX idx_service_events_created_at ON service_events(created_at);
```

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER | Auto-incrementing primary key |
| `service_id` | TEXT | Foreign key to services.id |
| `event_type` | TEXT | Event type (started, stopped, failed, restarted, etc.) |
| `message` | TEXT | Optional details about the event |
| `created_at` | TEXT | ISO 8601 timestamp of the event |

---

## Go Data Structures

### Service Types

```go
// ServiceType represents the type of service
type ServiceType string

const (
    ServiceTypeCompose ServiceType = "compose"
    ServiceTypeBinary  ServiceType = "binary"
)

// ServiceStatus represents the current status of a service
type ServiceStatus string

const (
    StatusStopped  ServiceStatus = "stopped"
    StatusStarting ServiceStatus = "starting"
    StatusRunning  ServiceStatus = "running"
    StatusStopping ServiceStatus = "stopping"
    StatusFailed   ServiceStatus = "failed"
    StatusDisabled ServiceStatus = "disabled"
)
```

### Service

The core service definition:

```go
type Service struct {
    ID             string      `json:"id"`
    Name           string      `json:"name"`
    Type           ServiceType `json:"type"`
    Path           string      `json:"path"`
    Enabled        bool        `json:"enabled"`
    Infrastructure bool        `json:"infrastructure"`
    CreatedAt      time.Time   `json:"created_at"`
    UpdatedAt      time.Time   `json:"updated_at"`
}
```

### ServiceState

Runtime state of a service:

```go
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
```

### ServiceWithState

Combined view for API responses:

```go
type ServiceWithState struct {
    Service
    State ServiceState `json:"state"`
}
```

### ServiceEvent

Lifecycle event for audit log:

```go
type ServiceEvent struct {
    ID        int       `json:"id"`
    ServiceID string    `json:"service_id"`
    EventType string    `json:"event_type"`
    Message   string    `json:"message,omitempty"`
    CreatedAt time.Time `json:"created_at"`
}
```

### Event Types

Standard event types for the audit log:

| Event Type | Description |
|------------|-------------|
| `created` | Service was registered |
| `updated` | Service configuration was modified |
| `deleted` | Service was removed |
| `enabled` | Service was enabled for auto-start |
| `disabled` | Service was disabled |
| `starting` | Service start initiated |
| `started` | Service successfully started |
| `stopping` | Service stop initiated |
| `stopped` | Service successfully stopped |
| `failed` | Service failed to start or crashed |
| `restarted` | Automatic restart triggered |
| `pulled` | Docker images were pulled |

---

## API Request/Response Types

### Create/Update Service Request

```go
type ServiceRequest struct {
    ID             string `json:"id"`
    Name           string `json:"name"`
    Type           string `json:"type"`
    Path           string `json:"path"`
    Enabled        *bool  `json:"enabled,omitempty"`
    Infrastructure *bool  `json:"infrastructure,omitempty"`
}
```

### Service List Response

```go
type ServiceListResponse struct {
    Services []ServiceWithState `json:"services"`
    Total    int                `json:"total"`
}
```

### Service Stats Response

Container statistics for Docker Compose services:

```go
type ContainerStats struct {
    Name       string  `json:"name"`
    State      string  `json:"state"`
    Health     string  `json:"health,omitempty"`
    CPUPercent float64 `json:"cpu_percent"`
    MemoryMB   float64 `json:"memory_mb"`
}

type ServiceStats struct {
    ServiceID     string           `json:"service_id"`
    Status        ServiceStatus    `json:"status"`
    UptimeSeconds int64            `json:"uptime_seconds"`
    LastStarted   *time.Time       `json:"last_started,omitempty"`
    RestartCount  int              `json:"restart_count"`
    Containers    []ContainerStats `json:"containers,omitempty"`
}
```

### Health Response

Orchestrator health check:

```go
type HealthResponse struct {
    Status          string `json:"status"`
    Version         string `json:"version"`
    Uptime          int64  `json:"uptime_seconds"`
    ServicesTotal   int    `json:"services_total"`
    ServicesRunning int    `json:"services_running"`
    ServicesFailed  int    `json:"services_failed"`
}
```

### Error Response

Standard error response:

```go
type ErrorResponse struct {
    Error   string `json:"error"`
    Code    string `json:"code,omitempty"`
    Details string `json:"details,omitempty"`
}
```

---

## Database Operations

### Service CRUD

```go
// Create a new service
func (s *Service) Create(db *sql.DB) error

// Get a service by ID
func GetServiceByID(db *sql.DB, id string) (*Service, error)

// List all services
func ListServices(db *sql.DB) ([]Service, error)

// List enabled services ordered for startup
func ListEnabledServicesForStartup(db *sql.DB) ([]Service, error)

// Update a service
func (s *Service) Update(db *sql.DB) error

// Delete a service
func DeleteService(db *sql.DB, id string) error
```

### State Operations

```go
// Get or create state for a service
func GetServiceState(db *sql.DB, serviceID string) (*ServiceState, error)

// Update service status
func UpdateServiceStatus(db *sql.DB, serviceID string, status ServiceStatus) error

// Set service as started
func SetServiceStarted(db *sql.DB, serviceID string, pid *int) error

// Set service as stopped
func SetServiceStopped(db *sql.DB, serviceID string) error

// Set service as failed
func SetServiceFailed(db *sql.DB, serviceID string, errMsg string) error

// Increment restart count
func IncrementRestartCount(db *sql.DB, serviceID string) error

// Reset restart count (on manual start)
func ResetRestartCount(db *sql.DB, serviceID string) error
```

### Event Operations

```go
// Log an event
func LogEvent(db *sql.DB, serviceID, eventType, message string) error

// Get events for a service
func GetServiceEvents(db *sql.DB, serviceID string, limit int) ([]ServiceEvent, error)

// Get recent events across all services
func GetRecentEvents(db *sql.DB, limit int) ([]ServiceEvent, error)
```

---

## Relationships

```
┌─────────────┐       ┌──────────────────┐
│  services   │───────│  service_state   │
│             │  1:1  │                  │
└─────────────┘       └──────────────────┘
       │
       │ 1:N
       ▼
┌─────────────────┐
│ service_events  │
└─────────────────┘
```

- Each service has exactly one state record (created automatically)
- Each service can have many event records (audit log)
- Deleting a service cascades to delete its state and events
