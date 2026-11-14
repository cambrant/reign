package orchestrator

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/reign/internal/executor"
	"github.com/reign/internal/models"
)

func TestContainerEventTriggersCheck(t *testing.T) {
	o, fake, db := newTestOrchestrator(t, models.StatusRunning, 0)
	o.eventDebounce = 0
	o.config.ExternalStopGrace = time.Hour

	// The fake config has no project name; give the service a real directory
	// and a config that names its project.
	dir := t.TempDir()
	os.WriteFile(dir+"/compose.yml", []byte("services: {}\n"), 0o644)
	if _, err := db.Exec(`UPDATE services SET path = ? WHERE id = 'app'`, dir); err != nil {
		t.Fatal(err)
	}
	named := &namedDocker{fakeDocker: fake, project: "app"}
	o.composeExecutor = executor.NewComposeExecutor(named.run)

	fake.mu.Lock()
	fake.running = false // the container died
	fake.mu.Unlock()

	o.handleContainerEvent(context.Background(), executor.ContainerEvent{Project: "other", Action: "die"})
	o.handleContainerEvent(context.Background(), executor.ContainerEvent{Project: "app", Action: "die"})
	o.handleContainerEvent(context.Background(), executor.ContainerEvent{Project: "app", Action: "stop"})
	o.wg.Wait()

	if fake.ups() != 1 {
		t.Errorf("compose up ran %d times after a die event, want 1 (one debounced check, one restart)", fake.ups())
	}
}

// namedDocker answers compose config with a project name.
type namedDocker struct {
	*fakeDocker
	project string
}

func (n *namedDocker) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	if len(args) >= 2 && args[0] == "compose" && args[1] == "config" {
		return []byte(`{"name":"` + n.project + `","services":{"app":{"restart":"on-failure"}}}`), nil, nil
	}
	return n.fakeDocker.run(ctx, dir, name, args...)
}
