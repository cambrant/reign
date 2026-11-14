package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reign/internal/executor"
	"github.com/reign/internal/models"
)

// recordingDocker wraps fakeDocker and records every compose command it sees.
type recordingDocker struct {
	*fakeDocker
	mu      sync.Mutex
	cmds    []string
	pullErr error
}

func (r *recordingDocker) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	cmd := strings.Join(args, " ")
	r.mu.Lock()
	r.cmds = append(r.cmds, cmd)
	pullErr := r.pullErr
	r.mu.Unlock()
	if cmd == "compose pull" && pullErr != nil {
		return nil, []byte("manifest unknown"), pullErr
	}
	return r.fakeDocker.run(ctx, dir, name, args...)
}

func (r *recordingDocker) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.cmds...)
}

func newRecordingOrchestrator(t *testing.T, status models.ServiceStatus) (*Orchestrator, *recordingDocker) {
	t.Helper()
	o, fake, _ := newTestOrchestrator(t, status, 0)
	rec := &recordingDocker{fakeDocker: fake}
	o.composeExecutor = executor.NewComposeExecutor(rec.run)
	return o, rec
}

// waitForCommand polls until cmd has been run or the timeout expires.
func waitForCommand(rec *recordingDocker, cmd string) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range rec.commands() {
			if c == cmd {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestStartDoesNotPull(t *testing.T) {
	o, rec := newRecordingOrchestrator(t, models.StatusStopped)

	if err := o.StartService(context.Background(), "app"); err != nil {
		t.Fatalf("StartService: %v", err)
	}
	for _, c := range rec.commands() {
		if c == "compose pull" {
			t.Fatalf("start ran compose pull; commands: %q", rec.commands())
		}
	}
}

func TestRestartWithPull(t *testing.T) {
	tests := []struct {
		name        string
		pullErr     error
		wantRestart bool
	}{
		{"pull succeeds then restarts", nil, true},
		{"failed pull aborts restart", errors.New("exit status 1"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, rec := newRecordingOrchestrator(t, models.StatusRunning)
			rec.pullErr = tt.pullErr

			if _, err := o.RestartServiceAsync(context.Background(), "app", true); err != nil {
				t.Fatalf("RestartServiceAsync: %v", err)
			}
			if !waitForCommand(rec, "compose pull") {
				t.Fatalf("compose pull never ran; commands: %q", rec.commands())
			}

			if tt.wantRestart {
				if !waitForCommand(rec, "compose up -d") {
					t.Fatalf("service was not restarted; commands: %q", rec.commands())
				}
				cmds := rec.commands()
				if cmds[0] != "compose pull" {
					t.Errorf("first command = %q, want compose pull before the stop", cmds[0])
				}
				return
			}

			time.Sleep(100 * time.Millisecond)
			for _, c := range rec.commands() {
				if c == "compose down" || c == "compose up -d" {
					t.Errorf("restart continued after a failed pull: %q", rec.commands())
				}
			}
		})
	}
}

func TestPullServiceRejectsBinary(t *testing.T) {
	o, _, db := newTestOrchestrator(t, models.StatusStopped, 0)
	bin := &models.Service{ID: "bin", Name: "bin", Type: models.ServiceTypeBinary, Command: "/bin/true", Enabled: true}
	if err := bin.Create(db); err != nil {
		t.Fatal(err)
	}
	if err := o.PullService(context.Background(), "bin"); err == nil || !strings.Contains(err.Error(), "only supported") {
		t.Fatalf("PullService(binary) error = %v, want 'only supported'", err)
	}
}

func TestEventsRecordRequester(t *testing.T) {
	o, _, db := newTestOrchestrator(t, models.StatusStopped, 0)
	ctx := WithRequester(context.Background(), "tim@kraut pts/0 [127.0.0.1:5555]")

	if err := o.StartService(ctx, "app"); err != nil {
		t.Fatalf("StartService: %v", err)
	}
	events, err := models.GetServiceEvents(db, "app", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no events logged")
	}
	for _, e := range events {
		if e.Requester != "tim@kraut pts/0 [127.0.0.1:5555]" {
			t.Errorf("event %s requester = %q", e.EventType, e.Requester)
		}
	}
}

func TestHealthCheckEventsAttributedToReign(t *testing.T) {
	o, fake, db := newTestOrchestrator(t, models.StatusRunning, 0)
	fake.running = false

	healthCheck(o)

	events, err := models.GetServiceEvents(db, "app", 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, e := range events {
		seen[e.EventType+"/"+e.Requester] = e.Message
	}
	if _, ok := seen["restarting/"+RequesterAutoRestart]; !ok {
		t.Errorf("no restarting event attributed to %q; events: %v", RequesterAutoRestart, seen)
	}
}
