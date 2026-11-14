package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reign/internal/executor"
	"github.com/reign/internal/models"
)

// downDocker handles `compose down` on top of fakeDocker, so restarts really
// stop the fake container.
type downDocker struct{ *fakeDocker }

func (d *downDocker) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	if strings.Join(args, " ") == "compose down" {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		return nil, nil, nil
	}
	return d.fakeDocker.run(ctx, dir, name, args...)
}

// Before e3c508c, concurrent `reign restart` calls hit SQLITE_BUSY and could
// leave a service stopped. Restarts of one service now run one after another.
func TestConcurrentRestartsEndRunning(t *testing.T) {
	o, fake, db := newTestOrchestrator(t, models.StatusRunning, 0)
	o.restartPause = 0
	d := &downDocker{fake}
	o.composeExecutor = executor.NewComposeExecutor(d.run)

	const restarts = 5
	var wg sync.WaitGroup
	for i := 0; i < restarts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := o.RestartServiceAsync(context.Background(), "app", false); err != nil {
				t.Errorf("RestartServiceAsync: %v", err)
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for fake.ups() < restarts && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	unlock := o.lockService("app") // wait for the last restart to finish
	unlock()

	if fake.ups() != restarts {
		t.Errorf("compose up ran %d times, want %d", fake.ups(), restarts)
	}
	if s := getState(t, db); s.Status != models.StatusRunning {
		t.Errorf("status after concurrent restarts = %q, want running", s.Status)
	}
	fake.mu.Lock()
	running := fake.running
	fake.mu.Unlock()
	if !running {
		t.Error("service left stopped after concurrent restarts")
	}
}

func TestHealthCheckSkipsServiceWithOperationInProgress(t *testing.T) {
	o, fake, _ := newTestOrchestrator(t, models.StatusRunning, 0)
	fake.running = false

	unlock := o.lockService("app")
	healthCheck(o)
	unlock()

	if fake.ups() != 0 {
		t.Errorf("health check acted on a service with an operation in progress (%d ups)", fake.ups())
	}
}

func TestDisabledRunningServiceStopsAsDisabled(t *testing.T) {
	o, fake, db := newTestOrchestrator(t, models.StatusRunning, 0)
	if _, err := db.Exec(`UPDATE services SET enabled = 0 WHERE id = 'app'`); err != nil {
		t.Fatal(err)
	}
	fake.running = false

	healthCheck(o)

	if s := getState(t, db); s.Status != models.StatusDisabled {
		t.Errorf("status = %q, want disabled", s.Status)
	}
	if fake.ups() != 0 {
		t.Errorf("disabled service was restarted")
	}
}
