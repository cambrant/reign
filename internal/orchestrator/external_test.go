package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/reign/internal/executor"
	"github.com/reign/internal/models"
)

// stopCauseDocker extends fakeDocker with the commands StopCause runs.
type stopCauseDocker struct {
	*fakeDocker
	inspect string // docker inspect output for the app container
	events  string // docker events output
}

func (s *stopCauseDocker) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	switch {
	case strings.Join(args, " ") == "compose ps -a -q":
		return []byte("abc123\n"), nil, nil
	case args[0] == "inspect":
		return []byte(s.inspect), nil, nil
	case args[0] == "events":
		return []byte(s.events), nil, nil
	}
	return s.fakeDocker.run(ctx, dir, name, args...)
}

func newStopCauseOrchestrator(t *testing.T, inspect, events string) (*Orchestrator, *stopCauseDocker) {
	t.Helper()
	o, fake, _ := newTestOrchestrator(t, models.StatusRunning, 0)
	fake.running = false
	d := &stopCauseDocker{fakeDocker: fake, inspect: inspect, events: events}
	o.composeExecutor = executor.NewComposeExecutor(d.run)
	return o, d
}

func lastEvent(t *testing.T, o *Orchestrator, eventType string) *models.ServiceEvent {
	t.Helper()
	events, err := models.GetServiceEvents(o.db, "app", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.EventType == eventType {
			return &e
		}
	}
	return nil
}

func TestExternalStopIsNotRestartedDuringGrace(t *testing.T) {
	o, d := newStopCauseOrchestrator(t, "/app-app-1|exited|0|false|app", "kill app-app-1\nstop app-app-1\n")
	o.config.ExternalStopGrace = time.Hour

	healthCheck(o)
	healthCheck(o)

	if d.ups() != 0 {
		t.Errorf("compose up called %d times during the grace period, want 0", d.ups())
	}
	if s := getState(t, o.db); s.Status != models.StatusStopped {
		t.Errorf("status = %q, want stopped", s.Status)
	}
	e := lastEvent(t, o, "stopped_externally")
	if e == nil || !strings.Contains(e.Message, "stopped outside reign") {
		t.Fatalf("stopped_externally event = %+v", e)
	}
	if lastEvent(t, o, "failed") != nil {
		t.Error("external stop was logged as a failure")
	}
}

func TestExternallyStoppedServiceResumes(t *testing.T) {
	o, d := newStopCauseOrchestrator(t, "/app-app-1|exited|0|false|app", "stop app-app-1\n")

	healthCheck(o)
	d.mu.Lock()
	d.running = true // maintenance done: someone ran docker compose start
	d.mu.Unlock()
	healthCheck(o)

	if s := getState(t, o.db); s.Status != models.StatusRunning {
		t.Errorf("status = %q, want running", s.Status)
	}
	if lastEvent(t, o, "resumed") == nil {
		t.Error("no resumed event")
	}
	if d.ups() != 0 {
		t.Errorf("compose up called %d times, want 0", d.ups())
	}
}

func TestExternalStopRestartedAfterGrace(t *testing.T) {
	o, d := newStopCauseOrchestrator(t, "/app-app-1|exited|0|false|app", "stop app-app-1\n")
	o.config.ExternalStopGrace = 10 * time.Millisecond

	healthCheck(o)
	time.Sleep(20 * time.Millisecond)
	healthCheck(o)

	if d.ups() != 1 {
		t.Errorf("compose up called %d times after the grace period, want 1", d.ups())
	}
}

func TestExternalStopWithZeroGraceStaysStopped(t *testing.T) {
	o, d := newStopCauseOrchestrator(t, "/app-app-1|exited|0|false|app", "stop app-app-1\n")
	o.config.ExternalStopGrace = 0

	healthCheck(o)
	healthCheck(o)

	if d.ups() != 0 {
		t.Errorf("compose up called %d times, want 0", d.ups())
	}
	if e := lastEvent(t, o, "stopped_externally"); e == nil || !strings.Contains(e.Message, "not restarting") {
		t.Errorf("stopped_externally event = %+v", e)
	}
}

func TestOOMKillIsACrash(t *testing.T) {
	o, d := newStopCauseOrchestrator(t, "/app-app-1|exited|137|true|app", "")

	healthCheck(o)

	e := lastEvent(t, o, "failed")
	if e == nil || !strings.Contains(e.Message, "OOMKilled=true") {
		t.Fatalf("failed event = %+v, want OOMKilled in the message", e)
	}
	if d.ups() != 1 {
		t.Errorf("compose up called %d times, want 1 (crashes are restarted)", d.ups())
	}
}

func TestManualStartClearsExternalStop(t *testing.T) {
	o, _ := newStopCauseOrchestrator(t, "/app-app-1|exited|0|false|app", "stop app-app-1\n")

	healthCheck(o)
	if err := o.StartService(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}

	o.pendingMu.Lock()
	_, watched := o.externalStops["app"]
	o.pendingMu.Unlock()
	if watched {
		t.Error("external stop still tracked after a manual start")
	}
}
