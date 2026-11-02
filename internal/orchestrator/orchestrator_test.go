package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reign/internal/config"
	"github.com/reign/internal/executor"
	"github.com/reign/internal/models"
)

const composeWarning = `level=warning msg="compose.yml: the attribute ` + "`version`" + ` is obsolete"`

// fakeDocker simulates one compose project with a single "app" service.
type fakeDocker struct {
	mu      sync.Mutex
	running bool  // whether the app container is running
	psErr   error // simulates an unreachable docker daemon
	upErr   error // makes `compose up` fail
	upCalls int
}

func (f *fakeDocker) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch strings.Join(args, " ") {
	case "compose config --format json":
		return []byte(`{"services":{"app":{"restart":"on-failure"}}}`), []byte(composeWarning), nil
	case "compose ps -a --format json":
		if f.psErr != nil {
			return nil, []byte("Cannot connect to the Docker daemon"), f.psErr
		}
		state := "exited"
		if f.running {
			state = "running"
		}
		line := fmt.Sprintf(`{"Service":"app","State":%q,"Health":"","ExitCode":0,"Labels":""}`, state)
		return []byte(line), []byte(composeWarning), nil
	case "compose pull":
		return nil, nil, nil
	case "compose up -d":
		f.upCalls++
		if f.upErr != nil {
			return nil, []byte("Error response from daemon"), f.upErr
		}
		f.running = true
		return nil, nil, nil
	}
	return nil, nil, fmt.Errorf("unexpected command: %s %v", name, args)
}

func (f *fakeDocker) ups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upCalls
}

