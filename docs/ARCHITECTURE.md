# Reign Architecture

Reign is a lightweight Docker Compose orchestrator designed for single-user, single-machine deployments. It provides a simple REST API for managing Docker Compose projects and native binaries, with persistent state stored in SQLite.

---

## Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         systemd                                  │
│                    (runs reign as root)                          │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Reign                                    │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │  REST API   │  │ Orchestrator│  │  Service Executors      │  │
│  │  (HTTP)     │──│  (Core)     │──│  - Docker Compose       │  │
│  │             │  │             │  │  - Native Binary        │  │
│  └─────────────┘  └─────────────┘  └─────────────────────────┘  │
│         │               │                      │                 │
│         └───────────────┼──────────────────────┘                 │
│                         │                                        │
│                         ▼                                        │
│              ┌─────────────────────┐                             │
│              │   SQLite Database   │                             │
│              │   (services.db)     │                             │
│              └─────────────────────┘                             │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Managed Services                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │ Docker       │  │ Docker       │  │ Native       │          │
│  │ Compose #1   │  │ Compose #2   │  │ Binary #1    │          │
│  └──────────────┘  └──────────────┘  └──────────────┘          │
└─────────────────────────────────────────────────────────────────┘
```

---

## Package Structure

```
reign/
├── main.go                     # Entry point, signal handling, startup
├── main_test.go                # Integration tests
├── go.mod                      # Go module definition
├── go.sum                      # Dependency checksums
├── Makefile                    # Build, test, and utility commands
├── run.sh                      # Development run script
├── config.json                 # Active configuration (gitignored)
├── config.json.sample          # Sample configuration
├── README.md                   # Project overview
├── docs/
│   ├── ARCHITECTURE.md         # This document
│   ├── DATA_MODEL.md           # Database schema and structures
│   └── ROADMAP.md              # Planned features
└── internal/
    ├── config/                 # Configuration loading
    │   ├── config.go           # Config struct and loading logic
    │   └── config_test.go      # Configuration tests
    ├── logger/                 # Logging package
    │   └── logger.go           # Level-based logging
    ├── models/                 # Data models and database
    │   ├── db.go               # Database initialization
    │   ├── service.go          # Service model and CRUD
    │   └── service_test.go     # Model tests
    ├── executor/               # Service execution
    │   ├── executor.go         # Executor interface
    │   ├── compose.go          # Docker Compose executor
    │   ├── binary.go           # Native binary executor
    │   └── executor_test.go    # Executor tests
    ├── orchestrator/           # Core orchestration logic
    │   ├── orchestrator.go     # Service lifecycle management
    │   └── orchestrator_test.go
    └── handlers/               # HTTP handlers
        ├── health.go           # Health check endpoint
        ├── services.go         # Service management endpoints
        └── handlers_test.go    # Handler tests
```

---

## Component Details

### Configuration (`internal/config/`)

Handles loading configuration from JSON files with sensible defaults.

**Configuration Options:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `listenAddr` | string | `127.0.0.1:7890` | HTTP API listen address |
| `databasePath` | string | `/var/lib/reign/reign.db` | SQLite database path |
| `logLevel` | string | `info` | Log level (debug, info, warn, error) |
| `startupTimeout` | duration | `5m` | Max time to wait for services on startup |
| `healthCheckInterval` | duration | `30s` | Interval between health checks |

### Logger (`internal/logger/`)

Simple level-based logging to stdout with timestamps. All output goes to stdout/stderr which systemd captures to journald.

### Models (`internal/models/`)

Data structures representing services and their state. Handles all SQLite database operations.

**Key Types:**
- `Service` - Core service definition
- `ServiceState` - Current runtime state
- `ServiceType` - Enum: `compose`, `binary`
- `ServiceStatus` - Enum: `stopped`, `starting`, `running`, `stopping`, `failed`, `disabled`

### Executor (`internal/executor/`)

Abstracts the execution of different service types.

**Interface:**
```go
type Executor interface {
    Start(ctx context.Context, service *models.Service) error
    Stop(ctx context.Context, service *models.Service) error
    Status(ctx context.Context, service *models.Service) (models.ServiceStatus, error)
    Pull(ctx context.Context, service *models.Service) error
}
```

**Docker Compose Executor:**
- Runs `docker compose pull` before starting
- Runs `docker compose up -d` from the service directory
- Runs `docker compose stop` to stop
- Parses `docker compose ps --format json` for status

**Binary Executor:**
- Forks the binary process
- Redirects stdout/stderr to journald via systemd-cat
- Tracks PID for monitoring
- Sends SIGTERM for graceful stop, SIGKILL after timeout

### Orchestrator (`internal/orchestrator/`)

Core service lifecycle management:

1. **Startup Sequence:**
   - Load all enabled services from database
   - Start infrastructure services first (alphabetically)
   - Wait for each infrastructure service to be healthy
   - Start remaining services (alphabetically)

2. **Runtime Management:**
   - Periodic health checks
   - Automatic restart on failure (for compose projects)
   - State tracking and persistence

3. **Shutdown Sequence:**
   - Stop all services in reverse order
   - Wait for graceful shutdown
   - Force kill after timeout

### Handlers (`internal/handlers/`)

HTTP handlers for the REST API. Uses standard `net/http`.

---

## Data Flow

### Service Start Flow

```
API Request: POST /services/{id}/start
       │
       ▼
