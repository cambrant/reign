// Package config handles configuration loading and validation for Reign.
package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

// Config holds all configuration for Reign.
type Config struct {
	ListenAddr   string `json:"listenAddr"`
	DatabasePath string `json:"databasePath"`
	LogLevel     string `json:"logLevel"`
	// LogLevelSource says where LogLevel came from: "default", "config" or
	// "REIGN_LOG_LEVEL" (the environment variable wins over the file).
	LogLevelSource      string        `json:"-"`
	StartupTimeout      time.Duration `json:"-"`
	StartupTimeoutStr   string        `json:"startupTimeout"`
	HealthCheckInterval time.Duration `json:"-"`
	HealthCheckStr      string        `json:"healthCheckInterval"`
	RestartDelay        time.Duration `json:"-"`
	RestartDelayStr     string        `json:"restartDelay"`
	MaxRestarts         int           `json:"maxRestarts"`
	ShutdownTimeout     time.Duration `json:"-"`
	ShutdownTimeoutStr  string        `json:"shutdownTimeout"`
	KeepRunning         bool          `json:"keepRunning"`
	// ExternalStopGrace is how long a service stopped outside reign (docker
	// stop, systemctl stop) stays down before reign restarts it. 0 disables
	// automatic restarts after external stops.
	ExternalStopGrace    time.Duration `json:"-"`
	ExternalStopGraceStr string        `json:"externalStopGrace"`
	// BinaryRunner selects how binary services run: "systemd" (each in its own
	// transient unit, survives reign restarts), "direct" (child processes of
	// reign), or "auto" (systemd when systemd-run is available).
	BinaryRunner string `json:"binaryRunner"`
	// DockerSocket is the Docker Engine API socket used for cheap status
	// checks. Empty disables the API and uses the docker CLI only.
	DockerSocket string `json:"dockerSocket"`
	// RuntimeDir holds short-lived files such as env files for transient units.
	RuntimeDir string `json:"runtimeDir"`
	// APIAuth controls who may change services through the API: "peer"
	// allows root and AdminGroup members, identified by the UID owning the
	// loopback client socket; "none" allows anyone who can reach the API.
	APIAuth string `json:"apiAuth"`
	// AdminGroup is the group whose members may change services.
	AdminGroup string `json:"adminGroup"`
	// AllowedHosts are extra Host header names the API accepts, besides IP
	// addresses, localhost, the listen host and this machine's hostname.
	AllowedHosts []string `json:"allowedHosts"`
}

// API auth modes.
const (
	APIAuthPeer = "peer"
	APIAuthNone = "none"
)

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:           "127.0.0.1:7890",
		DatabasePath:         "/var/lib/reign/reign.db",
		LogLevel:             "info",
		StartupTimeout:       5 * time.Minute,
		StartupTimeoutStr:    "5m",
		HealthCheckInterval:  30 * time.Second,
		HealthCheckStr:       "30s",
		RestartDelay:         30 * time.Second,
		RestartDelayStr:      "30s",
		MaxRestarts:          5,
		ShutdownTimeout:      30 * time.Second,
		ShutdownTimeoutStr:   "30s",
		KeepRunning:          true,
		ExternalStopGrace:    time.Hour,
		ExternalStopGraceStr: "1h",
		BinaryRunner:         "auto",
		RuntimeDir:           "/run/reign",
		DockerSocket:         "/var/run/docker.sock",
		APIAuth:              APIAuthPeer,
		AdminGroup:           "reign",
	}
}

// ParseFlags parses command-line flags and returns configuration options.
func ParseFlags() (configPath string, showVersion bool) {
	flag.StringVar(&configPath, "config", "", "Path to configuration file")
	flag.StringVar(&configPath, "c", "", "Path to configuration file (shorthand)")
	flag.BoolVar(&showVersion, "version", false, "Show version information")
	flag.BoolVar(&showVersion, "v", false, "Show version information (shorthand)")
	flag.Parse()
	return configPath, showVersion
}

