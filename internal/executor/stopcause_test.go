package executor

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/reign/internal/models"
)

func inspectLine(name, status string, code int, oom bool) string {
	o := "false"
	if oom {
		o = "true"
	}
	return "/" + name + "|" + status + "|" + strconv.Itoa(code) + "|" + o + "|app"
}

func TestComposeStopCause(t *testing.T) {
	tests := []struct {
		name         string
		ids          string
		inspect      string
		events       string
		wantExternal bool
		wantOOM      bool
		wantDetail   string
	}{
		{
			name:         "containers removed by compose down",
			ids:          "",
			wantExternal: true,
			wantDetail:   "containers were removed",
		},
		{
			name:         "docker compose stop for a cold backup",
			ids:          "abc\ndef",
			inspect:      inspectLine("app-web-1", "exited", 0, false) + "\n" + inspectLine("app-db-1", "exited", 0, false),
			events:       "kill app-web-1\nstop app-web-1\nkill app-db-1\nstop app-db-1\n",
			wantExternal: true,
			wantDetail:   "stop app-web-1",
		},
		{
			name:       "crash without a stop request",
			ids:        "abc",
			inspect:    inspectLine("app-web-1", "exited", 1, false),
			wantDetail: "app-web-1 exited with code 1",
		},
		{
			name:       "OOM kill wins over stop events",
			ids:        "abc\ndef",
			inspect:    inspectLine("app-web-1", "running", 0, false) + "\n" + inspectLine("app-db-1", "exited", 7, true),
			events:     "kill app-web-1\n",
			wantOOM:    true,
			wantDetail: "app-db-1 was OOM killed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewComposeExecutor(func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
				switch {
				case strings.Join(args, " ") == "compose ps -a -q":
					return []byte(tt.ids), []byte(versionWarning), nil
				case args[0] == "inspect":
					return []byte(tt.inspect), nil, nil
				case args[0] == "events":
					if !strings.Contains(strings.Join(args, " "), "label=com.docker.compose.project=app") {
						t.Errorf("events not filtered by project: %v", args)
					}
					return []byte(tt.events), nil, nil
				}
				t.Fatalf("unexpected command %v", args)
				return nil, nil, nil
			})
			cause, err := e.StopCause(context.Background(), &models.Service{ID: "app", Path: "/srv/app"}, time.Now().Add(-time.Hour))
			if err != nil {
				t.Fatalf("StopCause: %v", err)
			}
			if cause.External != tt.wantExternal || cause.OOMKilled != tt.wantOOM {
				t.Errorf("cause = %+v, want external=%v oom=%v", cause, tt.wantExternal, tt.wantOOM)
			}
			if !strings.Contains(cause.Detail, tt.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", cause.Detail, tt.wantDetail)
			}
		})
	}
}

func TestClassifyUnit(t *testing.T) {
	tests := []struct {
		name         string
		props        string
		wantExternal bool
		wantOOM      bool
	}{
		{"unloaded after clean stop", "ActiveState=inactive\nResult=success\nExecMainCode=0\nExecMainStatus=0\n", true, false},
		{"failed with exit code", "ActiveState=failed\nResult=exit-code\nExecMainCode=1\nExecMainStatus=2\n", false, false},
		{"oom kill", "ActiveState=failed\nResult=oom-kill\nExecMainCode=2\nExecMainStatus=9\n", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := classifyUnit(parseProperties([]byte(tt.props)))
			if c.External != tt.wantExternal || c.OOMKilled != tt.wantOOM {
				t.Errorf("classifyUnit = %+v, want external=%v oom=%v", c, tt.wantExternal, tt.wantOOM)
			}
		})
	}
}

func TestClassifyWaitStatus(t *testing.T) {
	tests := []struct {
		name         string
		ws           syscall.WaitStatus
		wantExternal bool
	}{
		{"exit 0", syscall.WaitStatus(0 << 8), true},
		{"exit 1", syscall.WaitStatus(1 << 8), false},
		{"SIGTERM", syscall.WaitStatus(syscall.SIGTERM), true},
		{"SIGKILL", syscall.WaitStatus(syscall.SIGKILL), false},
		{"SIGSEGV", syscall.WaitStatus(syscall.SIGSEGV), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if c := classifyWaitStatus(tt.ws); c.External != tt.wantExternal {
				t.Errorf("classifyWaitStatus = %+v, want external=%v", c, tt.wantExternal)
			}
		})
	}
}
