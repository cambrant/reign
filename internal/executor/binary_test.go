package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reign/internal/models"
)

func TestValidateWorkDir(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		dir     string
		wantErr string
	}{
		{"exists", tmp, ""},
		{"missing", filepath.Join(tmp, "nope"), "does not exist"},
		{"not a dir", file, "is not a directory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorkDir(tt.dir)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestUnitName(t *testing.T) {
	tests := []struct{ id, want string }{
		{"runbook", "reign-runbook.service"},
		{"semio-api", "reign-semio-api.service"},
		{"tc_1.2", "reign-tc_1.2.service"},
		{"a b/c", `reign-a\x20b\x2fc.service`},
	}
	for _, tt := range tests {
		if got := UnitName(tt.id); got != tt.want {
			t.Errorf("UnitName(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestUnitStateToStatus(t *testing.T) {
	tests := []struct {
		state string
		want  models.ServiceStatus
	}{
		{"active", models.StatusRunning},
		{"reloading", models.StatusRunning},
		{"activating", models.StatusStarting},
		{"deactivating", models.StatusStopping},
		{"failed", models.StatusFailed},
		{"inactive", models.StatusStopped},
		{"", models.StatusStopped},
	}
	for _, tt := range tests {
		if got := unitStateToStatus(tt.state); got != tt.want {
			t.Errorf("unitStateToStatus(%q) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestFormatSystemdEnv(t *testing.T) {
	got := formatSystemdEnv([]string{
		"PLAIN=value",
		"DSN=postgres://u:p@h/db?sslmode=disable&x=$HOME",
		`QUOTE=it's "quoted" \ $x`,
		"EMPTY=",
		"1BAD=skipped",
	})
	want := "PLAIN='value'\n" +
		"DSN='postgres://u:p@h/db?sslmode=disable&x=$HOME'\n" +
		`QUOTE="it's \"quoted\" \\ \$x"` + "\n" +
		"EMPTY=''\n"
	if got != want {
		t.Errorf("formatSystemdEnv =\n%s\nwant\n%s", got, want)
	}
}

// fakeSystemd records systemctl/systemd-run invocations and answers ActiveState.
type fakeSystemd struct {
	state   string
	cmds    []string
	envFile string // contents of the EnvironmentFile at systemd-run time
	stopErr error
}

func (f *fakeSystemd) run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
	switch {
	case name == "systemctl" && args[0] == "show" && args[2] == "ActiveState":
		return []byte(f.state + "\n"), nil, nil
	case name == "systemctl" && args[0] == "stop":
		if f.stopErr != nil {
			return nil, []byte("Failed to stop reign-app.service: Unit reign-app.service not loaded."), f.stopErr
		}
		return nil, nil, nil
	case name == "systemd-run":
		for _, a := range args {
			if p, ok := strings.CutPrefix(a, "--property=EnvironmentFile="); ok {
				data, _ := os.ReadFile(p)
				f.envFile = string(data)
			}
		}
		return nil, nil, nil
	}
	return nil, nil, nil
}

func newSystemdExecutor(t *testing.T, f *fakeSystemd) *BinaryExecutor {
	t.Helper()
	return NewBinaryExecutorWithOptions(BinaryOptions{
		Mode:        BinaryModeSystemd,
		EnvDir:      t.TempDir(),
		StopTimeout: 20 * time.Second,
		Run:         f.run,
	})
}

func TestSystemdStart(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "app.env")
	if err := os.WriteFile(envPath, []byte("export TOKEN=\"s3cr3t\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	f := &fakeSystemd{state: "inactive"}
	e := newSystemdExecutor(t, f)
	svc := &models.Service{ID: "app", Name: "myapp", Type: models.ServiceTypeBinary,
		Command: "/usr/local/bin/myapp serve --port 8080", WorkDir: dir, EnvFile: envPath}

	if err := e.Start(context.Background(), svc); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var run string
	for _, c := range f.cmds {
		if strings.HasPrefix(c, "systemd-run ") {
			run = c
		}
	}
	for _, want := range []string{
		"--unit=reign-app.service",
		"--service-type=exec",
		"--property=SyslogIdentifier=myapp",
		"--property=WorkingDirectory=" + dir,
		"--property=TimeoutStopSec=20",
		"-- /usr/local/bin/myapp serve --port 8080",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("systemd-run missing %q:\n%s", want, run)
		}
	}
	if f.envFile != "TOKEN='s3cr3t'\n" {
		t.Errorf("env file = %q", f.envFile)
	}
	entries, _ := os.ReadDir(e.envDir)
	if len(entries) != 0 {
		t.Errorf("env file left behind after start: %v", entries)
	}
}

func TestSystemdStartAlreadyRunning(t *testing.T) {
	f := &fakeSystemd{state: "active"}
	e := newSystemdExecutor(t, f)
	err := e.Start(context.Background(), &models.Service{ID: "app", Name: "app", Command: "/bin/sleep 100"})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Start of an active unit: err = %v, want already running", err)
	}
}

func TestSystemdStop(t *testing.T) {
	tests := []struct {
		name    string
		stopErr error
	}{
		{"running unit", nil},
		{"unit not loaded is not an error", errors.New("exit status 5")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeSystemd{stopErr: tt.stopErr}
			e := newSystemdExecutor(t, f)
			if err := e.Stop(context.Background(), &models.Service{ID: "app", Name: "app"}); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if f.cmds[0] != "systemctl stop reign-app.service" {
				t.Errorf("first command = %q", f.cmds[0])
			}
		})
	}
}
