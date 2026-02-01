// Package version provides version information for Reign.
package version

import "fmt"

// These variables are set at build time via ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

// Get returns a formatted version string.
func Get() string {
	return fmt.Sprintf("reign %s (commit: %s, built: %s)", Version, GitCommit, BuildTime)
}

// Short returns just the version number.
func Short() string {
	return Version
}
