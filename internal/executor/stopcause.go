package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/reign/internal/models"
)

// StopCause explains why a service that should be running is down.
type StopCause struct {
	// External is true when the service was stopped outside reign (docker stop,
	// docker compose stop/down, systemctl stop, kill) rather than crashing.
	External bool
	// OOMKilled is true when the kernel's OOM killer ended the service.
	OOMKilled bool
	// Detail is a short human-readable explanation for the event log.
	Detail string
}

// StopCauser is implemented by executors that can tell crashes from external stops.
type StopCauser interface {
	// StopCause inspects a stopped service. since is when the service was last
	// started; only stop requests after it are considered.
	StopCause(ctx context.Context, service *models.Service, since time.Time) (StopCause, error)
}

// containerExit is the state of one container, from docker inspect.
type containerExit struct {
	Name      string
	Status    string // running, exited, restarting, ...
	ExitCode  int
	OOMKilled bool
	Project   string // com.docker.compose.project label
}

// inspectFormat prints one containerExit per line.
const inspectFormat = `{{.Name}}|{{.State.Status}}|{{.State.ExitCode}}|{{.State.OOMKilled}}|{{index .Config.Labels "com.docker.compose.project"}}`

func parseInspect(out []byte) []containerExit {
	var cs []containerExit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 5 {
			continue
		}
		code, _ := strconv.Atoi(f[2])
		cs = append(cs, containerExit{
			Name:      strings.TrimPrefix(f[0], "/"),
			Status:    f[1],
			ExitCode:  code,
			OOMKilled: f[3] == "true",
			Project:   f[4],
		})
	}
	return cs
}

// classifyContainers decides between crash and external stop.
//
//   - any container OOM killed: crash (OOM), whatever else happened
//   - a stop or kill request reached the Docker API since the last start: external
//   - otherwise: crash, reporting the exit codes
func classifyContainers(cs []containerExit, stopEvents []string) StopCause {
	var down []containerExit
	for _, c := range cs {
		if c.Status != "running" && c.Status != "restarting" {
			down = append(down, c)
		}
	}
	for _, c := range down {
		if c.OOMKilled {
			return StopCause{OOMKilled: true, Detail: fmt.Sprintf("container %s was OOM killed (OOMKilled=true, exit code %d)", c.Name, c.ExitCode)}
		}
	}
	if len(stopEvents) > 0 {
		return StopCause{External: true, Detail: "stopped outside reign (" + strings.Join(stopEvents, ", ") + ")"}
	}
	var parts []string
	for _, c := range down {
		parts = append(parts, fmt.Sprintf("%s exited with code %d", c.Name, c.ExitCode))
	}
	if len(parts) == 0 {
		return StopCause{Detail: "stopped unexpectedly"}
	}
	return StopCause{Detail: "crashed: " + strings.Join(parts, ", ")}
}

// StopCause tells a crash from an external stop for a compose project. Missing
// containers mean they were removed (docker compose down, docker rm). Otherwise
// the containers' state is inspected for OOM kills, and the Docker event log
// is searched for stop/kill requests made since the last start.
func (e *ComposeExecutor) StopCause(ctx context.Context, service *models.Service, since time.Time) (StopCause, error) {
	run := e.runner()

	idsOut, idsErr, err := run(ctx, service.Path, "docker", "compose", "ps", "-a", "-q")
	if err != nil {
		return StopCause{}, fmt.Errorf("docker compose ps failed: %s: %w", bytes.TrimSpace(idsErr), err)
	}
	ids := strings.Fields(string(idsOut))
	if len(ids) == 0 {
		return StopCause{External: true, Detail: "stopped outside reign (containers were removed, e.g. docker compose down)"}, nil
	}

	inspOut, inspErr, err := run(ctx, service.Path, "docker", append([]string{"inspect", "--format", inspectFormat}, ids...)...)
	if err != nil {
		return StopCause{}, fmt.Errorf("docker inspect failed: %s: %w", bytes.TrimSpace(inspErr), err)
	}
	containers := parseInspect(inspOut)

	var stopEvents []string
	if project := projectOf(containers); project != "" {
		stopEvents, err = e.stopEvents(ctx, service.Path, project, since)
		if err != nil {
			return StopCause{}, err
		}
	}
	return classifyContainers(containers, stopEvents), nil
}

