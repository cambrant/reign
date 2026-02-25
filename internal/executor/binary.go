package executor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/reign/internal/models"
)

// BinaryExecutor executes native binary services.
type BinaryExecutor struct {
	mu        sync.Mutex
	processes map[string]*os.Process
}

var binaryExecutor = &BinaryExecutor{
	processes: make(map[string]*os.Process),
}

// NewBinaryExecutor returns the shared binary executor instance.
func NewBinaryExecutor() *BinaryExecutor {
	return binaryExecutor
}

// Start starts a native binary service.
func (e *BinaryExecutor) Start(ctx context.Context, service *models.Service) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if already running
	if proc, exists := e.processes[service.Name]; exists {
		// Check if process is still alive
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			return fmt.Errorf("service %s is already running", service.Name)
		}
		// Process is dead, clean up
		delete(e.processes, service.Name)
	}

	// Build command with args
	args := parseArgs(service.Command)
	if len(args) == 0 {
		return fmt.Errorf("empty command for service %s", service.Name)
	}

	// Use systemd-cat to send output to journald with service name as identifier
	cmdArgs := []string{"-t", service.Name}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("systemd-cat", cmdArgs...)
	cmd.Dir = service.Path

	// Set up process group for clean termination
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", service.Name, err)
	}

	e.processes[service.Name] = cmd.Process

	// Start a goroutine to wait for the process and clean up
	go func() {
		cmd.Wait()
		e.mu.Lock()
		delete(e.processes, service.Name)
		e.mu.Unlock()
	}()

	return nil
}

// Stop stops a native binary service.
func (e *BinaryExecutor) Stop(ctx context.Context, service *models.Service) error {
	e.mu.Lock()
	proc, exists := e.processes[service.Name]
	e.mu.Unlock()

	if !exists {
		return nil
	}

	// Try graceful shutdown with SIGTERM
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to %s: %w", service.Name, err)
	}

	// Wait for process to exit or context deadline
	done := make(chan error, 1)
	go func() {
		_, err := proc.Wait()
		done <- err
	}()

	select {
	case <-done:
		e.mu.Lock()
		delete(e.processes, service.Name)
		e.mu.Unlock()
		return nil
	case <-ctx.Done():
		// Force kill on timeout
		proc.Signal(syscall.SIGKILL)
		e.mu.Lock()
		delete(e.processes, service.Name)
		e.mu.Unlock()
		return ctx.Err()
	}
}

// Status returns the status of a native binary service.
func (e *BinaryExecutor) Status(ctx context.Context, service *models.Service) (models.ServiceStatus, error) {
	e.mu.Lock()
	proc, exists := e.processes[service.Name]
	e.mu.Unlock()

	if !exists {
		return models.StatusStopped, nil
	}

	// Check if process is still alive
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return models.StatusStopped, nil
	}

	return models.StatusRunning, nil
}

// GetPID returns the PID of a running binary service.
func (e *BinaryExecutor) GetPID(serviceName string) *int {
	e.mu.Lock()
	defer e.mu.Unlock()

	proc, exists := e.processes[serviceName]
	if !exists {
		return nil
	}
	pid := proc.Pid
	return &pid
}

// Pull is a no-op for binary services.
func (e *BinaryExecutor) Pull(ctx context.Context, service *models.Service) error {
	return nil
}

// GetLogs retrieves logs from journald for a binary service.
func (e *BinaryExecutor) GetLogs(ctx context.Context, service *models.Service, lines int) (string, error) {
	args := []string{"-t", service.Name, "--no-pager"}
	if lines > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", lines))
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %s: %w", string(output), err)
	}
	return string(output), nil
}

// FollowLogs streams logs from journald for a binary service.
// It blocks until the context is cancelled.
func (e *BinaryExecutor) FollowLogs(ctx context.Context, service *models.Service, lines int, w func(line string)) error {
	args := []string{"-t", service.Name, "--no-pager", "-f"}
	if lines > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", lines))
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

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

// parseArgs splits a command string into arguments, respecting quotes.
func parseArgs(command string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, ch := range command {
		switch {
		case ch == '"' || ch == '\'':
			if inQuote && ch == quoteChar {
				inQuote = false
				quoteChar = 0
			} else if !inQuote {
				inQuote = true
				quoteChar = ch
			} else {
				current.WriteRune(ch)
			}
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	// Resolve relative paths to absolute
	if len(args) > 0 && !filepath.IsAbs(args[0]) {
		if abs, err := exec.LookPath(args[0]); err == nil {
			args[0] = abs
		}
	}

	return args
}
