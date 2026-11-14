package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/reign/internal/access"
	"github.com/reign/internal/logger"
	"github.com/reign/internal/models"
)

// Binary runner modes.
const (
	// BinaryModeSystemd runs each binary service in its own transient systemd
	// unit (reign-<id>.service). The services are not children of reign, so
	// restarting reign leaves them running, and each gets its own cgroup.
	BinaryModeSystemd = "systemd"
	// BinaryModeDirect runs binary services as child processes of reign,
	// logging through systemd-cat. Restarting reign kills them.
	BinaryModeDirect = "direct"
	// BinaryModeAuto picks systemd when systemd-run is available, else direct.
	BinaryModeAuto = "auto"
)

// ResolveBinaryMode turns a configured mode into the mode to use.
func ResolveBinaryMode(mode string) string {
	switch mode {
	case BinaryModeSystemd, BinaryModeDirect:
		return mode
	}
	if _, err := exec.LookPath("systemd-run"); err == nil {
		return BinaryModeSystemd
	}
	return BinaryModeDirect
}

// BinaryExecutor executes native binary services.
type BinaryExecutor struct {
	mode        string
	run         CommandRunner
	envDir      string         // systemd mode: where per-unit env files are written
	stopTimeout time.Duration  // systemd mode: TimeoutStopSec of the unit
	admins      *access.Admins // may own env files besides root and reign's user

	mu        sync.Mutex
	processes map[string]*os.Process      // direct mode, keyed by service name
	lastExit  map[string]*os.ProcessState // direct mode: how each service last exited
}

var binaryExecutor = &BinaryExecutor{
	mode:      BinaryModeDirect,
	processes: make(map[string]*os.Process),
}

// NewBinaryExecutor returns the shared direct-mode binary executor instance.
func NewBinaryExecutor() *BinaryExecutor {
	return binaryExecutor
}

// BinaryOptions configures a BinaryExecutor.
type BinaryOptions struct {
	Mode        string         // BinaryModeSystemd or BinaryModeDirect (resolve auto first)
	EnvDir      string         // systemd mode: directory for env files (default /run/reign)
	StopTimeout time.Duration  // systemd mode: how long systemd waits before SIGKILL (default 30s)
	Run         CommandRunner  // nil uses the real commands
	Admins      *access.Admins // users who may own env files; nil means root only
}

// NewBinaryExecutorWithOptions returns a binary executor for the given mode.
// Direct mode returns the shared instance, since it owns process state.
func NewBinaryExecutorWithOptions(opts BinaryOptions) *BinaryExecutor {
	if opts.Mode != BinaryModeSystemd {
		binaryExecutor.mu.Lock()
		binaryExecutor.admins = opts.Admins
		binaryExecutor.mu.Unlock()
		return binaryExecutor
	}
	if opts.EnvDir == "" {
		opts.EnvDir = "/run/reign"
	}
	if opts.StopTimeout <= 0 {
		opts.StopTimeout = 30 * time.Second
	}
	return &BinaryExecutor{
		mode:        BinaryModeSystemd,
		run:         opts.Run,
		envDir:      opts.EnvDir,
		stopTimeout: opts.StopTimeout,
		admins:      opts.Admins,
		processes:   make(map[string]*os.Process),
	}
}

// Mode returns the runner mode of the executor.
func (e *BinaryExecutor) Mode() string {
	if e.mode == "" {
		return BinaryModeDirect
	}
	return e.mode
}

func (e *BinaryExecutor) runner() CommandRunner {
	if e.run == nil {
		return execRunner
	}
	return e.run
}

// Start starts a native binary service.
func (e *BinaryExecutor) Start(ctx context.Context, service *models.Service) error {
	args := parseArgs(service.Command)
	if len(args) == 0 {
		return fmt.Errorf("empty command for service %s", service.Name)
	}

	// Use WorkDir as the process working directory, falling back to Path.
	// Validate it up front: a missing working directory makes the child's
	// chdir fail, which surfaces as a misleading exec error rather than the
	// directory that is missing.
	dir, err := workDir(service)
	if err != nil {
		return err
	}

	var envVars []string
	if service.EnvFile != "" {
		e.mu.Lock()
		admins := e.admins
		e.mu.Unlock()
		envVars, err = loadEnvFile(service.EnvFile, admins)
		if err != nil {
			return fmt.Errorf("failed to load env file for %s: %w", service.Name, err)
		}
	}

	if e.Mode() == BinaryModeSystemd {
		return e.startUnit(ctx, service, args, dir, envVars)
	}
	return e.startDirect(service, args, dir, envVars)
}

