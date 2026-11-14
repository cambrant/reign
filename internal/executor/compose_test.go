package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reign/internal/models"
)

const versionWarning = `time="2026-11-02T20:05:50Z" level=warning msg="/srv/app/compose.yml: the attribute ` +
	"`version`" + ` is obsolete, it will be ignored, please remove it to avoid potential confusion"`

// fakeResult is the canned output of one fake command.
type fakeResult struct {
	stdout, stderr string
	err            error
}

// fakeRunner answers commands by their joined arguments, e.g. "compose ps -a --format json".
func fakeRunner(results map[string]fakeResult) CommandRunner {
	return func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
		key := strings.Join(args, " ")
		r, ok := results[key]
		if !ok {
			return nil, nil, fmt.Errorf("unexpected command: %s %s", name, key)
		}
		return []byte(r.stdout), []byte(r.stderr), r.err
	}
}

// psLine renders one container as compose v2.21+ prints it (one JSON object per line).
func psLine(service, state, health string, exitCode int, oneoff bool) string {
	return fmt.Sprintf(`{"Service":%q,"State":%q,"Health":%q,"ExitCode":%d,"Labels":"com.docker.compose.project=app,com.docker.compose.oneoff=%s"}`,
		service, state, health, exitCode, map[bool]string{true: "True", false: "False"}[oneoff])
}

func TestComposeStatus(t *testing.T) {
	webDB := `{"services":{"web":{"restart":"unless-stopped"},"db":{"restart":"on-failure"}}}`

	tests := []struct {
		name    string
		config  string
		ps      fakeResult
		want    models.ServiceStatus
		wantErr bool
	}{
		{
			// The 2026-11-02 incident: everything exited after a docker restart, and the
			// obsolete-version warning on stderr used to make this look "running".
			name:   "all exited with compose warning on stderr",
			config: webDB,
			ps: fakeResult{
				stdout: psLine("web", "exited", "", 0, false) + "\n" + psLine("db", "exited", "", 0, false),
				stderr: versionWarning,
			},
			want: models.StatusStopped,
		},
		{
			name:   "no containers, only a warning",
			config: webDB,
			ps:     fakeResult{stderr: versionWarning},
			want:   models.StatusStopped,
		},
		{
			name:   "all running",
			config: webDB,
			ps:     fakeResult{stdout: psLine("web", "running", "", 0, false) + "\n" + psLine("db", "running", "healthy", 0, false)},
			want:   models.StatusRunning,
		},
		{
			// cambraboard: the app runs but its database shut down cleanly and on-failure kept it down
			name:   "one service exited cleanly with on-failure policy",
			config: webDB,
			ps:     fakeResult{stdout: psLine("web", "running", "", 0, false) + "\n" + psLine("db", "exited", "", 0, false)},
			want:   models.StatusFailed,
		},
		{
			name:   "one service has no container at all",
			config: webDB,
			ps:     fakeResult{stdout: psLine("web", "running", "", 0, false)},
			want:   models.StatusFailed,
		},
		{
			// windmill: 2 of 3 worker replicas up
			name:   "one replica down",
			config: `{"services":{"worker":{"restart":"unless-stopped","deploy":{"replicas":3}}}}`,
			ps: fakeResult{stdout: strings.Join([]string{
				psLine("worker", "running", "", 0, false),
				psLine("worker", "exited", "", 1, false),
				psLine("worker", "running", "", 0, false),
			}, "\n")},
			want: models.StatusFailed,
		},
		{
			name:   "service scaled to zero is not expected",
			config: `{"services":{"web":{"restart":"always"},"extra":{"deploy":{"replicas":0}}}}`,
			ps:     fakeResult{stdout: psLine("web", "running", "", 0, false)},
			want:   models.StatusRunning,
		},
		{
			// agenthub vikunja-init: no restart policy, ran once, exited 0
			name:   "one-shot init service completed",
			config: `{"services":{"app":{"restart":"unless-stopped"},"init":{}}}`,
			ps:     fakeResult{stdout: psLine("app", "running", "", 0, false) + "\n" + psLine("init", "exited", "", 0, false)},
			want:   models.StatusRunning,
		},
		{
			name:   "one-shot init service failed",
			config: `{"services":{"app":{"restart":"unless-stopped"},"init":{"restart":"no"}}}`,
			ps:     fakeResult{stdout: psLine("app", "running", "", 0, false) + "\n" + psLine("init", "exited", "", 1, false)},
			want:   models.StatusFailed,
		},
		{
			name:   "project of only completed one-shots",
			config: `{"services":{"migrate":{}}}`,
			ps:     fakeResult{stdout: psLine("migrate", "exited", "", 0, false)},
			want:   models.StatusRunning,
		},
		{
			// archivebox: a months-old `docker compose run` leftover must not count
			name:   "exited one-off run container is ignored",
			config: `{"services":{"app":{"restart":"unless-stopped"}}}`,
			ps:     fakeResult{stdout: psLine("app", "running", "", 0, false) + "\n" + psLine("app", "exited", "", 130, true)},
			want:   models.StatusRunning,
		},
		{
			name:   "unhealthy container",
			config: webDB,
			ps:     fakeResult{stdout: psLine("web", "running", "unhealthy", 0, false) + "\n" + psLine("db", "running", "", 0, false)},
			want:   models.StatusFailed,
		},
		{
			name:   "health check still starting",
			config: webDB,
			ps:     fakeResult{stdout: psLine("web", "running", "starting", 0, false) + "\n" + psLine("db", "running", "", 0, false)},
			want:   models.StatusStarting,
		},
		{
			name:   "restarting container",
			config: `{"services":{"mc":{"restart":"unless-stopped"}}}`,
			ps:     fakeResult{stdout: psLine("mc", "restarting", "", 1, false)},
			want:   models.StatusStarting,
		},
		{
			name:   "legacy JSON array output",
			config: webDB,
			ps:     fakeResult{stdout: "[" + psLine("web", "running", "", 0, false) + "," + psLine("db", "running", "", 0, false) + "]"},
			want:   models.StatusRunning,
		},
		{
			// docker daemon down: the state is unknown, not "stopped"
			name:    "compose ps fails",
			config:  webDB,
			ps:      fakeResult{stderr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock", err: errors.New("exit status 1")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewComposeExecutor(fakeRunner(map[string]fakeResult{
				"compose config --format json": {stdout: tt.config, stderr: versionWarning},
				"compose ps -a --format json":  tt.ps,
			}))
			got, err := e.Status(context.Background(), &models.Service{ID: "app", Path: "/srv/app"})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got status %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComposeStatusConfigError(t *testing.T) {
	e := NewComposeExecutor(fakeRunner(map[string]fakeResult{
		"compose config --format json": {stderr: "no configuration file provided: not found", err: errors.New("exit status 1")},
	}))
	if got, err := e.Status(context.Background(), &models.Service{ID: "gone", Path: "/srv/gone"}); err == nil {
		t.Fatalf("expected an error for a missing compose file, got status %q", got)
	}
}

func TestComposeStartErrorIncludesStderr(t *testing.T) {
	e := NewComposeExecutor(fakeRunner(map[string]fakeResult{
		"compose up -d": {stderr: "Error response from daemon: unable to apply cgroup configuration", err: errors.New("exit status 1")},
	}))
	err := e.Start(context.Background(), &models.Service{ID: "gatus", Path: "/srv/gatus"})
	if err == nil || !strings.Contains(err.Error(), "cgroup") {
		t.Fatalf("expected start error carrying stderr, got %v", err)
	}
}

func TestComposeStartPullPolicy(t *testing.T) {
	tests := []struct {
		policy models.PullPolicy
		want   string
	}{
		{"", "compose up -d"},
		{models.PullMissing, "compose up -d"},
		{models.PullAlways, "compose up -d --pull always"},
		{models.PullNever, "compose up -d --pull never"},
	}
	for _, tt := range tests {
		t.Run(string(tt.policy), func(t *testing.T) {
			var got []string
			e := NewComposeExecutor(func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
				got = append(got, strings.Join(args, " "))
				return nil, nil, nil
			})
			if err := e.Start(context.Background(), &models.Service{ID: "app", Path: "/srv/app", PullPolicy: tt.policy}); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("commands = %q, want only %q", got, tt.want)
			}
		})
	}
}

