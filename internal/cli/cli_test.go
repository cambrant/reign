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
