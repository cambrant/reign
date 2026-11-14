package models

import (
	"path/filepath"
	"testing"
)

func TestInitDBPragmas(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "reign.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		pragma string
		want   string
	}{
		{"busy_timeout", "5000"},
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
	}
	for _, tt := range tests {
		var got string
		if err := db.QueryRow("PRAGMA " + tt.pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", tt.pragma, err)
		}
		if got != tt.want {
			t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, got, tt.want)
		}
	}
}

func TestDeleteCascadesToStateAndEvents(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "reign.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := &Service{ID: "app", Name: "app", Type: ServiceTypeCompose, Path: "/srv/app", Enabled: true}
	if err := s.Create(db); err != nil {
		t.Fatal(err)
	}
	LogEvent(db, "app", "started", "")
	if err := DeleteService(db, "app"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"service_state", "service_events"} {
		var n int
		db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE service_id = 'app'").Scan(&n)
		if n != 0 {
			t.Errorf("%s still has %d rows for a deleted service", table, n)
		}
	}
}

func TestStoppedStatusFollowsEnabledFlag(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		want    ServiceStatus
	}{
		{"enabled service reads stopped", true, StatusStopped},
		{"disabled service reads disabled", false, StatusDisabled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := InitDB(filepath.Join(t.TempDir(), "reign.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			s := &Service{ID: "app", Name: "app", Type: ServiceTypeCompose, Path: "/srv/app", Enabled: tt.enabled}
			if err := s.Create(db); err != nil {
				t.Fatal(err)
			}
			SetServiceStarted(db, "app", nil)
			if err := SetServiceStopped(db, "app"); err != nil {
				t.Fatal(err)
			}
			state, _ := GetServiceState(db, "app")
			if state.Status != tt.want {
				t.Errorf("status = %q, want %q", state.Status, tt.want)
			}
		})
	}
}

// Databases from older versions have disabled services stuck at "stopped";
// opening them normalizes the status.
func TestInitDBNormalizesDisabledStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reign.db")
	db, err := InitDB(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{ID: "old", Name: "old", Type: ServiceTypeCompose, Path: "/srv/old", Enabled: false}
	if err := s.Create(db); err != nil {
		t.Fatal(err)
	}
	db.Exec(`UPDATE service_state SET status = 'stopped' WHERE service_id = 'old'`)
	db.Close()

	db, err = InitDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, _ := GetServiceState(db, "old")
	if state.Status != StatusDisabled {
		t.Errorf("status = %q after reopening, want disabled", state.Status)
	}
}
