// Package logger provides a simple level-based logging package.
package logger

import (
	"fmt"
	"log"
	"os"
)

// LogLevel represents the severity of a log message.
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

var (
	currentLevel = INFO
	logger       = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.LUTC)
)

// levelNames maps log levels to their string representations.
var levelNames = map[LogLevel]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
	FATAL: "FATAL",
}

// SetLevel sets the minimum log level. Messages below this level are ignored.
func SetLevel(level LogLevel) {
	currentLevel = level
}

// SetLevelFromString sets the log level from a string name.
func SetLevelFromString(level string) error {
	switch level {
	case "debug":
		currentLevel = DEBUG
	case "info":
		currentLevel = INFO
	case "warn":
		currentLevel = WARN
	case "error":
		currentLevel = ERROR
	default:
		return fmt.Errorf("unknown log level: %s", level)
	}
	return nil
}

// GetLevel returns the current log level.
func GetLevel() LogLevel {
	return currentLevel
}

// Debug logs a debug message.
func Debug(format string, v ...interface{}) {
	if currentLevel <= DEBUG {
		logger.Printf("[DEBUG] "+format, v...)
	}
}

// Info logs an informational message.
func Info(format string, v ...interface{}) {
	if currentLevel <= INFO {
		logger.Printf("[INFO] "+format, v...)
	}
}

// Warn logs a warning message.
func Warn(format string, v ...interface{}) {
	if currentLevel <= WARN {
		logger.Printf("[WARN] "+format, v...)
	}
}

// Error logs an error message.
func Error(format string, v ...interface{}) {
	if currentLevel <= ERROR {
		logger.Printf("[ERROR] "+format, v...)
	}
}

// Fatal logs a fatal message and exits the application.
func Fatal(format string, v ...interface{}) {
	logger.Printf("[FATAL] "+format, v...)
	os.Exit(1)
}