┌──────────────┐
│   Handler    │ ── Validate request
└──────────────┘
       │
       ▼
┌──────────────┐
│ Orchestrator │ ── Update state to 'starting'
└──────────────┘
       │
       ▼
┌──────────────┐
│   Executor   │ ── docker compose pull
└──────────────┘    docker compose up -d
       │
       ▼
┌──────────────┐
│   Database   │ ── Update state to 'running' or 'failed'
└──────────────┘
       │
       ▼
    Response
```

### Startup Boot Flow

```
systemd starts reign
       │
       ▼
Load configuration
       │
       ▼
Open/init database
       │
       ▼
Query enabled services
       │
       ▼
Filter infrastructure services
       │
       ▼
For each infra service (alphabetically):
  ├── Pull images
  ├── Start service
  └── Wait for healthy status
       │
       ▼
For each regular service (alphabetically):
  ├── Pull images
  └── Start service
       │
       ▼
Start HTTP API server
       │
       ▼
Begin health check loop
```

---

## Execution Details

### Docker Compose Execution

All Docker Compose commands are executed:
- From the directory containing docker-compose.yml
- With the full path to the compose file
- Using `/usr/bin/docker compose` (modern Docker CLI)

Example:
```bash
cd /home/tim/bookmarks && /usr/bin/docker compose -f docker-compose.yml pull
cd /home/tim/bookmarks && /usr/bin/docker compose -f docker-compose.yml up -d
cd /home/tim/bookmarks && /usr/bin/docker compose -f docker-compose.yml ps --format json
cd /home/tim/bookmarks && /usr/bin/docker compose -f docker-compose.yml stop
```

### Native Binary Execution

Binaries are executed using `os/exec`:
- Working directory set to configured path
- Stdout/stderr piped to `systemd-cat` with service name tag
- PID stored in database for tracking
- Process group created for clean shutdown

---

## Health Monitoring

### Container Status

For Docker Compose services, reign queries container status:
```bash
docker compose ps --format json
```

Response includes:
- Container state (running, exited, etc.)
- Health status (if health check defined)
- Exit code (if stopped)

### Analytics Endpoint

`GET /services/{id}/stats` returns:
```json
{
  "service_id": "bookmarks",
  "status": "running",
  "uptime_seconds": 86400,
  "last_started": "2026-01-31T10:00:00Z",
  "restart_count": 0,
  "containers": [
    {
      "name": "bookmarks-shaarli-1",
      "state": "running",
      "cpu_percent": 0.5,
      "memory_mb": 128
    }
  ]
}
```

---

## Error Handling

### Restart Policy

When a Docker Compose project fails:
1. Mark service as `failed`
2. Log the failure with details
3. After configurable delay (default: 30s), attempt restart
4. Track restart count
5. After max restarts (default: 5), leave in `failed` state

### Graceful Shutdown

On SIGTERM/SIGINT:
1. Stop accepting new API requests
2. Cancel ongoing operations
3. Stop all services in reverse start order
4. Wait up to 30s for graceful stop
5. Force kill remaining processes
6. Close database
7. Exit

---

## Security Considerations

Reign runs as root and is designed for single-user hobby deployments. Current security model:

- API listens on localhost only by default
- No authentication (planned for roadmap)
- Full access to Docker and system processes

Future considerations documented in ROADMAP.md.
