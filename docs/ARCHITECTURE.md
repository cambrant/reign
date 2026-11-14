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
    ├── doctor/                 # Host checks behind GET /doctor
    │   ├── doctor.go           # Unmanaged compose projects, conflicting units
    │   └── doctor_test.go
    ├── orchestrator/           # Core orchestration logic
    │   ├── orchestrator.go     # Service lifecycle management
    │   └── orchestrator_test.go
    └── handlers/               # HTTP handlers
        ├── health.go           # Health check endpoint
        ├── services.go         # Service management endpoints
        ├── doctor.go           # GET /doctor
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
| `binaryRunner` | string | `auto` | `systemd`, `direct`, or `auto` |
| `runtimeDir` | string | `/run/reign` | Directory for short-lived runtime files |
| `dockerSocket` | string | `/var/run/docker.sock` | Docker API socket for status checks (empty: CLI only) |

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
- Runs `docker compose up -d` from the service directory, adding `--pull always|never`
  when the service's pull policy asks for it (default `missing` needs no flag)
- Never pulls as part of a start; `docker compose pull` only runs for
  `reign pull` and `reign restart --pull`
- Runs `docker compose stop` to stop
- Status: the project's `docker compose config` (cached until a file in the
  project directory changes) says which services should run; container state
  comes from one `GET /containers/json` on the Docker socket shared by all
  projects for 2s, falling back to `docker compose ps -a --format json`

**Binary Executor** (mode set by `binaryRunner`):
- `systemd` mode: `systemd-run --unit=reign-<id>.service --service-type=exec` with
  `SyslogIdentifier`, `WorkingDirectory`, `TimeoutStopSec` and, for env files, an
  `EnvironmentFile` rewritten in systemd syntax under `runtimeDir` (0600, removed
  once the unit has started). Status comes from the unit's `ActiveState`, stop is
  `systemctl stop`. Units are independent of reign's cgroup, so they survive reign
  restarts and need no re-adoption.
- `direct` mode: forks the binary under `systemd-cat`, tracks the process in
  memory, SIGTERM to stop and SIGKILL after the timeout. Processes die with reign.

### Orchestrator (`internal/orchestrator/`)

Core service lifecycle management:

1. **Startup Sequence:**
   - Load all enabled services from database
   - Start infrastructure services first (alphabetically)
   - Wait for each infrastructure service to be healthy
   - Start remaining services (alphabetically)

2. **Runtime Management:**
   - Periodic health checks, started evenly over the interval on up to three workers
   - A Docker event watcher (`GET /events` on the socket) that checks a project
     2s after its containers start, die, stop, get killed, run out of memory or
     change health, instead of waiting for the next tick; it reconnects with
     backoff, and the periodic check remains the safety net
   - Automatic restart on failure (for compose projects)
   - State tracking and persistence

3. **Shutdown Sequence:**
   - Stop all services in reverse order
   - Wait for graceful shutdown
   - Force kill after timeout

### Handlers (`internal/handlers/`)

HTTP handlers for the REST API. Uses standard `net/http`.

### Doctor (`internal/doctor/`)

Finds things on the host that compete with reign. It is read-only and runs on
request (`GET /doctor`, `reign doctor`), not in the health loop.

- Lists compose projects through the Docker API (`DockerClient.ComposeProjects`,
  one `/containers/json` call) and reports those whose
  `com.docker.compose.project.working_dir` does not match any compose service
  `Path`. Paths are cleaned and symlinks resolved before comparing.
- Scans `.service` files in `/etc/systemd/system` (skipping `reign*`) for
  `Exec*` lines that run `docker compose`/`docker-compose` and a whole-path
  mention of a reign-managed path (longest path first, so `/srv/app-db` is not
  taken for `/srv/app`). Matches get `UnitFileState`, `ActiveState` and
  `NRestarts` from one `systemctl show` call.
- Each check degrades to a warning when it cannot run.

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
│   Executor   │ ── docker compose up -d [--pull always|never]
└──────────────┘
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
  ├── Start service
  └── Wait for healthy status
       │
       ▼
For each regular service (alphabetically):
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
cd /home/tim/bookmarks && /usr/bin/docker compose -f docker-compose.yml up -d
cd /home/tim/bookmarks && /usr/bin/docker compose -f docker-compose.yml ps --format json
cd /home/tim/bookmarks && /usr/bin/docker compose -f docker-compose.yml stop
```

### Native Binary Execution

In `systemd` mode:
```bash
systemd-run --unit=reign-myapp.service --quiet --service-type=exec \
  --property=SyslogIdentifier=myapp --property=WorkingDirectory=/opt/myapp \
  --property=TimeoutStopSec=30 -- /usr/local/bin/myapp serve
systemctl show -p ActiveState --value reign-myapp.service
systemctl stop reign-myapp.service
```

In `direct` mode, binaries are executed using `os/exec`:
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

When a service that should run is found down, the executor's `StopCause`
decides between a crash and an external stop (compose: removed containers,
`docker inspect` for `OOMKilled`, and `docker events` for `stop`/`kill` since
the last start; systemd units: `ActiveState`/`Result`; direct mode: the
child's exit status).

An external stop marks the service `stopped`, logs `stopped_externally`, and
starts a grace period (`externalStopGrace`, default 1h, kept in memory). The
service is restarted after it unless it comes back by itself (`resumed`) or is
started or stopped through reign. With `externalStopGrace` 0 it stays stopped.

When a service crashes:
1. Mark service as `failed`
2. Log the failure with details (exit codes, OOMKilled)
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
