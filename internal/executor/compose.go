package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/reign/internal/models"
)

// CommandRunner runs a command in dir and returns stdout and stderr separately.
// Keeping them apart matters: docker compose prints warnings (e.g. the obsolete
// `version` attribute) on stderr, which must never be parsed as command output.
type CommandRunner func(ctx context.Context, dir string, name string, args ...string) (stdout, stderr []byte, err error)

func execRunner(ctx context.Context, dir string, name string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// ComposeExecutor executes Docker Compose services.
type ComposeExecutor struct {
	run CommandRunner
}

// NewComposeExecutor returns a ComposeExecutor that runs commands via run.
// A nil run uses the real docker CLI.
func NewComposeExecutor(run CommandRunner) *ComposeExecutor {
	return &ComposeExecutor{run: run}
}

func (e *ComposeExecutor) runner() CommandRunner {
	if e.run == nil {
		return execRunner
	}
	return e.run
}

// compose runs a docker compose subcommand that is only judged by its exit code.
// On failure both streams are included in the error, since that is what explains it.
func (e *ComposeExecutor) compose(ctx context.Context, dir string, args ...string) error {
	stdout, stderr, err := e.runner()(ctx, dir, "docker", append([]string{"compose"}, args...)...)
	if err != nil {
		return fmt.Errorf("docker compose %s failed: %s%s: %w", args[0], stdout, stderr, err)
	}
	return nil
}

// Start starts a Docker Compose service.
//
// Images are pulled according to the service's pull policy. The default
// (missing) is what `docker compose up` does by itself, so no flag is passed;
// floating tags are never silently refreshed by a restart.
func (e *ComposeExecutor) Start(ctx context.Context, service *models.Service) error {
	return e.compose(ctx, service.Path, upArgs(service.PullPolicy)...)
}

// upArgs returns the `docker compose up` arguments for a pull policy.
func upArgs(policy models.PullPolicy) []string {
	args := []string{"up", "-d"}
	switch policy {
	case models.PullAlways, models.PullNever:
		args = append(args, "--pull", string(policy))
	}
	return args
}

// Stop stops a Docker Compose service.
func (e *ComposeExecutor) Stop(ctx context.Context, service *models.Service) error {
	return e.compose(ctx, service.Path, "down")
}

// psEntry is one container from `docker compose ps --format json`.
type psEntry struct {
	Service  string `json:"Service"`
	State    string `json:"State"`
	Health   string `json:"Health"`
	ExitCode int    `json:"ExitCode"`
	Labels   string `json:"Labels"`
}

// oneOff reports whether the container was created by `docker compose run`.
func (p psEntry) oneOff() bool {
	return strings.Contains(p.Labels, "com.docker.compose.oneoff=True")
}

// serviceSpec is the part of a service's resolved compose config that decides
// whether it is expected to be running.
type serviceSpec struct {
	Restart string `json:"restart"`
	Deploy  *struct {
		Replicas *int `json:"replicas"`
	} `json:"deploy"`
}

// parsePS parses `docker compose ps --format json` output. Compose v2.21+ prints
// one JSON object per line; older versions print a single JSON array.
func parsePS(out []byte) ([]psEntry, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	if out[0] == '[' {
		var entries []psEntry
		if err := json.Unmarshal(out, &entries); err != nil {
			return nil, fmt.Errorf("parse compose ps output: %w", err)
		}
		return entries, nil
	}
	var entries []psEntry
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var p psEntry
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, fmt.Errorf("parse compose ps output: %w", err)
		}
		entries = append(entries, p)
	}
	return entries, nil
}

// evaluateStatus decides a project's status from its expected services and containers.
//
//   - stopped:  no service has a running container
//   - failed:   some services run but at least one is missing, exited or unhealthy
//   - starting: everything runs but a health check is still starting
//   - running:  every expected service is up (or a one-shot that exited 0)
//
// One-shot services (restart policy "no" that exited 0) count as healthy, and
// containers left over from `docker compose run` are ignored.
func evaluateStatus(specs map[string]serviceSpec, containers []psEntry) models.ServiceStatus {
	byService := make(map[string][]psEntry)
	for _, c := range containers {
		if c.oneOff() {
			continue
		}
		byService[c.Service] = append(byService[c.Service], c)
	}

	anyRunning, anyCompleted, anyDown, anyUnhealthy, anyStarting := false, false, false, false, false
	for name, spec := range specs {
		if spec.Deploy != nil && spec.Deploy.Replicas != nil && *spec.Deploy.Replicas == 0 {
			continue // scaled to zero: nothing is expected
		}
		cs := byService[name]
		if len(cs) == 0 {
			anyDown = true
			continue
		}
		for _, c := range cs {
			switch {
			case c.State == "running" || c.State == "restarting":
				anyRunning = true
				switch c.Health {
				case "unhealthy":
					anyUnhealthy = true
				case "starting":
					anyStarting = true
				}
				if c.State == "restarting" {
					anyStarting = true
				}
			case c.State == "exited" && c.ExitCode == 0 && (spec.Restart == "" || spec.Restart == "no"):
				anyCompleted = true // one-shot service that completed successfully
			default:
				anyDown = true
			}
		}
	}

	switch {
	case !anyRunning && !(anyCompleted && !anyDown):
		return models.StatusStopped
	case anyDown || anyUnhealthy:
		return models.StatusFailed
	case anyStarting:
		return models.StatusStarting
	default:
		return models.StatusRunning
	}
}