// LogLevelEnv overrides the configured log level when set.
const LogLevelEnv = "REIGN_LOG_LEVEL"

// Load reads configuration from a file, applying defaults for unspecified
// fields. REIGN_LOG_LEVEL, when set, overrides the file's logLevel.
func Load(path string) (*Config, error) {
	config := DefaultConfig()
	config.LogLevelSource = "default"

	// If no config file specified, use defaults
	if path == "" {
		if err := config.applyEnv(); err != nil {
			return nil, err
		}
		return config, nil
	}

	// Read config file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Unmarshal into existing config (overrides defaults)
	defaultLevel := config.LogLevel
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	if config.LogLevel != defaultLevel {
		config.LogLevelSource = "config"
	}

	if err := config.applyEnv(); err != nil {
		return nil, err
	}

	// Parse duration strings
	if err := config.parseDurations(); err != nil {
		return nil, err
	}

	// Validate configuration
	if err := config.validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// applyEnv applies environment overrides. An explicitly set environment
// variable wins over the file, as for any other tool; the source is kept so
// startup can log which one is in effect.
func (c *Config) applyEnv() error {
	if level := os.Getenv(LogLevelEnv); level != "" {
		c.LogLevel = level
		c.LogLevelSource = LogLevelEnv
		switch level {
		case "debug", "info", "warn", "error":
		default:
			return fmt.Errorf("invalid %s: %s (must be debug, info, warn, or error)", LogLevelEnv, level)
		}
	}
	return nil
}

// parseDurations converts string duration fields to time.Duration.
func (c *Config) parseDurations() error {
	var err error

	if c.StartupTimeoutStr != "" {
		c.StartupTimeout, err = time.ParseDuration(c.StartupTimeoutStr)
		if err != nil {
			return fmt.Errorf("invalid startupTimeout: %w", err)
		}
	}

	if c.HealthCheckStr != "" {
		c.HealthCheckInterval, err = time.ParseDuration(c.HealthCheckStr)
		if err != nil {
			return fmt.Errorf("invalid healthCheckInterval: %w", err)
		}
	}

	if c.RestartDelayStr != "" {
		c.RestartDelay, err = time.ParseDuration(c.RestartDelayStr)
		if err != nil {
			return fmt.Errorf("invalid restartDelay: %w", err)
		}
	}

	if c.ExternalStopGraceStr != "" {
		c.ExternalStopGrace, err = time.ParseDuration(c.ExternalStopGraceStr)
		if err != nil {
			return fmt.Errorf("invalid externalStopGrace: %w", err)
		}
	}

	if c.ShutdownTimeoutStr != "" {
		c.ShutdownTimeout, err = time.ParseDuration(c.ShutdownTimeoutStr)
		if err != nil {
			return fmt.Errorf("invalid shutdownTimeout: %w", err)
		}
	}

	return nil
}

// validate checks that all configuration values are valid.
func (c *Config) validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listenAddr cannot be empty")
	}

	if c.DatabasePath == "" {
		return fmt.Errorf("databasePath cannot be empty")
	}

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("invalid logLevel: %s (must be debug, info, warn, or error)", c.LogLevel)
	}

	if c.StartupTimeout <= 0 {
		return fmt.Errorf("startupTimeout must be positive")
	}

	if c.HealthCheckInterval <= 0 {
		return fmt.Errorf("healthCheckInterval must be positive")
	}

	if c.MaxRestarts < 0 {
		return fmt.Errorf("maxRestarts cannot be negative")
	}

	if c.ExternalStopGrace < 0 {
		return fmt.Errorf("externalStopGrace cannot be negative")
	}

	switch c.BinaryRunner {
	case "auto", "systemd", "direct":
	default:
		return fmt.Errorf("invalid binaryRunner: %s (must be auto, systemd, or direct)", c.BinaryRunner)
	}

	switch c.APIAuth {
	case APIAuthPeer, APIAuthNone:
	default:
		return fmt.Errorf("invalid apiAuth: %s (must be peer or none)", c.APIAuth)
	}

	return nil
}
