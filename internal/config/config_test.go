package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ListenAddr != "127.0.0.1:7890" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:7890")
	}

	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}

	if cfg.StartupTimeout != 5*time.Minute {
		t.Errorf("StartupTimeout = %v, want %v", cfg.StartupTimeout, 5*time.Minute)
	}

	if cfg.MaxRestarts != 5 {
		t.Errorf("MaxRestarts = %d, want %d", cfg.MaxRestarts, 5)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		configJSON  string
		wantErr     bool
		errContains string
		check       func(*testing.T, *Config)
	}{
		{
			name:       "empty path uses defaults",
			configJSON: "",
			wantErr:    false,
			check: func(t *testing.T, c *Config) {
				if c.ListenAddr != "127.0.0.1:7890" {
					t.Errorf("ListenAddr = %q, want default", c.ListenAddr)
				}
			},
		},
		{
			name: "valid config overrides defaults",
			configJSON: `{
				"listenAddr": "0.0.0.0:8080",
				"databasePath": "/tmp/test.db",
				"logLevel": "debug"
			}`,
			wantErr: false,
			check: func(t *testing.T, c *Config) {
				if c.ListenAddr != "0.0.0.0:8080" {
					t.Errorf("ListenAddr = %q, want %q", c.ListenAddr, "0.0.0.0:8080")
				}
				if c.DatabasePath != "/tmp/test.db" {
					t.Errorf("DatabasePath = %q, want %q", c.DatabasePath, "/tmp/test.db")
				}
				if c.LogLevel != "debug" {
					t.Errorf("LogLevel = %q, want %q", c.LogLevel, "debug")
				}
			},
		},
		{
			name: "valid duration strings",
			configJSON: `{
				"listenAddr": "127.0.0.1:7890",
				"databasePath": "/tmp/test.db",
				"startupTimeout": "10m",
				"healthCheckInterval": "1m",
				"restartDelay": "45s"
			}`,
			wantErr: false,
			check: func(t *testing.T, c *Config) {
				if c.StartupTimeout != 10*time.Minute {
					t.Errorf("StartupTimeout = %v, want %v", c.StartupTimeout, 10*time.Minute)
				}
				if c.HealthCheckInterval != time.Minute {
					t.Errorf("HealthCheckInterval = %v, want %v", c.HealthCheckInterval, time.Minute)
				}
				if c.RestartDelay != 45*time.Second {
					t.Errorf("RestartDelay = %v, want %v", c.RestartDelay, 45*time.Second)
				}
			},
		},
		{
			name: "invalid JSON",
			configJSON: `{
				"listenAddr": "127.0.0.1:7890",
			}`,
			wantErr:     true,
			errContains: "failed to parse",
		},
		{
			name: "invalid log level",
			configJSON: `{
				"listenAddr": "127.0.0.1:7890",
				"databasePath": "/tmp/test.db",
				"logLevel": "verbose"
			}`,
			wantErr:     true,
			errContains: "invalid logLevel",
		},
		{
			name: "invalid duration string",
			configJSON: `{
				"listenAddr": "127.0.0.1:7890",
				"databasePath": "/tmp/test.db",
				"startupTimeout": "notaduration"
			}`,
			wantErr:     true,
			errContains: "invalid startupTimeout",
		},
		{
			name: "empty listen address",
			configJSON: `{
				"listenAddr": "",
				"databasePath": "/tmp/test.db"
			}`,
			wantErr:     true,
			errContains: "listenAddr cannot be empty",
		},
		{
			name: "empty database path",
			configJSON: `{
				"listenAddr": "127.0.0.1:7890",
				"databasePath": ""
			}`,
			wantErr:     true,
			errContains: "databasePath cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var configPath string

			if tt.configJSON != "" {
				// Create temporary config file
				tmpDir := t.TempDir()
				configPath = filepath.Join(tmpDir, "config.json")
				if err := os.WriteFile(configPath, []byte(tt.configJSON), 0644); err != nil {
					t.Fatalf("failed to write test config: %v", err)
				}
			}

			cfg, err := Load(configPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestLoadNonExistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("error = %q, want containing 'failed to read'", err.Error())
	}
}