func TestComposeConfigCache(t *testing.T) {
	dir := t.TempDir()
	composeFile := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configCalls := 0
	e := NewComposeExecutor(func(ctx context.Context, d, name string, args ...string) ([]byte, []byte, error) {
		switch strings.Join(args, " ") {
		case "compose config --format json":
			configCalls++
			return []byte(`{"name":"app","services":{"web":{"restart":"always"}}}`), nil, nil
		case "compose ps -a --format json":
			return []byte(psLine("web", "running", "", 0, false)), nil, nil
		}
		return nil, nil, fmt.Errorf("unexpected %v", args)
	})
	svc := &models.Service{ID: "app", Path: dir}

	for i := 0; i < 3; i++ {
		if _, err := e.Status(context.Background(), svc); err != nil {
			t.Fatal(err)
		}
	}
	if configCalls != 1 {
		t.Fatalf("compose config ran %d times for an unchanged project, want 1", configCalls)
	}

	// Editing a file in the project invalidates the cache.
	later := time.Now().Add(time.Minute)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(composeFile, later, later)
	if _, err := e.Status(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if configCalls != 2 {
		t.Errorf("compose config ran %d times after an edit, want 2", configCalls)
	}
}

func TestGetContainerStatsBatched(t *testing.T) {
	statsCalls := 0
	e := NewComposeExecutor(func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
		switch {
		case strings.Join(args, " ") == "compose ps -q":
			return []byte("aaa\nbbb\n"), []byte(versionWarning), nil
		case args[0] == "stats":
			statsCalls++
			if got := args[len(args)-2:]; got[0] != "aaa" || got[1] != "bbb" {
				t.Errorf("stats called for %v, want both containers in one call", got)
			}
			return []byte("aaa\tapp-web-1\t1.50%\t10MiB / 1GiB\t0.98%\t1kB / 2kB\t0B / 0B\n" +
				"bbb\tapp-db-1\t0.20%\t200MiB / 1GiB\t19.53%\t3kB / 4kB\t1MB / 2MB\n"), nil, nil
		}
		return nil, nil, fmt.Errorf("unexpected %v", args)
	})
	svc := &models.Service{ID: "app", Path: "/srv/app"}

	for i := 0; i < 3; i++ {
		stats, err := e.GetContainerStats(context.Background(), svc)
		if err != nil {
			t.Fatal(err)
		}
		if len(stats) != 2 || stats[1].ContainerName != "app-db-1" || stats[1].MemoryUsage != "200MiB" || stats[0].CPUPercent != 1.5 {
			t.Fatalf("stats = %+v", stats)
		}
	}
	if statsCalls != 1 {
		t.Errorf("docker stats ran %d times, want 1 (one call, then cached)", statsCalls)
	}
}
