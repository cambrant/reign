# Reign

A lightweight Docker Compose orchestrator for single-machine deployments.

---

## Overview

Reign manages Docker Compose projects and native binaries on a single Linux server. It replaces manual systemd service files with a unified CLI, REST API, and persistent state management.

```
  reign <cmd>          ┌──────────────────────────────────────────┐
  (CLI client) ──────► │              Reign Server                │
                       │                                          │
  curl / HTTP ───────► │  REST API ◄──► Orchestrator ◄──► Docker  │
                       │      │              │            Compose │
  systemd ───────────► │      └──────► SQLite Database            │
  (runs reign serve)   └──────────────────────────────────────────┘
                                             │
                               ┌─────────────┼─────────────┐
                               ▼             ▼             ▼
                         ┌─────────┐   ┌─────────┐   ┌─────────┐
                         │ Service │   │ Service │   │ Service │
                         │   #1    │   │   #2    │   │   #3    │
                         └─────────┘   └─────────┘   └─────────┘
```

## Features

- **CLI** - Manage services from the command line
- **REST API** - Full control via HTTP endpoints
- **Docker Compose Management** - Start, stop, restart compose projects
- **Native Binary Support** - Run standalone binaries with journald logging
- **Infrastructure Priority** - Start databases and dependencies first
- **Automatic Image Pulls** - Always pull latest images before starting
- **Persistent State** - SQLite database tracks all services
- **Event Logging** - Audit trail of all service operations
- **Health Monitoring** - Report container status and statistics

---

## Quick Start

### Prerequisites

- Linux (Debian/Ubuntu recommended)
- Docker with Compose V2 (`docker compose` command)
- Go 1.21+ (for building)

### Build

```bash
make build
```

### Configure

```bash
cp config.json.sample config.json
# Edit config.json as needed
```

### Run

```bash
# Development
./run.sh

# Production (as systemd service)
sudo cp reign /usr/local/bin/
sudo cp reign.service /etc/systemd/system/
sudo systemctl enable --now reign
```

Once the server is running, use the CLI from the same binary:

```bash
reign list
reign show myservice
reign start myservice
```

---

## Configuration

Configuration file (`config.json`):

```json
{
  "listenAddr": "127.0.0.1:7890",
  "databasePath": "/var/lib/reign/reign.db",
  "logLevel": "info"
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `listenAddr` | `127.0.0.1:7890` | HTTP API listen address |
| `databasePath` | `/var/lib/reign/reign.db` | SQLite database location |
| `logLevel` | `info` | Log level: debug, info, warn, error |

---

## CLI Reference

The `reign` binary acts as both the server and the CLI client. When invoked with a subcommand it talks to the running server over HTTP.

### Global Options

| Option | Env Var | Default | Description |
|--------|---------|---------|-------------|
| `--server` | `REIGN_SERVER` | `http://127.0.0.1:7890` | Server address |

### Commands

| Command | Aliases | Description |
|---------|---------|-------------|
| `list` | `ls`, `status`, `ps` | List all services with status |
| `show` | `get` | Show detailed service information |
| `create` | `add` | Create a new service |
| `update` | `set` | Update a service |
| `delete` | `rm`, `remove` | Delete a service |
| `start` | | Start a service |
| `stop` | | Stop a service |
| `restart` | | Restart a service |
| `logs` | | View service logs |
| `enable` | | Enable a service |
| `disable` | | Disable a service |
| `serve` | | Start the server (default) |
| `help` | | Show help for a command |
| `version` | | Show version |

### Listing Services

```bash
reign list              # table output
reign list --json       # JSON output
```

### Showing a Service

```bash
reign show myservice              # human-readable details + events
reign show --json myservice       # JSON service definition only
```

The `--json` flag outputs the service definition in a format that can be piped
directly into `create` or `update`.

### Creating a Service

Using flags:

```bash
reign create \
  --id myapp \
  --name "My Application" \
  --type compose \
  --path /home/tim/myapp
```

Using a JSON file:

```bash
reign create -f service.json
```

From stdin (e.g. clone an existing service):

```bash
reign show --json oldservice | jq '.id = "newservice"' | reign create -f -
```

| Flag | Description |
|------|-------------|
| `-f`, `--file` | JSON file with service definition (`-` for stdin) |
| `--id` | Service ID (required) |
| `--name` | Display name (required) |
| `--type` | `compose` or `binary` (required) |
| `--path` | Path to compose dir or binary (required) |
| `--command` | Command / arguments for binary services |
| `--enabled` | `true` / `false` (default: `true`) |
| `--infrastructure` | `true` / `false` (default: `false`) |