// newTestOrchestrator returns an orchestrator with one enabled compose service "app"
// in the given state, backed by fake docker.
func newTestOrchestrator(t *testing.T, status models.ServiceStatus, restartCount int) (*Orchestrator, *fakeDocker, *sql.DB) {
	t.Helper()

	db, err := models.InitDB(filepath.Join(t.TempDir(), "reign.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	svc := &models.Service{ID: "app", Name: "app", Type: models.ServiceTypeCompose, Path: "/srv/app", Enabled: true}
	if err := svc.Create(db); err != nil {
		t.Fatalf("create service: %v", err)
	}
	if _, err := db.Exec(`UPDATE service_state SET status = ?, restart_count = ?, started_at = datetime('now') WHERE service_id = 'app'`,
		status, restartCount); err != nil {
		t.Fatalf("set state: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.RestartDelay = time.Millisecond
	cfg.MaxRestarts = 3

	fake := &fakeDocker{running: status == models.StatusRunning}
	o := New(db, cfg)
	o.composeExecutor = executor.NewComposeExecutor(fake.run)
	return o, fake, db
}

func getState(t *testing.T, db *sql.DB) *models.ServiceState {
	t.Helper()
	state, err := models.GetServiceState(db, "app")
	if err != nil || state == nil {
		t.Fatalf("GetServiceState: %v", err)
	}
	return state
}

// healthCheck runs one health check round and waits for any restarts it scheduled.
func healthCheck(o *Orchestrator) {
	o.runHealthChecks(context.Background())
	o.wg.Wait()
}

// The 2026-11-02 incident: after a docker restart the project's containers had exited,
// and compose printed a deprecation warning on stderr. Reign must notice and restart it.
func TestHealthCheckRestartsExitedProject(t *testing.T) {
	o, fake, db := newTestOrchestrator(t, models.StatusRunning, 0)
	fake.running = false

	healthCheck(o)

	if fake.ups() != 1 {
		t.Fatalf("compose up called %d times, want 1", fake.ups())
	}
	state := getState(t, db)
	if state.Status != models.StatusRunning {
		t.Errorf("status = %q, want running", state.Status)
	}
	if state.RestartCount != 1 {
		t.Errorf("restart count = %d, want 1", state.RestartCount)
	}
}

func TestHealthCheckIgnoresUnknownState(t *testing.T) {
	o, fake, db := newTestOrchestrator(t, models.StatusRunning, 0)
	fake.psErr = errors.New("exit status 1") // docker daemon restarting

	healthCheck(o)

	if fake.ups() != 0 {
		t.Errorf("compose up called %d times while docker was unreachable, want 0", fake.ups())
	}
	if state := getState(t, db); state.Status != models.StatusRunning {
		t.Errorf("status = %q, want it left as running", state.Status)
	}
}

// gatus: a start that failed once (transient cgroup error) must be retried.
func TestHealthCheckRetriesFailedService(t *testing.T) {
	o, fake, db := newTestOrchestrator(t, models.StatusFailed, 0)

	healthCheck(o)

	if fake.ups() != 1 {
		t.Fatalf("compose up called %d times, want 1", fake.ups())
	}
	if state := getState(t, db); state.Status != models.StatusRunning {
		t.Errorf("status = %q, want running", state.Status)
	}
}

func TestHealthCheckDoesNotStackRestarts(t *testing.T) {
	o, fake, _ := newTestOrchestrator(t, models.StatusFailed, 0)
	o.config.RestartDelay = 50 * time.Millisecond

	// Several health ticks fire before the scheduled restart runs.
	o.runHealthChecks(context.Background())
	o.runHealthChecks(context.Background())
	o.runHealthChecks(context.Background())
	o.wg.Wait()

	if fake.ups() != 1 {
		t.Errorf("compose up called %d times, want 1", fake.ups())
	}
}

func TestHealthCheckRespectsMaxRestarts(t *testing.T) {
	o, fake, _ := newTestOrchestrator(t, models.StatusFailed, 3) // MaxRestarts is 3

	healthCheck(o)

	if fake.ups() != 0 {
		t.Errorf("compose up called %d times at the restart limit, want 0", fake.ups())
	}
}

// Automatic restarts must not reset the counter, or MaxRestarts never takes effect.
func TestFailingServiceStopsAtMaxRestarts(t *testing.T) {
	o, fake, db := newTestOrchestrator(t, models.StatusFailed, 0)
	fake.upErr = errors.New("exit status 1")

	for i := 0; i < 10; i++ {
		healthCheck(o)
	}

	if fake.ups() != 3 {
		t.Errorf("compose up called %d times, want MaxRestarts (3)", fake.ups())
	}
	state := getState(t, db)
	if state.Status != models.StatusFailed {
		t.Errorf("status = %q, want failed", state.Status)
	}
	if state.RestartCount != 3 {
		t.Errorf("restart count = %d, want 3", state.RestartCount)
	}
}

func TestManualStartResetsRestartCount(t *testing.T) {
	o, _, db := newTestOrchestrator(t, models.StatusFailed, 3)

	if err := o.StartService(context.Background(), "app"); err != nil {
		t.Fatalf("StartService: %v", err)
	}

	if state := getState(t, db); state.RestartCount != 0 {
		t.Errorf("restart count = %d after manual start, want 0", state.RestartCount)
	}
}

func TestStableServiceResetsRestartCount(t *testing.T) {
	tests := []struct {
		name      string
		startedAt string
		want      int
	}{
		{"recently restarted keeps count", "datetime('now')", 2},
		{"stable for an hour resets count", "datetime('now', '-1 hour')", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, _, db := newTestOrchestrator(t, models.StatusRunning, 2)
			if _, err := db.Exec(`UPDATE service_state SET started_at = ` + tt.startedAt + ` WHERE service_id = 'app'`); err != nil {
				t.Fatal(err)
			}

			healthCheck(o)

			if state := getState(t, db); state.RestartCount != tt.want {
				t.Errorf("restart count = %d, want %d", state.RestartCount, tt.want)
			}
		})
	}
}

func TestRestartBackoff(t *testing.T) {
	base := 30 * time.Second
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 30 * time.Second},
		{1, time.Minute},
		{3, 4 * time.Minute},
		{5, maxRestartBackoff}, // 16m, capped
		{100, maxRestartBackoff},
	}
	for _, tt := range tests {
		if got := restartBackoff(base, tt.attempts); got != tt.want {
			t.Errorf("restartBackoff(%s, %d) = %s, want %s", base, tt.attempts, got, tt.want)
		}
	}
}