// Status returns the status of a Docker Compose service.
//
// It returns an error, not StatusStopped, when the state cannot be determined
// (e.g. the docker daemon is restarting): callers must not act on an unknown state.
func (e *ComposeExecutor) Status(ctx context.Context, service *models.Service) (models.ServiceStatus, error) {
	run := e.runner()

	cfgOut, cfgErr, err := run(ctx, service.Path, "docker", "compose", "config", "--format", "json")
	if err != nil {
		return "", fmt.Errorf("docker compose config failed: %s: %w", bytes.TrimSpace(cfgErr), err)
	}
	var cfg struct {
		Services map[string]serviceSpec `json:"services"`
	}
	if err := json.Unmarshal(cfgOut, &cfg); err != nil {
		return "", fmt.Errorf("parse compose config: %w", err)
	}

	psOut, psErr, err := run(ctx, service.Path, "docker", "compose", "ps", "-a", "--format", "json")
	if err != nil {
		return "", fmt.Errorf("docker compose ps failed: %s: %w", bytes.TrimSpace(psErr), err)
	}
	containers, err := parsePS(psOut)
	if err != nil {
		return "", err
	}

	return evaluateStatus(cfg.Services, containers), nil
}

// Pull pulls the latest images for a Docker Compose service. It is only run
// on request (reign pull, reign restart --pull), never as part of a start.
func (e *ComposeExecutor) Pull(ctx context.Context, service *models.Service) error {
	return e.compose(ctx, service.Path, "pull")
}

// GetLogs retrieves logs for a Docker Compose service.
func (e *ComposeExecutor) GetLogs(ctx context.Context, service *models.Service, lines int) (string, error) {
	args := []string{"compose", "logs", "--no-color"}
	if lines > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", lines))
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = service.Path
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker compose logs failed: %s: %w", string(output), err)
	}
	return string(output), nil
}

// FollowLogs streams logs for a Docker Compose service to the given writer.
// It blocks until the context is cancelled.
func (e *ComposeExecutor) FollowLogs(ctx context.Context, service *models.Service, lines int, w func(line string)) error {
	args := []string{"compose", "logs", "--no-color", "--follow"}
	if lines > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", lines))
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = service.Path

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start log stream: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		w(scanner.Text())
	}

	cmd.Wait()
	return ctx.Err()
}

// GetContainerStats retrieves resource usage stats for Docker Compose containers.
func (e *ComposeExecutor) GetContainerStats(ctx context.Context, service *models.Service) ([]ContainerStats, error) {
	// Get list of container IDs (stdout only: stderr may carry compose warnings)
	psOutput, psErr, err := e.runner()(ctx, service.Path, "docker", "compose", "ps", "-q")
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %s: %w", string(psErr), err)
	}

	containerIDs := strings.Split(strings.TrimSpace(string(psOutput)), "\n")
	if len(containerIDs) == 0 || containerIDs[0] == "" {
		return nil, nil
	}

	var stats []ContainerStats
	for _, id := range containerIDs {
		if id == "" {
			continue
		}
		stat, err := getContainerStats(ctx, id)
		if err != nil {
			continue
		}
		stats = append(stats, stat)
	}

	return stats, nil
}

// ContainerStats holds resource usage statistics for a container.
type ContainerStats struct {
	ContainerID   string  `json:"container_id"`
	ContainerName string  `json:"container_name"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryUsage   string  `json:"memory_usage"`
	MemoryLimit   string  `json:"memory_limit"`
	MemoryPercent float64 `json:"memory_percent"`
	NetIO         string  `json:"net_io"`
	BlockIO       string  `json:"block_io"`
}

func getContainerStats(ctx context.Context, containerID string) (ContainerStats, error) {
	cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format",
		"{{.ID}}\t{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.NetIO}}\t{{.BlockIO}}",
		containerID)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ContainerStats{}, err
	}

	line := strings.TrimSpace(out.String())
	parts := strings.Split(line, "\t")
	if len(parts) < 7 {
		return ContainerStats{}, fmt.Errorf("unexpected stats format")
	}

	cpuPercent := parsePercent(parts[2])
	memParts := strings.SplitN(parts[3], " / ", 2)
	memUsage := ""
	memLimit := ""
	if len(memParts) == 2 {
		memUsage = memParts[0]
		memLimit = memParts[1]
	}
	memPercent := parsePercent(parts[4])

	return ContainerStats{
		ContainerID:   parts[0],
		ContainerName: parts[1],
		CPUPercent:    cpuPercent,
		MemoryUsage:   memUsage,
		MemoryLimit:   memLimit,
		MemoryPercent: memPercent,
		NetIO:         parts[5],
		BlockIO:       parts[6],
	}, nil
}

func parsePercent(s string) float64 {
	s = strings.TrimSuffix(s, "%")
	var val float64
	fmt.Sscanf(s, "%f", &val)
	return val
}