func projectOf(cs []containerExit) string {
	for _, c := range cs {
		if c.Project != "" {
			return c.Project
		}
	}
	return ""
}

// stopEvents lists "stop name" / "kill name" Docker events for the project's
// containers between since and now. Docker emits them for API stop/kill
// requests (docker stop, docker compose stop), never for a crash or OOM kill.
func (e *ComposeExecutor) stopEvents(ctx context.Context, dir, project string, since time.Time) ([]string, error) {
	out, errOut, err := e.runner()(ctx, dir, "docker", "events",
		"--since", strconv.FormatInt(since.Unix(), 10),
		"--until", strconv.FormatInt(time.Now().Unix(), 10),
		"--filter", "type=container",
		"--filter", "label=com.docker.compose.project="+project,
		"--filter", "event=stop",
		"--filter", "event=kill",
		"--format", "{{.Action}} {{.Actor.Attributes.name}}")
	if err != nil {
		return nil, fmt.Errorf("docker events failed: %s: %w", bytes.TrimSpace(errOut), err)
	}
	seen := map[string]bool{}
	var events []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			events = append(events, line)
		}
	}
	return events, nil
}

// StopCause tells a crash from an external stop for a binary service.
func (e *BinaryExecutor) StopCause(ctx context.Context, service *models.Service, since time.Time) (StopCause, error) {
	if e.Mode() == BinaryModeSystemd {
		out, err := e.systemctl(ctx, "show", "-p", "ActiveState", "-p", "Result", "-p", "ExecMainCode", "-p", "ExecMainStatus", UnitName(service.ID))
		if err != nil {
			return StopCause{}, err
		}
		return classifyUnit(parseProperties(out)), nil
	}

	e.mu.Lock()
	ps := e.lastExit[service.Name]
	e.mu.Unlock()
	if ps == nil {
		return StopCause{}, fmt.Errorf("no exit status recorded for %s", service.Name)
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok {
		return StopCause{}, fmt.Errorf("unsupported exit status for %s", service.Name)
	}
	return classifyWaitStatus(ws), nil
}

// parseProperties parses `systemctl show -p A -p B` output (KEY=value lines).
func parseProperties(out []byte) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	return props
}

// classifyUnit decides from a transient unit's properties. A unit that failed
// stays loaded with its Result; a unit that exited cleanly (exit 0, or SIGTERM
// from systemctl stop or kill) is unloaded and reads as inactive/success.
func classifyUnit(p map[string]string) StopCause {
	if p["ActiveState"] != "failed" {
		return StopCause{External: true, Detail: "stopped outside reign or exited cleanly (systemctl stop, kill, or exit 0)"}
	}
	if p["Result"] == "oom-kill" {
		return StopCause{OOMKilled: true, Detail: "killed by the OOM killer (Result=oom-kill)"}
	}
	return StopCause{Detail: fmt.Sprintf("crashed (Result=%s, status=%s)", p["Result"], p["ExecMainStatus"])}
}

// classifyWaitStatus decides from a direct-mode child's exit status, using
// systemd's notion of a clean exit: code 0, or SIGTERM/SIGINT/SIGHUP.
func classifyWaitStatus(ws syscall.WaitStatus) StopCause {
	switch {
	case ws.Exited() && ws.ExitStatus() == 0:
		return StopCause{External: true, Detail: "stopped outside reign or exited cleanly (exit 0)"}
	case ws.Signaled():
		switch ws.Signal() {
		case syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP:
			return StopCause{External: true, Detail: fmt.Sprintf("stopped outside reign (%s)", ws.Signal())}
		}
		return StopCause{Detail: fmt.Sprintf("crashed (killed by %s)", ws.Signal())}
	default:
		return StopCause{Detail: fmt.Sprintf("crashed (exit code %d)", ws.ExitStatus())}
	}
}

// recordExit stores a direct-mode child's exit status for StopCause.
func (e *BinaryExecutor) recordExit(name string, ps *os.ProcessState) {
	if ps == nil {
		return
	}
	if e.lastExit == nil {
		e.lastExit = make(map[string]*os.ProcessState)
	}
	e.lastExit[name] = ps
}
