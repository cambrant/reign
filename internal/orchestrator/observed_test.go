package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/reign/internal/executor"
	"github.com/reign/internal/models"
)

// countingDocker counts `compose ps` calls on top of fakeDocker.
type countingDocker struct {
	*fakeDocker
	mu sync.Mutex
	ps int
}

func (c *countingDocker) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	if strings.Join(args, " ") == "compose ps -a --format json" {
		c.mu.Lock()
		c.ps++
		c.mu.Unlock()
	}
	return c.fakeDocker.run(ctx, dir, name, args...)
}

func TestGetServiceStatusServedFromLastCheck(t *testing.T) {
	o, fake, _ := newTestOrchestrator(t, models.StatusRunning, 0)
	c := &countingDocker{fakeDocker: fake}
	o.composeExecutor = executor.NewComposeExecutor(c.run)

	healthCheck(o) // observes "running"
	if c.ps != 1 {
		t.Fatalf("health check ran compose ps %d times, want 1", c.ps)
	}

	tests := []struct {
		name    string
		fresh   bool
		wantPS  int
		running bool
		want    models.ServiceStatus
	}{
		{"cached answer does not probe", false, 1, false, models.StatusRunning},
		{"fresh answer probes", true, 2, false, models.StatusStopped},
		{"cache holds the fresh answer", false, 2, true, models.StatusStopped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.mu.Lock()
			fake.running = tt.running
			fake.mu.Unlock()
			obs, err := o.GetServiceStatus(context.Background(), "app", tt.fresh)
			if err != nil {
				t.Fatal(err)
			}
			if obs.Status != tt.want || obs.CheckedAt.IsZero() {
				t.Errorf("observation = %+v, want status %q", obs, tt.want)
			}
			if c.ps != tt.wantPS {
				t.Errorf("compose ps ran %d times, want %d", c.ps, tt.wantPS)
			}
		})
	}
}

func TestGetServiceStatusUnknownService(t *testing.T) {
	o, _, _ := newTestOrchestrator(t, models.StatusRunning, 0)
	if _, err := o.GetServiceStatus(context.Background(), "nope", false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not found", err)
	}
}
