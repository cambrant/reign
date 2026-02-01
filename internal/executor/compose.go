package executor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/reign/internal/models"
)

// ComposeExecutor executes Docker Compose services.
type ComposeExecutor struct{}

// Start starts a Docker Compose service.
func (e *ComposeExecutor) Start(ctx context.Context, service *models.Service) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "up", "-d")
	cmd.Dir = service.Path
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up failed: %s: %w", string(output), err)
	}
	return nil
}

// Stop stops a Docker Compose service.
func (e *ComposeExecutor) Stop(ctx context.Context, service *models.Service) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "down")
	cmd.Dir = service.Path
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down failed: %s: %w", string(output), err)
	}
	return nil
}

// Status returns the status of a Docker Compose service.
func (e *ComposeExecutor) Status(ctx context.Context, service *models.Service) (models.ServiceStatus, error) {
	// Check if any containers are running
	cmd := exec.CommandContext(ctx, "docker", "compose", "ps", "--format", "json")
	cmd.Dir = service.Path
	output, err := cmd.CombinedOutput()
	if err != nil {
		return models.StatusStopped, nil
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" || outputStr == "[]" {
		return models.StatusStopped, nil
	}

	// Check container health status
	healthCmd := exec.CommandContext(ctx, "docker", "compose", "ps", "--format", "{{.Health}}")
	healthCmd.Dir = service.Path
	healthOutput, _ := healthCmd.CombinedOutput()
	healthStr := strings.TrimSpace(string(healthOutput))

	// If any container reports unhealthy, report as failed
	if strings.Contains(healthStr, "unhealthy") {
		return models.StatusFailed, nil
	}

	// If health checks are defined and all healthy, or no health checks
	if strings.Contains(healthStr, "starting") {
		return models.StatusStarting, nil
	}

	return models.StatusRunning, nil
}

// Pull pulls the latest images for a Docker Compose service.
func (e *ComposeExecutor) Pull(ctx context.Context, service *models.Service) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "pull")
	cmd.Dir = service.Path
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose pull failed: %s: %w", string(output), err)
	}
	return nil
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

// GetContainerStats retrieves resource usage stats for Docker Compose containers.
func (e *ComposeExecutor) GetContainerStats(ctx context.Context, service *models.Service) ([]ContainerStats, error) {
	// Get list of container IDs
	psCmd := exec.CommandContext(ctx, "docker", "compose", "ps", "-q")
	psCmd.Dir = service.Path
	psOutput, err := psCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %s: %w", string(psOutput), err)
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
