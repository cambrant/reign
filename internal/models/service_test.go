package models

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "test.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Verify database file exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}

	// Verify tables exist
	tables := []string{"services", "service_state", "service_events"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s does not exist: %v", table, err)
		}
	}
}

func TestServiceCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Create
	service := &Service{
		ID:             "test-service",
		Name:           "Test Service",
		Type:           ServiceTypeCompose,
		Path:           "/home/test/app",
		Enabled:        true,
		Infrastructure: false,
	}

	if err := service.Create(db); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Read
	got, err := GetServiceByID(db, "test-service")
	if err != nil {
		t.Fatalf("GetServiceByID failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetServiceByID returned nil")
	}
	if got.Name != "Test Service" {
		t.Errorf("Name = %q, want %q", got.Name, "Test Service")
	}
	if got.Type != ServiceTypeCompose {
		t.Errorf("Type = %q, want %q", got.Type, ServiceTypeCompose)
	}

	// Verify state was created
	state, err := GetServiceState(db, "test-service")
	if err != nil {
		t.Fatalf("GetServiceState failed: %v", err)
	}
	if state == nil {
		t.Fatal("service state was not created")
	}
	if state.Status != StatusStopped {
		t.Errorf("initial status = %q, want %q", state.Status, StatusStopped)
	}

	// Update
	service.Name = "Updated Name"
	service.Infrastructure = true
	if err := service.Update(db); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err = GetServiceByID(db, "test-service")
	if err != nil {
		t.Fatalf("GetServiceByID after update failed: %v", err)
	}
	if got.Name != "Updated Name" {
		t.Errorf("Name after update = %q, want %q", got.Name, "Updated Name")
	}
	if !got.Infrastructure {
		t.Error("Infrastructure should be true after update")
	}

	// Delete
	if err := DeleteService(db, "test-service"); err != nil {
		t.Fatalf("DeleteService failed: %v", err)
	}

	got, err = GetServiceByID(db, "test-service")
	if err != nil {
		t.Fatalf("GetServiceByID after delete failed: %v", err)
	}
	if got != nil {
		t.Error("service should be nil after delete")
	}
}

func TestListServices(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Create multiple services
	services := []Service{
		{ID: "app-a", Name: "App A", Type: ServiceTypeCompose, Path: "/a", Enabled: true},
		{ID: "app-b", Name: "App B", Type: ServiceTypeBinary, Path: "/b", Enabled: true},
		{ID: "app-c", Name: "App C", Type: ServiceTypeCompose, Path: "/c", Enabled: false},
	}

	for _, s := range services {
		svc := s
		if err := svc.Create(db); err != nil {
			t.Fatalf("Create failed for %s: %v", s.ID, err)
		}
	}

	// List all
	list, err := ListServices(db)
	if err != nil {
		t.Fatalf("ListServices failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("ListServices returned %d services, want 3", len(list))
	}
}

func TestListEnabledServicesForStartup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Create services with different priorities
	services := []Service{
		{ID: "webapp", Name: "Web App", Type: ServiceTypeCompose, Path: "/web", Enabled: true, Infrastructure: false},
		{ID: "redis", Name: "Redis", Type: ServiceTypeCompose, Path: "/redis", Enabled: true, Infrastructure: true},
		{ID: "postgres", Name: "PostgreSQL", Type: ServiceTypeCompose, Path: "/pg", Enabled: true, Infrastructure: true},
		{ID: "disabled", Name: "Disabled", Type: ServiceTypeCompose, Path: "/dis", Enabled: false, Infrastructure: false},
	}

	for _, s := range services {
		svc := s
		if err := svc.Create(db); err != nil {
			t.Fatalf("Create failed for %s: %v", s.ID, err)
		}
	}

	// List for startup
	list, err := ListEnabledServicesForStartup(db)
	if err != nil {
		t.Fatalf("ListEnabledServicesForStartup failed: %v", err)
	}

	// Should have 3 enabled services
	if len(list) != 3 {
		t.Fatalf("got %d services, want 3", len(list))
	}

	// Infrastructure services should come first (postgres, redis alphabetically)
	if list[0].ID != "postgres" {
		t.Errorf("first service = %q, want postgres", list[0].ID)
	}
	if list[1].ID != "redis" {
		t.Errorf("second service = %q, want redis", list[1].ID)
	}
	if list[2].ID != "webapp" {
		t.Errorf("third service = %q, want webapp", list[2].ID)
	}
}

