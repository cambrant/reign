package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reign/internal/executor"
	"github.com/reign/internal/models"
)

type fakeProjects struct {
	projects []executor.ComposeProject
	err      error
}

func (f fakeProjects) ComposeProjects(ctx context.Context) ([]executor.ComposeProject, error) {
	return f.projects, f.err
}

func composeService(id, path string) models.Service {
	return models.Service{ID: id, Name: id, Type: models.ServiceTypeCompose, Path: path}
}

func TestUnmanagedProjects(t *testing.T) {
	services := []models.Service{
		composeService("app", "/srv/app"),
		composeService("trailing", "/srv/trailing/"),
		{ID: "bin", Type: models.ServiceTypeBinary, Path: "/opt/bin"},
	}
	tests := []struct {
		name string
		dir  string
		want bool // reported as unmanaged
	}{
		{"managed path", "/srv/app", false},
		{"managed path with trailing slash in service", "/srv/trailing", false},
		{"unclean working dir", "/srv/./app/", false},
		{"unknown project", "/home/tim/conduit", true},
		{"prefix of a managed path is not managed", "/srv/ap", true},
		{"binary service path does not count", "/opt/bin", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unmanagedProjects([]executor.ComposeProject{{Name: "p", WorkingDir: tt.dir}}, composePaths(services))
			if (len(got) == 1) != tt.want {
				t.Errorf("unmanaged = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunsCompose(t *testing.T) {
	tests := []struct {
		name string
		unit string
		want bool
	}{
		{"compose v2", "[Service]\nExecStart=/usr/bin/docker compose up\n", true},
		{"compose v1", "[Service]\nExecStart=/usr/local/bin/docker-compose up -d\n", true},
		{"compose in ExecStop only", "[Service]\nExecStart=/bin/true\nExecStop=/usr/bin/docker compose down\n", true},
		{"mentioned in a comment only", "# replaced docker compose\n[Service]\nExecStart=/srv/app/run\n", false},
		{"plain docker run", "[Service]\nExecStart=/usr/bin/docker run nginx\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runsCompose([]byte(tt.unit)); got != tt.want {
				t.Errorf("runsCompose = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchPath(t *testing.T) {
	paths := map[string]string{"/srv/app": "app", "/srv/app-db": "appdb"}
	tests := []struct {
		name   string
		unit   string
		wantID string
	}{
		{"WorkingDirectory", "WorkingDirectory=/srv/app\nExecStart=docker compose up\n", "app"},
		{"compose file flag", "ExecStart=docker compose -f /srv/app/compose.yml up\n", "app"},
		{"quoted", `ExecStart=docker compose --project-directory "/srv/app" up`, "app"},
		{"longer path wins", "WorkingDirectory=/srv/app-db\n", "appdb"},
		{"longer path after a non-match", "ExecStart=x /srv/application /srv/app-db\n", "appdb"},
		{"prefix only", "WorkingDirectory=/srv/application\n", ""},
		{"at end of file", "WorkingDirectory=/srv/app", "app"},
		{"unrelated", "WorkingDirectory=/home/tim/conduit\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, _, ok := matchPath([]byte(tt.unit), paths)
			if id != tt.wantID || ok != (tt.wantID != "") {
				t.Errorf("matchPath = %q, %v, want %q", id, ok, tt.wantID)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	unitDir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(unitDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app-compose.service", "[Service]\nWorkingDirectory=/srv/app\nExecStart=/usr/bin/docker compose up\nRestart=always\n")
	write("other-compose.service", "[Service]\nWorkingDirectory=/srv/other\nExecStart=/usr/bin/docker compose up\n")
	write("app-backup.service", "[Service]\nExecStart=/usr/local/bin/backup /srv/app\n")
	write("reign.service", "[Service]\nExecStart=/usr/local/bin/reign\nWorkingDirectory=/srv/app\n# docker compose\nExecStartPre=docker compose version\n")
	write("app-compose.timer", "[Timer]\n# docker compose /srv/app\n")

	var systemctlArgs []string
	d := &Doctor{
		Projects: fakeProjects{projects: []executor.ComposeProject{
			{Name: "app", WorkingDir: "/srv/app", Containers: 2, Running: 2},
			{Name: "conduit", WorkingDir: "/home/tim/conduit", Containers: 1, Running: 1},
		}},
		UnitDir: unitDir,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			systemctlArgs = append([]string{name}, args...)
			return []byte("Id=app-compose.service\nUnitFileState=enabled\nActiveState=failed\nNRestarts=12\n"), nil
		},
	}

	r := d.Check(context.Background(), []models.Service{composeService("app", "/srv/app")})

	if len(r.UnmanagedProjects) != 1 || r.UnmanagedProjects[0].Name != "conduit" {
		t.Errorf("unmanaged projects = %+v, want only conduit", r.UnmanagedProjects)
	}
	want := Unit{Name: "app-compose.service", File: filepath.Join(unitDir, "app-compose.service"),
		ServiceID: "app", Path: "/srv/app", Enabled: "enabled", Active: "failed", Restarts: 12}
	if len(r.ConflictingUnits) != 1 || r.ConflictingUnits[0] != want {
		t.Errorf("conflicting units = %+v, want [%+v]", r.ConflictingUnits, want)
	}
	if got := strings.Join(systemctlArgs, " "); !strings.HasPrefix(got, "systemctl show") || !strings.HasSuffix(got, "app-compose.service") {
		t.Errorf("systemctl called as %q", got)
	}
	if r.Issues() != 2 || len(r.Warnings) != 0 {
		t.Errorf("issues = %d, warnings = %v", r.Issues(), r.Warnings)
	}
}

func TestCheckDegrades(t *testing.T) {
	tests := []struct {
		name         string
		doctor       *Doctor
		wantWarnings int
	}{
		{"docker unreachable", &Doctor{Projects: fakeProjects{err: errors.New("connection refused")}, UnitDir: "."}, 1},
		{"no unit directory", &Doctor{UnitDir: filepath.Join(os.TempDir(), "reign-no-such-dir")}, 1},
		{"both fail", &Doctor{Projects: fakeProjects{err: errors.New("down")}, UnitDir: filepath.Join(os.TempDir(), "reign-no-such-dir")}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.doctor.Check(context.Background(), nil)
			if len(r.Warnings) != tt.wantWarnings {
				t.Errorf("warnings = %v, want %d", r.Warnings, tt.wantWarnings)
			}
			if r.UnmanagedProjects == nil || r.ConflictingUnits == nil {
				t.Error("report lists must be empty, not nil, so the JSON has [] not null")
			}
		})
	}
}
