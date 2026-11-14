package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunHelpFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantHandled bool
	}{
		{"double dash help", []string{"reign", "--help"}, true},
		{"single dash help", []string{"reign", "-help"}, true},
		{"short help", []string{"reign", "-h"}, true},
		{"help command", []string{"reign", "help"}, true},
		{"server flag goes to serve", []string{"reign", "-config", "x.json"}, false},
		{"explicit serve", []string{"reign", "serve"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handled, err := Run(tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if handled != tt.wantHandled {
				t.Errorf("handled = %v, want %v", handled, tt.wantHandled)
			}
		})
	}
}

func TestDeleteConfirmation(t *testing.T) {
	tests := []struct {
		name        string
		yes         bool
		interactive bool
		input       string
		wantDelete  bool
		wantErr     string
	}{
		{"yes skips prompt without a terminal", true, false, "", true, ""},
		{"no terminal without yes refuses", false, false, "y\n", false, "stdin is not a terminal"},
		{"interactive confirm", false, true, "y\n", true, ""},
		{"interactive confirm long form", false, true, "YES\n", true, ""},
		{"interactive decline", false, true, "n\n", false, "aborted"},
		{"interactive empty answer", false, true, "", false, "aborted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deleted := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete && r.URL.Path == "/services/app" {
					deleted = true
				}
				w.Write([]byte(`{"status":"deleted"}`))
			}))
			defer srv.Close()

			cmd := NewDeleteCommand(NewClient(srv.URL), "app", tt.yes)
			cmd.in = strings.NewReader(tt.input)
			cmd.interactive = tt.interactive

			err := cmd.Run()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}
			if deleted != tt.wantDelete {
				t.Errorf("deleted = %v, want %v", deleted, tt.wantDelete)
			}
		})
	}
}

func TestFormatRequester(t *testing.T) {
	tests := []struct {
		name                  string
		user, sudo, host, tty string
		want                  string
	}{
		{"plain user on a terminal", "tim", "", "kraut", "pts/0", "tim@kraut pts/0 ppid=42"},
		{"sudo reports the invoking user", "root", "tim", "kraut", "pts/1", "tim@kraut pts/1 sudo ppid=42"},
		{"no terminal (cron, script)", "deploy", "", "kraut", "", "deploy@kraut ppid=42"},
		{"unknown user", "", "", "", "", "unknown ppid=42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRequester(tt.user, tt.sudo, tt.host, tt.tty, 42); got != tt.want {
				t.Errorf("formatRequester = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientSendsRequester(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(requesterHeader)
		w.Write([]byte(`{"id":"app","state":{"status":"stopping"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.requester = "tim@kraut pts/0 ppid=42"
	if _, err := c.RestartService("app", false); err != nil {
		t.Fatal(err)
	}
	if got != "tim@kraut pts/0 ppid=42" {
		t.Errorf("X-Reign-Requester = %q", got)
	}
}
