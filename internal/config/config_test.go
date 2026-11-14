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

func TestBinaryRunnerValidation(t *testing.T) {
	tests := []struct {
		runner  string
		wantErr bool
	}{
		{"auto", false},
		{"systemd", false},
		{"direct", false},
		{"docker", true},
	}
	for _, tt := range tests {
		t.Run(tt.runner, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"binaryRunner":"`+tt.runner+`"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load(binaryRunner=%q) err = %v, wantErr %v", tt.runner, err, tt.wantErr)
			}
		})
	}
}

func TestExternalStopGrace(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"0", 0, false},
		{"15m", 15 * time.Minute, false},
		{"-1m", 0, true},
		{"soon", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"externalStopGrace":"`+tt.value+`"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && cfg.ExternalStopGrace != tt.want {
				t.Errorf("ExternalStopGrace = %s, want %s", cfg.ExternalStopGrace, tt.want)
			}
		})
	}
}

func TestLogLevelPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		env        string
		wantLevel  string
		wantSource string
		wantErr    bool
	}{
		{"default", `{}`, "", "info", "default", false},
		{"config file", `{"logLevel":"warn"}`, "", "warn", "config", false},
		{"environment wins over file", `{"logLevel":"info"}`, "debug", "debug", LogLevelEnv, false},
		{"invalid environment value", `{}`, "loud", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(LogLevelEnv, tt.env)
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.file), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if cfg.LogLevel != tt.wantLevel || cfg.LogLevelSource != tt.wantSource {
				t.Errorf("log level = %s from %s, want %s from %s", cfg.LogLevel, cfg.LogLevelSource, tt.wantLevel, tt.wantSource)
			}
		})
	}
}
