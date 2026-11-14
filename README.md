# Reign

A lightweight Docker Compose orchestrator for single-machine deployments.

---

## Overview

Reign manages Docker Compose projects and native binaries on a single Linux server. It replaces manual systemd service files with a unified CLI, REST API, and persistent state management.

## Features

- **CLI**: Manage services from the command line
- **REST API**: Full control via HTTP endpoints
- **Docker Compose Management**: Start, stop, restart compose projects
- **Native Binary Support**: Run standalone binaries with journald logging
- **Infrastructure Priority**: Start databases and dependencies first
- **Explicit Image Pulls**: Per-service pull policy (`missing` by default); upgrades via `reign pull` or `reign restart --pull`
- **Persistent State**: SQLite database tracks all services
- **Event Logging**: Audit trail of all service operations, including who requested each one
- **Health Monitoring**: Cheap periodic checks (one Docker API call per tick) plus Docker events for immediate reaction

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
| `logLevel` | `info` | Log level: debug, info, warn, error (overridden by `REIGN_LOG_LEVEL`) |
| `externalStopGrace` | `1h` | How long a service stopped outside reign stays down before reign restarts it; `0` never restarts it |
| `binaryRunner` | `auto` | How binary services run: `systemd` (transient units), `direct` (child processes), `auto` (systemd if `systemd-run` exists) |
| `dockerSocket` | `/var/run/docker.sock` | Docker API socket for status checks; empty uses only the docker CLI |
| `runtimeDir` | `/run/reign` | Short-lived files, e.g. env files handed to transient units |
| `apiAuth` | `peer` | Who may change services: `peer` allows root and `adminGroup` members, `none` allows anyone who can reach the API |
| `adminGroup` | `reign` | Group whose members may create, update, delete, start, stop, restart and pull services |
| `allowedHosts` | `[]` | Extra `Host` header names the API accepts (IP addresses, `localhost`, the listen host and the machine's hostname always work) |

The `REIGN_LOG_LEVEL` environment variable, when set, wins over `logLevel` in
the file. The effective level and its source are logged at startup
(`Log level: info (from config)`), so a stray `Environment=REIGN_LOG_LEVEL=debug`
in a unit file is easy to spot.

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
| `restart` | | Restart a service (`--pull` pulls images first) |
| `pull` | | Pull a compose service's images without restarting |
| `logs` | | View service logs |
| `enable` | | Enable a service |
| `disable` | | Disable a service |
| `doctor` | | Find compose projects and systemd units that conflict with reign |
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
| `--workdir` | Working directory for binary services |
| `--envfile` | Env file for binary services |
| `--pull` | Compose pull policy: `missing` (default), `always`, `never` |
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
reign delete -y myservice     # no confirmation (also -f/--force)
```

Without a terminal on stdin (scripts, pipes) `--yes` is required; the command
fails with an error instead of reading a confirmation from the pipe.

### Start / Stop / Restart

```bash
reign start myservice
reign stop myservice
reign restart myservice
reign restart --pull myservice   # pull new images first, then restart
```

### Image Pulls

Starting or restarting a compose service does not pull images by default.
The per-service pull policy maps onto `docker compose up --pull`:

| Policy | Behavior |
|--------|----------|
| `missing` (default) | Pull only images that are not present locally |
| `always` | Pull on every start and restart |
| `never` | Never pull; a missing image fails the start |

Pin images to a digest (`image: postgres:16@sha256:...`) and upgrade on purpose:

```bash
reign update myservice --pull always   # change the policy
reign pull myservice                    # pull without restarting
reign restart --pull myservice          # pull, then restart (a failed pull aborts)
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

### Events

```bash
reign events                  # last 50 events across all services
reign events -f               # follow new events
```

Every event records its requester. The CLI sends its identity in the
`X-Reign-Requester` header (`user@host tty [sudo] ppid=N`, with the invoking
user when run through sudo). The server appends what it can verify: the remote
address and, for loopback connections on Linux, the UID owning the client
socket (looked up in `/proc/net/tcp`). Events reign triggers itself are
attributed to `reign (startup)`, `reign (health check)`, `reign (auto-restart)`
or `reign (shutdown)`. Start, stop, restart and delete requests are also logged
with their requester in the server log (journald).

### Doctor

```bash
reign doctor                  # human-readable report, exits 1 on findings
reign doctor --json
```

Runs on the server and reports two things reign cannot see otherwise:

- **Unmanaged compose projects**: projects with containers on the host whose
  working directory (the `com.docker.compose.project.working_dir` label) is not
  the path of any reign compose service. Needs `docker_socket`.
- **Conflicting systemd units**: `.service` files in `/etc/systemd/system` whose
  `Exec*` lines run `docker compose` (or `docker-compose`) and that mention a
  reign-managed path. Each is shown with its boot enablement, active state and
  restart count, so a unit that fights reign in a crash loop stands out. Disable
  it with `systemctl disable --now <unit>`.

A check that cannot run (Docker unreachable, no unit directory) is reported as
a warning instead of failing the whole command.

### API Access

reign runs services as root, so being able to create or start a service is
the same as being root. The API therefore:

- **Rejects browser requests.** Requests with an `Origin` header, a
  `Sec-Fetch-Site` other than `none`, a `Host` that is not an IP address or a
  known name (DNS rebinding), or a body that is not `application/json` get
  `403`. A web page can therefore not drive the API through a browser on the
  same machine.
- **Only lets root and the admin group change things.** Every method other
  than `GET`/`HEAD` needs a caller that is root or in `adminGroup` (default
  `reign`). The caller is identified by the UID that owns the client socket
  in `/proc/net/tcp`, so this works for loopback connections on Linux only;
  anyone else is refused. Reading (`list`, `show`, `logs`, `events`) stays
  open to local users.

To let a user deploy without sudo:

```bash
sudo groupadd --system reign
sudo usermod -aG reign tim   # takes effect at the next login
```

Env files are loaded by root, so reign only accepts one that is an absolute
path to a regular file (not a symlink), owned by root, by the user reign runs
as or by an admin, and not writable by other users (`chmod 600` is safest).
On create and update the calling user must also be able to read the file, so
the API cannot be used to read files the caller has no access to. A service
whose env file stops passing these checks fails to start with
`failed to load env file`.

If the group does not exist, only root can make changes. `"apiAuth": "none"`
turns the UID check off (for development on macOS, where the UID cannot be
determined); browser requests are still rejected.

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

Returns all registered services with their current state. Once the health
loop has checked a service, it also carries `observed`: the status last
reported by docker or systemd and when (`{"status":"running","checked_at":"..."}`).
`state.status` is what reign intends; `observed.status` is what it last saw.
Monitoring (e.g. a Zabbix HTTP agent item) can read this one endpoint, or
`reign ls --json`, instead of running compose commands per project.

### Service Status

```bash
GET /services/{id}/status
GET /services/{id}/status?fresh=1
```

Returns the observed status from the last health check without touching
docker or systemd. With `fresh=1` (or when no check has run yet), the executor
is asked live and the cache is updated.

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

Starts the service. Compose images are pulled according to the service's
`pull_policy` (default `missing`).

### Stop Service

```bash
POST /services/{id}/stop
```

Gracefully stops the service.

### Restart Service

```bash
POST /services/{id}/restart
```

Stops and starts the service. With `?pull=true` (compose only), images are
pulled first while the service keeps running, and a failed pull aborts the restart.

### Pull Images

```bash
POST /services/{id}/pull
```

Pulls a compose service's images synchronously without restarting it.

### Doctor

```bash
GET /doctor
```

Returns `unmanaged_projects`, `conflicting_units` and `warnings`; see
[Doctor](#doctor).

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

- `command` is the command line; `work_dir` (or `path`) is the working directory
- `env_file` is loaded into the environment (`KEY=value`, optional `export`, quotes stripped)
- Stdout/stderr go to journald with the service name as identifier (`journalctl -t <name>`)

With `binaryRunner` `systemd` (the default where `systemd-run` exists), each
binary service runs in its own transient unit, `reign-<id>.service`:

- Restarting reign (e.g. deploying a new binary) does not touch running binary
  services; reign finds them again through systemd.
- Each service has its own cgroup: `systemctl status reign-<id>`,
  `systemd-cgtop`, and journald attribute resources and output to it.
- The unit does not inherit reign's environment, only the env file and
  systemd's defaults.

With `direct`, binary services are child processes of reign and are killed
when reign restarts (systemd's default `KillMode=control-group`).

## Crashes and External Stops

The health check tells a crash from a stop made outside reign:

| Situation | Classified as |
|-----------|---------------|
| Container OOM killed (`OOMKilled=true`), unit `Result=oom-kill` | crash (OOM) |
| Containers removed (`docker compose down`, `docker rm`) | external stop |
| A `stop`/`kill` request reached the Docker API since the last start (`docker stop`, `docker compose stop`) | external stop |
| Binary unit gone after a clean exit (`systemctl stop`, `kill`, exit 0) | external stop |
| Anything else (non-zero exit, unit `failed`) | crash |

A crash is logged as `failed` with the exit codes (and `OOMKilled=true` for OOM
kills) and restarted with backoff as before. An external stop is logged as
`stopped_externally`, the service is marked `stopped`, and reign waits
`externalStopGrace` before restarting it, so a cold backup with
`docker compose stop` is not undone 30 seconds later. If the service comes back
on its own (`docker compose start`), reign logs `resumed` and marks it running.
`reign start` or `reign stop` ends the wait. The wait is kept in memory only:
after a reign restart, a stopped service stays stopped until started.

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

- [Architecture](docs/ARCHITECTURE.md): System design and component details
- [Data Model](docs/DATA_MODEL.md): Database schema and data structures
- [Roadmap](docs/ROADMAP.md): Planned features and future direction

---

## License

MIT
