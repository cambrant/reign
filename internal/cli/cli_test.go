package cli

import "testing"

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