func TestServiceState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Create a service
	service := &Service{
		ID:      "test",
		Name:    "Test",
		Type:    ServiceTypeCompose,
		Path:    "/test",
		Enabled: true,
	}
	if err := service.Create(db); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update status
	if err := UpdateServiceStatus(db, "test", StatusStarting); err != nil {
		t.Fatalf("UpdateServiceStatus failed: %v", err)
	}

	state, _ := GetServiceState(db, "test")
	if state.Status != StatusStarting {
		t.Errorf("status = %q, want %q", state.Status, StatusStarting)
	}

	// Set started
	pid := 12345
	if err := SetServiceStarted(db, "test", &pid); err != nil {
		t.Fatalf("SetServiceStarted failed: %v", err)
	}

	state, _ = GetServiceState(db, "test")
	if state.Status != StatusRunning {
		t.Errorf("status = %q, want %q", state.Status, StatusRunning)
	}
	if state.PID == nil || *state.PID != 12345 {
		t.Error("PID not set correctly")
	}
	if state.StartedAt == nil {
		t.Error("StartedAt not set")
	}

	// Set failed
	if err := SetServiceFailed(db, "test", "connection refused"); err != nil {
		t.Fatalf("SetServiceFailed failed: %v", err)
	}

	state, _ = GetServiceState(db, "test")
	if state.Status != StatusFailed {
		t.Errorf("status = %q, want %q", state.Status, StatusFailed)
	}
	if state.LastError == nil || *state.LastError != "connection refused" {
		t.Error("LastError not set correctly")
	}

	// Increment restart count
	if err := IncrementRestartCount(db, "test"); err != nil {
		t.Fatalf("IncrementRestartCount failed: %v", err)
	}
	if err := IncrementRestartCount(db, "test"); err != nil {
		t.Fatalf("IncrementRestartCount failed: %v", err)
	}

	state, _ = GetServiceState(db, "test")
	if state.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2", state.RestartCount)
	}

	// Reset restart count
	if err := ResetRestartCount(db, "test"); err != nil {
		t.Fatalf("ResetRestartCount failed: %v", err)
	}

	state, _ = GetServiceState(db, "test")
	if state.RestartCount != 0 {
		t.Errorf("RestartCount after reset = %d, want 0", state.RestartCount)
	}

	// Set stopped
	if err := SetServiceStopped(db, "test"); err != nil {
		t.Fatalf("SetServiceStopped failed: %v", err)
	}

	state, _ = GetServiceState(db, "test")
	if state.Status != StatusStopped {
		t.Errorf("status = %q, want %q", state.Status, StatusStopped)
	}
	if state.StoppedAt == nil {
		t.Error("StoppedAt not set")
	}
}

func TestServiceEvents(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Create a service
	service := &Service{
		ID:      "test",
		Name:    "Test",
		Type:    ServiceTypeCompose,
		Path:    "/test",
		Enabled: true,
	}
	if err := service.Create(db); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Log events
	if err := LogEvent(db, "test", "started", "Service started successfully"); err != nil {
		t.Fatalf("LogEvent failed: %v", err)
	}
	if err := LogEvent(db, "test", "stopped", ""); err != nil {
		t.Fatalf("LogEvent failed: %v", err)
	}

	// Get events
	events, err := GetServiceEvents(db, "test", 10)
	if err != nil {
		t.Fatalf("GetServiceEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	// Events should be in reverse chronological order
	if events[0].EventType != "stopped" {
		t.Errorf("first event type = %q, want stopped", events[0].EventType)
	}
	if events[1].Message != "Service started successfully" {
		t.Errorf("second event message = %q, want 'Service started successfully'", events[1].Message)
	}
}

func TestDisabledServiceInitialState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Create a disabled service
	service := &Service{
		ID:      "disabled",
		Name:    "Disabled Service",
		Type:    ServiceTypeCompose,
		Path:    "/disabled",
		Enabled: false,
	}
	if err := service.Create(db); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Check initial state is disabled
	state, err := GetServiceState(db, "disabled")
	if err != nil {
		t.Fatalf("GetServiceState failed: %v", err)
	}
	if state.Status != StatusDisabled {
		t.Errorf("status = %q, want %q", state.Status, StatusDisabled)
	}
}