Flags override any values loaded from a JSON file.

### Updating a Service

```bash
reign update myservice --name "New Name"
reign update myservice --enabled false
reign update myservice -f updated.json
reign show --json myservice | jq '.name = "New Name"' | reign update myservice -f -
```

Accepts the same field flags as `create` (except `--id`). Only provided fields
are changed.

### Deleting a Service

```bash
reign delete myservice        # prompts for confirmation
reign delete -f myservice     # no confirmation
```

### Start / Stop / Restart

```bash
reign start myservice
reign stop myservice
reign restart myservice
```

### Enable / Disable

```bash
reign disable myservice       # prevents starting
reign enable myservice        # allows starting again
```

### Logs

```bash
reign logs myservice          # last 100 lines
reign logs -n 50 myservice    # last 50 lines
```

---

## API Reference

### Health Check

```bash
GET /health
```

Returns orchestrator status and summary.

### List Services

```bash
GET /services
```

Returns all registered services with their current state.

### Get Service

```bash
GET /services/{id}
```

Returns a single service with state and recent events.

### Register Service

```bash
POST /services
Content-Type: application/json

{
  "id": "bookmarks",
  "name": "Shaarli Bookmarks",
  "type": "compose",
  "path": "/home/tim/bookmarks",
  "enabled": true,
  "infrastructure": false
}
```

### Update Service

```bash
PUT /services/{id}
Content-Type: application/json

{
  "name": "Updated Name",
  "enabled": false
}
```

### Delete Service

```bash
DELETE /services/{id}
```

Stops the service if running and removes it from the database.

### Start Service

```bash
POST /services/{id}/start
```

Pulls images (for compose) and starts the service.

### Stop Service

```bash
POST /services/{id}/stop
```

Gracefully stops the service.

### Restart Service

```bash
POST /services/{id}/restart
```

Stops and starts the service.

### Service Logs

```bash
GET /services/{id}/logs?lines=100
```

Returns recent logs from the service (compose) or journald (binary).

---

## Service Types

### Docker Compose (`type: "compose"`)

- `path` is the directory containing `docker-compose.yml`
- Reign runs `docker compose` from this directory
- Environment files and bind mounts work as expected

### Native Binary (`type: "binary"`)

- `path` is the full path to the executable
- Stdout/stderr are captured to journald
- Working directory is the binary's parent directory

---

## Infrastructure Services

Services marked as `infrastructure: true` are started first during boot:

1. All infrastructure services start in alphabetical order
2. Each must be healthy before the next starts
3. Regular services start after all infrastructure is ready

Example: PostgreSQL and Redis as infrastructure, web apps as regular services.

---

## Example Workflow

Using the CLI:

```bash
# Register a database (infrastructure)
reign create \
  --id postgres \
  --name "PostgreSQL" \
  --type compose \
  --path /home/tim/postgres \
  --infrastructure true

# Register an application
reign create \
  --id myapp \
  --name "My Application" \
  --type compose \
  --path /home/tim/myapp

# Start services
reign start postgres
reign start myapp

# Check status
reign list

# View details
reign show myapp

# Export, tweak, and clone a service
reign show --json myapp | jq '.id = "myapp-staging" | .name = "My App (staging)"' | reign create -f -
```

The same operations via curl:

```bash
# Register a database (infrastructure)
curl -X POST http://localhost:7890/services \
  -H "Content-Type: application/json" \
  -d '{
    "id": "postgres",
    "name": "PostgreSQL",
    "type": "compose",
    "path": "/home/tim/postgres",
    "infrastructure": true
  }'

# Register an application
curl -X POST http://localhost:7890/services \
  -H "Content-Type: application/json" \
  -d '{
    "id": "myapp",
    "name": "My Application",
    "type": "compose",
    "path": "/home/tim/myapp"
  }'

# Start all services
curl -X POST http://localhost:7890/services/postgres/start
curl -X POST http://localhost:7890/services/myapp/start

# Check status
curl http://localhost:7890/services
```

---

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - System design and component details
- [Data Model](docs/DATA_MODEL.md) - Database schema and data structures
- [Roadmap](docs/ROADMAP.md) - Planned features and future direction

---

## License

MIT