// workDir returns the validated working directory of a service, or "".
func workDir(service *models.Service) (string, error) {
	if dir := service.WorkDir; dir != "" {
		if err := validateWorkDir(dir); err != nil {
			return "", fmt.Errorf("work dir for service %s: %w", service.Name, err)
		}
		return dir, nil
	}
	if dir := service.Path; dir != "" {
		if err := validateWorkDir(dir); err != nil {
			return "", fmt.Errorf("path for service %s: %w", service.Name, err)
		}
		return dir, nil
	}
	return "", nil
}

// Stop stops a native binary service.
func (e *BinaryExecutor) Stop(ctx context.Context, service *models.Service) error {
	if e.Mode() == BinaryModeSystemd {
		return e.stopUnit(ctx, service)
	}
	return e.stopDirect(ctx, service)
}

// Status returns the status of a native binary service.
func (e *BinaryExecutor) Status(ctx context.Context, service *models.Service) (models.ServiceStatus, error) {
	if e.Mode() == BinaryModeSystemd {
		return e.unitStatus(ctx, service)
	}
	return e.directStatus(service), nil
}

// GetPID returns the PID of a running binary service, or nil.
func (e *BinaryExecutor) GetPID(ctx context.Context, service *models.Service) *int {
	if e.Mode() == BinaryModeSystemd {
		out, err := e.systemctl(ctx, "show", "-p", "MainPID", "--value", UnitName(service.ID))
		if err != nil {
			return nil
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil || pid <= 0 {
			return nil
		}
		return &pid
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	proc, exists := e.processes[service.Name]
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

// --- systemd mode ---

// UnitName returns the transient systemd unit name for a service ID. Characters
// systemd does not allow in unit names are escaped like systemd-escape does.
func UnitName(id string) string {
	var b strings.Builder
	b.WriteString("reign-")
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == ':', c == '.' && i > 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	b.WriteString(".service")
	return b.String()
}

// systemctl runs systemctl and returns stdout. Errors include stderr.
func (e *BinaryExecutor) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	stdout, stderr, err := e.runner()(ctx, "", "systemctl", args...)
	if err != nil {
		return stdout, fmt.Errorf("systemctl %s: %s: %w", args[0], bytes.TrimSpace(stderr), err)
	}
	return stdout, nil
}

// activeState returns the ActiveState of a service's unit ("inactive" if not loaded).
func (e *BinaryExecutor) activeState(ctx context.Context, service *models.Service) (string, error) {
	out, err := e.systemctl(ctx, "show", "-p", "ActiveState", "--value", UnitName(service.ID))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (e *BinaryExecutor) startUnit(ctx context.Context, service *models.Service, args []string, dir string, envVars []string) error {
	unit := UnitName(service.ID)

	state, err := e.activeState(ctx, service)
	if err != nil {
		return err
	}
	switch state {
	case "active", "activating", "reloading":
		return fmt.Errorf("service %s is already running (%s)", service.Name, unit)
	}

	// A unit that failed earlier stays loaded until reset; clear it so the name is free.
	e.systemctl(ctx, "reset-failed", unit)

	runArgs := []string{
		"--unit=" + unit,
		"--quiet",
		"--service-type=exec",
		"--description=reign: " + service.Name,
		"--property=SyslogIdentifier=" + service.Name,
		fmt.Sprintf("--property=TimeoutStopSec=%d", int(e.stopTimeout.Seconds())),
	}
	if dir != "" {
		runArgs = append(runArgs, "--property=WorkingDirectory="+dir)
	}

	// The env file is rewritten in systemd's format rather than passed as is,
	// so values are parsed exactly as loadEnvFile parses them in direct mode.
	// It is only needed while systemd starts the unit, so it is removed after.
	var envPath string
	if len(envVars) > 0 {
		envPath, err = e.writeUnitEnvFile(unit, envVars)
		if err != nil {
			return fmt.Errorf("failed to write env file for %s: %w", service.Name, err)
		}
		defer os.Remove(envPath)
		runArgs = append(runArgs, "--property=EnvironmentFile="+envPath)
	}

	runArgs = append(runArgs, "--")
	runArgs = append(runArgs, args...)

	stdout, stderr, err := e.runner()(ctx, "", "systemd-run", runArgs...)
	if err != nil {
		return fmt.Errorf("failed to start %s: %s%s: %w", service.Name, bytes.TrimSpace(stdout), bytes.TrimSpace(stderr), err)
	}
	return nil
}

func (e *BinaryExecutor) stopUnit(ctx context.Context, service *models.Service) error {
	unit := UnitName(service.ID)
	_, err := e.systemctl(ctx, "stop", unit)
	if err != nil && !strings.Contains(err.Error(), "not loaded") {
		return fmt.Errorf("failed to stop %s: %w", service.Name, err)
	}
	// A unit that exited with an error stays loaded as "failed"; free the name.
	e.systemctl(ctx, "reset-failed", unit)
	return nil
}

func (e *BinaryExecutor) unitStatus(ctx context.Context, service *models.Service) (models.ServiceStatus, error) {
	state, err := e.activeState(ctx, service)
	if err != nil {
		return "", err
	}
	return unitStateToStatus(state), nil
}

// unitStateToStatus maps a systemd ActiveState onto a service status.
func unitStateToStatus(state string) models.ServiceStatus {
	switch state {
	case "active", "reloading":
		return models.StatusRunning
	case "activating":
		return models.StatusStarting
	case "deactivating":
		return models.StatusStopping
	case "failed":
		return models.StatusFailed
	default: // inactive, or not loaded
		return models.StatusStopped
	}
}

// envKeyPattern matches environment variable names systemd accepts.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// writeUnitEnvFile writes KEY=VALUE pairs to a root-only file in systemd's
// EnvironmentFile format and returns its path.
func (e *BinaryExecutor) writeUnitEnvFile(unit string, envVars []string) (string, error) {
	if err := os.MkdirAll(e.envDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(e.envDir, unit+".env")
	if err := os.WriteFile(path, []byte(formatSystemdEnv(envVars)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// formatSystemdEnv renders KEY=VALUE pairs for a systemd EnvironmentFile.
// Values are single-quoted (taken literally by systemd) unless they contain a
// single quote; then they are double-quoted with \ " ` $ escaped.
func formatSystemdEnv(envVars []string) string {
	var b strings.Builder
	for _, kv := range envVars {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || !envKeyPattern.MatchString(key) {
			logger.Warn("Skipping invalid environment variable name %q", key)
			continue
		}
		if !strings.Contains(val, "'") {
			fmt.Fprintf(&b, "%s='%s'\n", key, val)
			continue
		}
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
		fmt.Fprintf(&b, "%s=\"%s\"\n", key, r.Replace(val))
	}
	return b.String()
}

// --- direct mode ---

func (e *BinaryExecutor) startDirect(service *models.Service, args []string, dir string, envVars []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.processes == nil {
		e.processes = make(map[string]*os.Process)
	}

	// Check if already running
	if proc, exists := e.processes[service.Name]; exists {
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			return fmt.Errorf("service %s is already running", service.Name)
		}
		delete(e.processes, service.Name)
	}

	// Use systemd-cat to send output to journald with service name as identifier
	cmdArgs := append([]string{"-t", service.Name}, args...)
	cmd := exec.Command("systemd-cat", cmdArgs...)
	cmd.Dir = dir
	if len(envVars) > 0 {
		cmd.Env = append(os.Environ(), envVars...)
	}

	// Set up process group for clean termination
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", service.Name, err)
	}

	e.processes[service.Name] = cmd.Process

	// Wait for the process and clean up
	go func() {
		cmd.Wait()
		e.mu.Lock()
		delete(e.processes, service.Name)
		e.recordExit(service.Name, cmd.ProcessState)
		e.mu.Unlock()
	}()

	return nil
}

func (e *BinaryExecutor) stopDirect(ctx context.Context, service *models.Service) error {
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

func (e *BinaryExecutor) directStatus(service *models.Service) models.ServiceStatus {
	e.mu.Lock()
	proc, exists := e.processes[service.Name]
	e.mu.Unlock()

	if !exists {
		return models.StatusStopped
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return models.StatusStopped
	}
	return models.StatusRunning
}

// --- logs ---

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

// validateWorkDir returns a clear error if dir does not exist or is not a
// directory, so callers get an actionable message instead of the runtime's
// misleading "fork/exec .../systemd-cat: no such file or directory".
func validateWorkDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("working directory %q does not exist", dir)
		}
		return fmt.Errorf("cannot access working directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory %q is not a directory", dir)
	}
	return nil
}

// loadEnvFile reads a file of environment variable exports and returns them
// as a slice of "KEY=VALUE" strings suitable for use with exec.Cmd.Env.
// Lines beginning with '#' and blank lines are ignored. The 'export ' prefix
// is stripped if present. The file must pass access.OpenEnvFile's checks.
func loadEnvFile(path string, admins *access.Admins) ([]string, error) {
	f, err := access.OpenEnvFile(path, admins)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var vars []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eqIdx := strings.Index(line, "=")
		if eqIdx < 0 {
			continue
		}
		key := line[:eqIdx]
		val := line[eqIdx+1:]
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		vars = append(vars, key+"="+val)
	}
	return vars, scanner.Err()
}
