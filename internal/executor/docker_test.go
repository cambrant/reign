package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/reign/internal/models"
)

// fakeDockerAPI serves GET /containers/json with the given containers.
func fakeDockerAPI(t *testing.T, containers []apiContainer, calls *int32) *DockerClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/json" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(calls, 1)
		if r.URL.Query().Get("all") != "1" || !strings.Contains(r.URL.Query().Get("filters"), labelProject) {
			t.Errorf("unexpected query %s", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode(containers)
	}))
	t.Cleanup(srv.Close)
	return &DockerClient{http: srv.Client(), baseURL: srv.URL}
}

func apiCtr(project, service, state, status string, oneoff bool) apiContainer {
	o := "False"
	if oneoff {
		o = "True"
	}
	return apiContainer{ID: project + service, State: state, Status: status,
		Labels: map[string]string{labelProject: project, labelService: service, labelOneOff: o}}
}

func TestAPIContainerToPSEntry(t *testing.T) {
	tests := []struct {
		name       string
		c          apiContainer
		wantHealth string
		wantExit   int
		wantOneOff bool
	}{
		{"healthy", apiCtr("p", "web", "running", "Up 2 hours (healthy)", false), "healthy", 0, false},
		{"unhealthy", apiCtr("p", "web", "running", "Up 2 hours (unhealthy)", false), "unhealthy", 0, false},
		{"health starting", apiCtr("p", "web", "running", "Up 3 seconds (health: starting)", false), "starting", 0, false},
		{"exited 137", apiCtr("p", "db", "exited", "Exited (137) 5 minutes ago", false), "", 137, false},
		{"one-off", apiCtr("p", "web", "exited", "Exited (0) 2 months ago", true), "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.c.psEntry()
			if p.Health != tt.wantHealth || p.ExitCode != tt.wantExit || p.oneOff() != tt.wantOneOff {
				t.Errorf("psEntry = %+v, want health=%q exit=%d oneoff=%v", p, tt.wantHealth, tt.wantExit, tt.wantOneOff)
			}
		})
	}
}

func TestComposeStatusViaDockerAPI(t *testing.T) {
	var apiCalls int32
	docker := fakeDockerAPI(t, []apiContainer{
		apiCtr("shop", "web", "running", "Up 1 hour", false),
		apiCtr("shop", "db", "running", "Up 1 hour (healthy)", false),
		apiCtr("blog", "app", "exited", "Exited (1) 2 minutes ago", false),
	}, &apiCalls)

	configs := map[string]string{
		"/srv/shop": `{"name":"shop","services":{"web":{"restart":"always"},"db":{"restart":"always"}}}`,
		"/srv/blog": `{"name":"blog","services":{"app":{"restart":"always"}}}`,
	}
	psCalls := 0
	e := NewComposeExecutorWithDocker(docker)
	e.run = func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
		switch strings.Join(args, " ") {
		case "compose config --format json":
			return []byte(configs[dir]), nil, nil
		case "compose ps -a --format json":
			psCalls++
		}
		return nil, nil, nil
	}

	tests := []struct {
		path string
		want models.ServiceStatus
	}{
		{"/srv/shop", models.StatusRunning},
		{"/srv/blog", models.StatusStopped},
	}
	for _, tt := range tests {
		got, err := e.Status(context.Background(), &models.Service{ID: tt.path, Path: tt.path})
		if err != nil {
			t.Fatalf("Status(%s): %v", tt.path, err)
		}
		if got != tt.want {
			t.Errorf("Status(%s) = %q, want %q", tt.path, got, tt.want)
		}
	}
	if psCalls != 0 {
		t.Errorf("docker compose ps ran %d times, want 0 with the API available", psCalls)
	}
	if n := atomic.LoadInt32(&apiCalls); n != 1 {
		t.Errorf("Docker API called %d times for two projects, want 1 shared listing", n)
	}
}

func TestComposeStatusFallsBackToCLI(t *testing.T) {
	docker := &DockerClient{http: http.DefaultClient, baseURL: "http://127.0.0.1:1"} // nothing listens
	e := NewComposeExecutorWithDocker(docker)
	e.run = fakeRunner(map[string]fakeResult{
		"compose config --format json": {stdout: `{"name":"app","services":{"web":{"restart":"always"}}}`},
		"compose ps -a --format json":  {stdout: psLine("web", "running", "", 0, false)},
	})
	got, err := e.Status(context.Background(), &models.Service{ID: "app", Path: "/srv/app"})
	if err != nil || got != models.StatusRunning {
		t.Fatalf("Status = %q, %v; want running via CLI fallback", got, err)
	}
}
