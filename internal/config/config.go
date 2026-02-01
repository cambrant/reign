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
	ListenAddr          string        `json:"listenAddr"`
	DatabasePath        string        `json:"databasePath"`
	LogLevel            string        `json:"logLevel"`
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
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:          "127.0.0.1:7890",
		DatabasePath:        "/var/lib/reign/reign.db",
		LogLevel:            "info",
		StartupTimeout:      5 * time.Minute,
		StartupTimeoutStr:   "5m",
		HealthCheckInterval: 30 * time.Second,
		HealthCheckStr:      "30s",
		RestartDelay:        30 * time.Second,
		RestartDelayStr:     "30s",
		MaxRestarts:         5,
		ShutdownTimeout:     30 * time.Second,
		ShutdownTimeoutStr:  "30s",
		KeepRunning:         true,
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

// Load reads configuration from a file, applying defaults for unspecified fields.
func Load(path string) (*Config, error) {
	config := DefaultConfig()

	// If no config file specified, use defaults
	if path == "" {
		return config, nil
	}

	// Read config file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Unmarshal into existing config (overrides defaults)
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
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

	return nil
}
